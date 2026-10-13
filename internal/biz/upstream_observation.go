package biz

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"micro-one-api/domain/upstream/provider"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

type UpstreamObserver interface {
	RecordUpstreamObservation(context.Context, provider.Observation) error
}

func upstreamFailureKind(ctx context.Context, err error) string {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || IsLocalAdmissionError(err) || IsPostForwardError(err) {
		return ""
	}
	var configuration *provider.ConfigurationError
	if errors.As(err, &configuration) {
		return "configuration"
	}
	if provider.IsPreForwardError(err) || IsProtocolCapabilityMismatch(err) || isUpstreamPolicyRejection(err) {
		return ""
	}
	if isUpstreamModelUnavailable(err) {
		return "model"
	}
	switch UpstreamStatus(err) {
	case 429, 423, 529:
		return "capacity"
	case 401, 403, 404:
		return "configuration"
	default:
		return ""
	}
}

func recordUpstreamObservation(ctx context.Context, client any, ch *Channel, err error, responseTime int64, startedAt time.Time) bool {
	observer, ok := client.(UpstreamObserver)
	if !ok || ch == nil || ch.UpstreamConfigurationRevision == "" {
		return false
	}
	outcome := UpstreamHealthDisposition(ctx, err)
	kind := upstreamFailureKind(ctx, err)
	if outcome == provider.HealthNeutral && kind == "" && ch.HealthProbeID == "" {
		return true
	}
	o := provider.Observation{
		SourceKind: UpstreamSourceChannel, SourceID: ch.ID, ConfigurationRevision: ch.UpstreamConfigurationRevision,
		ProbeID: ch.HealthProbeID, ID: uuid.NewString(), StartedAt: startedAt, Outcome: outcome,
		FailureKind: kind, UpstreamModelID: ch.UpstreamModelID, ResponseTime: responseTime,
	}
	if ch.SubscriptionAccountID > 0 {
		o.SourceKind, o.SourceID = UpstreamSourceSubscription, ch.SubscriptionAccountID
	}
	if err != nil {
		o.Error = applogger.SanitizeAndTruncate(err.Error(), 2048)
	}
	var httpError *provider.UpstreamHTTPError
	if errors.As(err, &httpError) {
		o.RetryAfter = min(httpError.RetryAfter, 5*time.Minute)
	}
	if UpstreamStatus(err) == 529 && o.RetryAfter <= 0 {
		o.RetryAfter = 30 * time.Second
	}
	reportCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 200*time.Millisecond)
	defer cancel()
	if err := observer.RecordUpstreamObservation(reportCtx, o); err != nil {
		metrics.RelayUpstreamObservationFailures.Inc()
		applogger.Log.Warn("record upstream observation failed", zap.String("source_kind", o.SourceKind), zap.Int64("source_id", o.SourceID), zap.Error(err))
	}
	return true
}

func (uc *RelayUsecase) RecordUpstreamResult(ctx context.Context, ch *Channel, err error, responseTime int64, startedAt time.Time) {
	if uc == nil || ch == nil {
		return
	}
	if recordUpstreamObservation(ctx, uc.channel, ch, err, responseTime, startedAt) {
		return
	}
	uc.RecordRoutingSourceHealth(ctx, ch, err, responseTime)
}
