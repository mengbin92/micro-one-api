// Package biz owns bounded operator task plans. These tasks never authorize
// supplier invoices, customer charging or upstream contract publication.
package biz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"micro-one-api/pkg/jsonx"
	"regexp"
	"time"
)

type Plan struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Enabled         bool   `json:"enabled"`
	PayloadFile     string `json:"payload_file"`
	PayloadDigest   string `json:"payload_digest"`
	IntervalSeconds int64  `json:"interval_seconds"`
	ExpiresAt       int64  `json:"expires_at"`
	MaxRuns         int    `json:"max_runs"`
}
type Claim struct {
	Key, PlanID, Kind string
	StartedAt         int64
}
type Receipt struct {
	Status     string `json:"status"`
	ArtifactID string `json:"artifact_id"`
	Reason     string `json:"reason"`
}
type TaskRepo interface {
	Claim(context.Context, Plan, time.Time, int) (*Claim, error)
	Finish(context.Context, Claim, Receipt, time.Time) error
	Expire(context.Context, time.Time) error
}
type Handler interface {
	Run(context.Context, Plan, Claim) (Receipt, error)
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,80}$`)

func (p Plan) Validate(now time.Time) error {
	if !identifier.MatchString(p.ID) || !identifier.MatchString(p.PayloadFile) || len(p.PayloadDigest) != 64 || p.IntervalSeconds < 3600 || p.IntervalSeconds > 7*86400 || p.ExpiresAt <= now.Unix() || p.ExpiresAt > now.Add(30*24*time.Hour).Unix() || p.MaxRuns < 1 || p.MaxRuns > 100 {
		return fmt.Errorf("bounded fixed task identity, interval, expiry and run budget required")
	}
	switch p.Kind {
	case "cost":
	case "repair":
		if p.MaxRuns != 1 {
			return fmt.Errorf("one repair invocation per immutable plan required")
		}
	case "weekly":
		if p.IntervalSeconds < 7*86400 {
			return fmt.Errorf("weekly review interval must be at least seven days")
		}
	default:
		return fmt.Errorf("unsupported task kind")
	}
	return nil
}
func (p Plan) Hash() string { b, _ := jsonx.Marshal(p); return fmt.Sprintf("%x", sha256.Sum256(b)) }
func (p Plan) Key(now time.Time) string {
	slot := now.Unix() / p.IntervalSeconds
	if p.Kind == "repair" {
		slot = 0
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", p.ID, p.Hash(), slot))))
}

func Tick(ctx context.Context, repo TaskRepo, handler Handler, plans []Plan, kind string, dailyBudget int, now time.Time) error {
	if dailyBudget < 1 || dailyBudget > 48 || len(plans) > 100 {
		return fmt.Errorf("bounded daily task budget required")
	}
	if err := repo.Expire(ctx, now); err != nil {
		return err
	}
	for _, p := range plans {
		if !p.Enabled || p.Kind != kind {
			continue
		}
		if err := p.Validate(now); err != nil {
			return err
		}
		claim, err := repo.Claim(ctx, p, now, dailyBudget)
		if err != nil {
			return err
		}
		if claim == nil {
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		receipt, runErr := handler.Run(callCtx, p, *claim)
		cancel()
		if runErr != nil {
			receipt = Receipt{Status: "failed", Reason: "handler_failed"}
		}
		if receipt.Status != "succeeded" && receipt.Status != "attention" && receipt.Status != "failed" {
			receipt = Receipt{Status: "failed", Reason: "invalid_handler_receipt"}
		}
		if err := repo.Finish(ctx, *claim, receipt, time.Now().UTC()); err != nil {
			return err
		}
	}
	return nil
}
