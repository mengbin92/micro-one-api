// evidence-worker runs one isolated operator role per process. It reads fixed
// plans from a read-only input root and writes durable receipts/artifacts.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/app/billing/internal/data"
	ops "micro-one-api/domain/operations/biz"
	opsdata "micro-one-api/domain/operations/data"
	"micro-one-api/domain/upstream/repair"
	agentdata "micro-one-api/domain/upstream/repair/data"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/metrics"
)

type worker struct {
	inputs, state *os.Root
	client        *http.Client
}
type costPayload struct {
	Query         biz.VendorCostQuery `json:"query"`
	Manifest      biz.VendorManifest  `json:"manifest"`
	CSVFile       string              `json:"csv_file"`
	CSVDigest     string              `json:"csv_digest"`
	CredentialRef string              `json:"credential_ref"`
}
type repairPayload struct {
	Context        repair.AgentContext `json:"context"`
	FixturesFile   string              `json:"fixtures_file"`
	FixturesDigest string              `json:"fixtures_digest"`
}
type weeklyPayload struct {
	WindowDays int `json:"window_days"`
}

var fileID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func hash(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func (w *worker) read(file, digest string) ([]byte, error) {
	if !fileID.MatchString(file) {
		return nil, fmt.Errorf("exact input filename required")
	}
	f, err := w.inputs.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (4<<20)+1))
	if err != nil || len(b) > 4<<20 {
		return nil, fmt.Errorf("input budget exceeded")
	}
	if digest != "" && hash(b) != digest {
		return nil, fmt.Errorf("immutable input digest changed")
	}
	return b, nil
}
func decode(b []byte, out any) error {
	if err := jsonx.ValidateUnique(b); err != nil {
		return err
	}
	d := jsonx.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(out)
}
func (w *worker) save(key string, value any) (string, error) {
	if len(key) != 64 {
		return "", fmt.Errorf("fixed artifact key required")
	}
	b, err := jsonx.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := w.state.OpenFile(key+".json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	return hash(b), nil
}

func (w *worker) saveProposal(key string, p repair.Proposal) error {
	if len(key) != 64 {
		return fmt.Errorf("fixed candidate artifact key required")
	}
	b, err := jsonx.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	f, err := w.state.OpenFile(key+".proposal.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
	}
	return f.Sync()
}
func (w *worker) Run(ctx context.Context, p ops.Plan, c ops.Claim) (ops.Receipt, error) {
	raw, err := w.read(p.PayloadFile, p.PayloadDigest)
	if err != nil {
		return ops.Receipt{}, err
	}
	switch p.Kind {
	case "cost":
		var job costPayload
		if decode(raw, &job) != nil {
			return ops.Receipt{}, fmt.Errorf("invalid cost plan")
		}
		if !fileID.MatchString(job.CredentialRef) || len(job.CSVDigest) != 64 {
			return ops.Receipt{}, fmt.Errorf("fixed credential reference required")
		}
		csv, err := w.read(job.CSVFile, job.CSVDigest)
		if err != nil {
			return ops.Receipt{}, err
		}
		rows, err := biz.ReadVendorCSV(bytes.NewReader(csv), job.Manifest.Scope)
		if err != nil {
			return ops.Receipt{}, err
		}
		if err = job.Query.ValidateCSV(job.Manifest, rows); err != nil {
			return ops.Receipt{}, err
		}
		key := os.Getenv("P3_COST_" + job.CredentialRef)
		d := data.NewVendorCostAPIData(w.client)
		e, err := data.NewVendorCostReader(d, key).FetchCosts(ctx, job.Query)
		if err != nil {
			return ops.Receipt{}, err
		}
		id, err := w.save(c.Key, e)
		if err != nil {
			return ops.Receipt{}, err
		}
		status := "succeeded"
		if !e.CSVMatched {
			status = "attention"
		}
		return ops.Receipt{Status: status, ArtifactID: id, Reason: "provider_reported_archive_only"}, nil
	case "repair":
		var job repairPayload
		if decode(raw, &job) != nil {
			return ops.Receipt{}, fmt.Errorf("invalid repair plan")
		}
		if len(job.FixturesDigest) != 64 {
			return ops.Receipt{}, fmt.Errorf("fixed independent fixture digest required")
		}
		fixtureRaw, err := w.read(job.FixturesFile, job.FixturesDigest)
		if err != nil {
			return ops.Receipt{}, err
		}
		var fixtures repair.FixtureSet
		if decode(fixtureRaw, &fixtures) != nil {
			return ops.Receipt{}, fmt.Errorf("invalid independent fixtures")
		}
		generator := agentdata.NewAgent(&agentdata.Data{Client: w.client, Key: os.Getenv("P3_REPAIR_AGENT_KEY")})
		generation, genErr := generator.Generate(ctx, job.Context)
		var bundle repair.Bundle
		if genErr == nil {
			bundle, genErr = repair.Prepare(generation.Proposal, fixtures)
		}
		if generation.Proposal.Candidate.Scope.Validate() == nil {
			if err := w.saveProposal(c.Key, generation.Proposal); err != nil {
				return ops.Receipt{}, err
			}
		}
		status := "succeeded"
		reason := "review_required"
		if genErr != nil || bundle.Failures > 0 {
			status = "attention"
			reason = "generation_or_replay_needs_review"
		}
		id, err := w.save(c.Key, struct {
			Generation repair.Generation `json:"generation"`
			Bundle     repair.Bundle     `json:"independent_replay"`
			Status     string            `json:"status"`
		}{generation, bundle, reason})
		if err != nil {
			return ops.Receipt{}, err
		}
		return ops.Receipt{Status: status, ArtifactID: id, Reason: reason}, nil
	case "weekly":
		var job weeklyPayload
		if decode(raw, &job) != nil || job.WindowDays < 1 || job.WindowDays > 31 {
			return ops.Receipt{}, fmt.Errorf("bounded weekly window required")
		}
		base, token := os.Getenv("P3_REVIEW_ADMIN_URL"), os.Getenv("P3_REVIEW_OPERATOR_TOKEN")
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) || token == "" {
			return ops.Receipt{}, fmt.Errorf("trusted review owner and read credential required")
		}
		end := c.StartedAt
		start := end - int64(job.WindowDays)*86400
		// #nosec G704 -- URL is trusted operator configuration, validated above as HTTPS or loopback without userinfo/query/path; task and model output cannot choose it.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/admin/vendor-reports?"+url.Values{"start": {fmt.Sprint(start)}, "end": {fmt.Sprint(end)}, "limit": {"100"}}.Encode(), nil)
		if err != nil {
			return ops.Receipt{}, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		// #nosec G704 -- Same validated operator-only owner; production client rejects all redirects before forwarding the read credential.
		resp, err := w.client.Do(req)
		if err != nil {
			return ops.Receipt{}, fmt.Errorf("review owner unavailable")
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return ops.Receipt{}, fmt.Errorf("review owner HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, (4<<20)+1))
		if err != nil || len(body) > 4<<20 || jsonx.ValidateUnique(body) != nil {
			return ops.Receipt{}, fmt.Errorf("invalid weekly owner projection")
		}
		var view struct {
			Reports []jsonx.RawMessage      `json:"reports"`
			Review  *biz.VendorWeeklyReview `json:"weekly_review"`
		}
		if jsonx.Unmarshal(body, &view) != nil || view.Review == nil {
			return ops.Receipt{}, fmt.Errorf("weekly owner projection missing")
		}
		id, err := w.save(c.Key, jsonx.RawMessage(body))
		if err != nil {
			return ops.Receipt{}, err
		}
		status := "succeeded"
		if len(view.Reports) == 0 || view.Review.Pending > 0 || view.Review.Attention > 0 || view.Review.UnknownAmounts > 0 {
			status = "attention"
		}
		return ops.Receipt{Status: status, ArtifactID: id, Reason: "weekly_review_archived_no_external_message"}, nil
	}
	return ops.Receipt{}, fmt.Errorf("unknown worker role")
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func run() error {
	kind := flag.String("kind", "", "one role: cost, repair, weekly")
	inputDir := flag.String("inputs", "/inputs", "read-only reviewed input root")
	stateDir := flag.String("state", "/state", "private persistent task/artifact volume")
	once := flag.Bool("once", false, "one scheduler tick")
	metricsAddr := flag.String("metrics-addr", "", "optional private operator metrics listener")
	budget := flag.Int("daily-calls", 2, "fixed role daily invocation ceiling, 1..48")
	flag.Parse()
	if *kind != "cost" && *kind != "repair" && *kind != "weekly" {
		return fmt.Errorf("one isolated worker role required")
	}
	if *kind == "repair" {
		for _, name := range []string{"SERVICE_TOKEN", "SQL_DSN", "BILLING_SQL_DSN", "VENDOR_RECONCILE_DSN", "UPSTREAM_ROLLOUT_OPERATOR_TOKEN", "P3_REVIEW_OPERATOR_TOKEN", "UPSTREAM_REPAIR_REVIEW_KEYS"} {
			if os.Getenv(name) != "" {
				return fmt.Errorf("repair environment must exclude production/reviewer credentials")
			}
		}
		for _, entry := range os.Environ() {
			name, _, _ := strings.Cut(entry, "=")
			if strings.HasPrefix(name, "P3_COST_") || strings.HasPrefix(name, "P3_REVIEW_") || name == "ADMIN_TOKEN" {
				return fmt.Errorf("repair role may only receive its independent generator credential")
			}
		}
	}
	if !filepath.IsAbs(*stateDir) || !filepath.IsAbs(*inputDir) || filepath.Clean(*inputDir) == filepath.Clean(*stateDir) {
		return fmt.Errorf("separate absolute input/state roots required")
	}
	if err := os.MkdirAll(*stateDir, 0700); err != nil {
		return err
	}
	// #nosec G302 -- This is a private directory, not a file: owner traversal needs 0700; no group/world access is granted. Artifacts remain 0600.
	if err := os.Chmod(*stateDir, 0700); err != nil {
		return err
	}
	inputInfo, err := os.Stat(*inputDir)
	if err != nil {
		return err
	}
	stateInfo, err := os.Stat(*stateDir)
	if err != nil {
		return err
	}
	if os.SameFile(inputInfo, stateInfo) {
		return fmt.Errorf("input and state roots must be physically separate")
	}
	inputs, err := os.OpenRoot(*inputDir)
	if err != nil {
		return err
	}
	defer inputs.Close()
	state, err := os.OpenRoot(*stateDir)
	if err != nil {
		return err
	}
	defer state.Close()
	w := &worker{inputs: inputs, state: state, client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	d, err := opsdata.NewData(filepath.Join(*stateDir, "tasks.db"))
	if err != nil {
		return err
	}
	defer d.Close()
	repo := opsdata.NewTaskRepo(d)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	if *metricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		server := &http.Server{Addr: *metricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
		go func() { _ = server.ListenAndServe() }()
		defer server.Close()
	}
	for {
		raw, err := w.read("plans", "")
		if err != nil {
			return err
		}
		var plans []ops.Plan
		if err = decode(raw, &plans); err != nil {
			return err
		}
		if err = ops.Tick(ctx, repo, w, plans, *kind, *budget, time.Now().UTC()); err != nil {
			return err
		}
		reader := repo.(interface {
			Summary(context.Context, string) (map[string]int, error)
		})
		counts, err := reader.Summary(ctx, *kind)
		if err != nil {
			return err
		}
		for _, status := range []string{"running", "succeeded", "attention", "failed", "uncertain"} {
			metrics.OperatorTaskStates.WithLabelValues(*kind, status).Set(float64(counts[status]))
		}
		if *once {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}
