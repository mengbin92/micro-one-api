package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/platform/metrics"

	applogger "micro-one-api/platform/logging"
	"go.uber.org/zap"
)

// RecordUnpricedRoutedMetric updates micro_one_api_model_unpriced_routed from a
// ListUnpricedRoutedModels response. A model routed through both a channel and a
// subscription counts in both gauges.
func RecordUnpricedRoutedMetric(resp *channelv1.ListUnpricedRoutedModelsResponse) {
	if metrics.UnpricedRoutedModels == nil || resp == nil {
		return
	}
	var channelCount, subscriptionCount float64
	for _, m := range resp.GetModels() {
		if m.GetChannelCount() > 0 {
			channelCount++
		}
		if m.GetSubscriptionCount() > 0 {
			subscriptionCount++
		}
	}
	metrics.UnpricedRoutedModels.WithLabelValues("channel").Set(channelCount)
	metrics.UnpricedRoutedModels.WithLabelValues("subscription").Set(subscriptionCount)
}

// loadPricedModelSet reads the ModelPrice system option and returns its keys as
// a canonicalised set. Returns nil when the option is absent or unparseable.
func (s *AdminService) loadPricedModelSet(ctx context.Context) []string {
	raw, err := s.GetSystemOption(ctx, "ModelPrice")
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	out := make([]string, 0, len(parsed))
	for k := range parsed {
		if k = strings.TrimSpace(k); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// EvaluateUnpricedRoutedMetric loads the priced-model set and updates the gauge.
func (s *AdminService) EvaluateUnpricedRoutedMetric(ctx context.Context) error {
	resp, err := s.ListUnpricedRoutedModelsWithPricing(ctx)
	if err != nil {
		return err
	}
	RecordUnpricedRoutedMetric(resp)
	return nil
}

// ListUnpricedRoutedModelsWithPricing returns the routed-but-unpriced models
// after loading the priced-model set from the ModelPrice system option.
func (s *AdminService) ListUnpricedRoutedModelsWithPricing(ctx context.Context) (*channelv1.ListUnpricedRoutedModelsResponse, error) {
	priced := s.loadPricedModelSet(ctx)
	return s.ListUnpricedRoutedModels(ctx, &channelv1.ListUnpricedRoutedModelsRequest{
		PricedModelIds: priced,
	})
}

// UnpricedRoutedMetricWorker keeps the unpriced-routed-model gauge fresh without
// requiring a human to open the admin page.
type UnpricedRoutedMetricWorker struct {
	svc      *AdminService
	interval time.Duration
}

// NewUnpricedRoutedMetricWorker creates a worker that evaluates the gauge every
// interval. The interval can be overridden with UNPRICED_ROUTED_METRIC_INTERVAL.
func NewUnpricedRoutedMetricWorker(svc *AdminService, interval time.Duration) *UnpricedRoutedMetricWorker {
	if d := parseDurationEnv("UNPRICED_ROUTED_METRIC_INTERVAL"); d > 0 {
		interval = d
	}
	return &UnpricedRoutedMetricWorker{svc: svc, interval: interval}
}

// Run starts the periodic evaluation loop. It stops when ctx is cancelled.
func (w *UnpricedRoutedMetricWorker) Run(ctx context.Context) {
	if w == nil || w.svc == nil {
		return
	}
	if err := w.evaluateOnce(ctx); err != nil {
		applogger.Log.Warn("unpriced routed metric initial evaluation failed", zap.Error(err))
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.evaluateOnce(ctx); err != nil {
				applogger.Log.Warn("unpriced routed metric evaluation failed", zap.Error(err))
			}
		}
	}
}

func (w *UnpricedRoutedMetricWorker) evaluateOnce(ctx context.Context) error {
	return w.svc.EvaluateUnpricedRoutedMetric(ctx)
}

func parseDurationEnv(key string) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		applogger.Log.Warn("invalid duration env", zap.String("key", key), zap.String("value", v), zap.Error(err))
		return 0
	}
	return d
}
