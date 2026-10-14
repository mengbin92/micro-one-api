package server

import (
	"context"
	"fmt"
	v "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/contractdto"
	"time"
)

// Actual native capture reports once after durable recording; no supplier replay.
func (s *HTTPServer) reportContractResult(ctx context.Context, attempt string, r evidence.Record) {
	if r.ParameterContract == nil || r.Execution == "checkpoint" || s.channelClient == nil {
		return
	}
	tag := r.ParameterContract
	if attempt == "" {
		attempt = r.Observation.ProviderRequestID
	}
	if attempt == "" {
		attempt = fmt.Sprint(r.StartedAtUnixNano, ":", tag.Revision)
	}
	raw, _ := jsonx.Marshal(r)
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, _ = s.channelClient.RecordContractResult(call, &v.RecordContractResultRequest{Scope: contractdto.ScopeProto(tag.Scope), Revision: tag.Revision, BindingRevision: tag.BindingRevision, IncidentRevision: tag.IncidentRevision, AttemptId: attempt, RecordJson: raw, CapabilityValid: r.Complete})
}
func (s *HTTPServer) withContractResults(ctx context.Context, attempt string) context.Context {
	return evidence.WithContractReporter(ctx, func(r evidence.Record) { s.reportContractResult(ctx, attempt, r) })
}
