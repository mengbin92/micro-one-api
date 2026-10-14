// upstream-rollout runs only in an operator environment, after reviewer/CI
// approval. Canary inference remains under the existing monitor's budget.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	v "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/repair"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/contractdto"
)

type ownerClient struct {
	base, token string
	client      *http.Client
	calls, max  int
}

func (c *ownerClient) request(ctx context.Context, method, path string, body []byte, reply proto.Message) (int, error) {
	if c.calls >= c.max {
		return 0, fmt.Errorf("rollout control request budget exhausted")
	}
	c.calls++
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("invalid owner request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("owner unavailable; inspect binding before resuming")
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return resp.StatusCode, fmt.Errorf("owner response budget exceeded")
	}
	if resp.StatusCode != 200 {
		return resp.StatusCode, nil
	}
	if jsonx.ValidateUnique(raw) != nil || protojson.Unmarshal(raw, reply) != nil {
		return 200, fmt.Errorf("invalid owner response")
	}
	return 200, nil
}
func (c *ownerClient) state(ctx context.Context, scope contract.Scope) (repair.RolloutState, error) {
	q := url.Values{"source_kind": {scope.Kind}, "source_id": {fmt.Sprint(scope.ID)}, "upstream_model_id": {scope.Model}, "protocol": {scope.Protocol}, "page_size": {"1"}}
	var reply v.ListUpstreamContractsReply
	code, err := c.request(ctx, "GET", "/api/admin/upstream-contracts?"+q.Encode(), nil, &reply)
	if err != nil {
		return repair.RolloutState{}, err
	}
	if code != 200 || reply.Binding == nil {
		return repair.RolloutState{}, fmt.Errorf("owner binding read denied or unavailable (HTTP %d)", code)
	}
	b := reply.Binding
	if contractdto.Scope(b.Scope) != scope {
		return repair.RolloutState{}, fmt.Errorf("owner returned a different scope")
	}
	return repair.RolloutState{Current: b.CurrentRevision, Previous: b.PreviousRevision, Status: b.Status, Revision: b.Revision, IncidentRevision: b.IncidentRevision, StartedAt: b.StartedAt, CompleteAttempts: b.CompleteAttempts, Failures: b.Failures}, nil
}
func (c *ownerClient) transition(ctx context.Context, d contract.Document, next repair.RolloutDecision, gray int, until time.Time) (int, error) {
	if gray < 1 || gray > 5 {
		return 0, fmt.Errorf("gray percentage out of bounds")
	}
	p := &v.TransitionUpstreamContractRequest{Scope: contractdto.ScopeProto(d.Scope), Revision: next.Revision, ExpectedRevision: next.Expected, Action: next.Action, GrayPercent: int32(gray), Reason: "bounded reviewed repair " + d.Repair.ProposalID}
	if next.Action == "publish" {
		p.AutomaticUntil = until.Unix()
	}
	raw, err := protojson.Marshal(p)
	if err != nil {
		return 0, err
	}
	var out v.TransitionUpstreamContractReply
	return c.request(ctx, "POST", "/api/admin/upstream-contracts/transition", raw, &out)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func run() error {
	path := flag.String("reviewed-request", "", "signed protobuf JSON create request")
	apply := flag.Bool("apply", false, "explicitly run the approved operator rollout; otherwise validate only")
	gray := flag.Int("gray-percent", 1, "P2 initial gray percentage, 1 through 5")
	untilUnix := flag.Int64("until", 0, "fixed rollout expiration, within signed review validity")
	max := flag.Int("max-control-requests", 128, "control HTTP request budget, 8 through 256")
	flag.Parse()
	if *gray < 1 || *gray > 5 || *max < 8 || *max > 256 {
		return fmt.Errorf("bounded gray and control budget required")
	}
	f, err := os.Open(*path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || jsonx.ValidateUnique(raw) != nil {
		return fmt.Errorf("invalid signed request")
	}
	var p v.CreateUpstreamContractRequest
	if protojson.Unmarshal(raw, &p) != nil {
		return fmt.Errorf("invalid protobuf create request")
	}
	d := contractdto.Document(p.Document)
	if _, err := contract.Compile(d); err != nil {
		return err
	}
	var keys map[string]string
	if jsonx.Unmarshal([]byte(os.Getenv("UPSTREAM_REPAIR_REVIEW_KEYS")), &keys) != nil || d.Repair == nil {
		return fmt.Errorf("trusted reviewer keys and signed repair required")
	}
	if err := contract.VerifyRepairReview(d, keys, time.Now()); err != nil {
		return err
	}
	until := time.Unix(*untilUnix, 0)
	if !until.After(time.Now()) || until.Unix() > d.Repair.ExpiresAt || p.Reason == "" {
		return fmt.Errorf("fixed deadline within signed approval required")
	}
	if !*apply {
		fmt.Printf("validated bounded rollout: scope=%s revision=%s gray=%d until=%d; no publication\n", d.Scope.Key(), d.Revision(), *gray, until.Unix())
		return nil
	}
	base, token := os.Getenv("UPSTREAM_ROLLOUT_ADMIN_URL"), os.Getenv("UPSTREAM_ROLLOUT_OPERATOR_TOKEN")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost")) || token == "" || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("trusted HTTPS/local owner URL and dedicated operator credential required")
	}
	client := &ownerClient{base: strings.TrimSuffix(base, "/"), token: token, max: *max, client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	ctx, cancel := context.WithDeadline(context.Background(), until.Add(45*time.Second))
	defer cancel()
	// Create is idempotent and never changes an effective binding.
	var created v.CreateUpstreamContractReply
	code, err := client.request(ctx, "POST", "/api/admin/upstream-contracts", raw, &created)
	if err != nil {
		return err
	}
	if code != 200 || created.Revision != d.Revision() {
		return fmt.Errorf("owner did not accept the exact approved candidate (HTTP %d)", code)
	}
	for client.calls < client.max-3 {
		state, err := client.state(ctx, d.Scope)
		if err != nil {
			return err
		}
		next, err := repair.NextRollout(d, state, until, time.Now())
		if err != nil {
			return err
		}
		if next.Action == "complete" {
			fmt.Printf("approved repair active: %s\n", d.Revision())
			return nil
		}
		if next.Action != "wait" {
			code, err = client.transition(ctx, d, next, *gray, until)
			if err != nil {
				return err
			}
			if code != 200 && code != 409 {
				return fmt.Errorf("owner transition denied or unavailable (HTTP %d)", code)
			}
			if next.Action == "rollback" {
				if code != 200 {
					return fmt.Errorf("rollback proof unavailable; source remains isolated")
				}
				fmt.Println("candidate rolled back under fresh owner proof")
				return nil
			}
			if code == 200 {
				continue
			}
		}
		delay := time.Minute
		if remaining := time.Until(until); remaining < delay {
			delay = remaining
		}
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("rollout deadline; inspect owner binding")
		}
	}
	// Reserve cleanup control calls. A gray candidate without enough samples
	// is quarantined at the control budget boundary, never promoted by timeout.
	state, err := client.state(ctx, d.Scope)
	if err != nil {
		return err
	}
	if state.Revision < 0 {
		return fmt.Errorf("invalid owner binding revision")
	}
	if state.Current == d.Revision() && state.Status == "gray" {
		code, err := client.transition(ctx, d, repair.RolloutDecision{Action: "quarantine", Revision: d.Revision(), Expected: uint64(state.Revision)}, *gray, until)
		if err != nil {
			return err
		}
		if code != 200 {
			return fmt.Errorf("budget exhausted; owner quarantine needs attention (HTTP %d)", code)
		}
	}
	return fmt.Errorf("control budget exhausted; candidate not promoted")
}
