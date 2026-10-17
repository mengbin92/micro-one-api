package biz

import (
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/requesttrace"

	"github.com/go-kratos/kratos/v3/errors"
)

var ErrInvalidRequestTiming = errors.BadRequest(logv1.RequestTimingErrorReason_INVALID_REQUEST_TIMING.String(), "invalid request timing summary")

// prepareRequestTiming normalizes ownership and dedupe at the log owner. A
// producer cannot choose a key that collides with financial consumption logs.
func prepareRequestTiming(entry *LogEntry) error {
	if entry == nil {
		return ErrInvalidRequestTiming
	}
	if entry.Source != requesttrace.TimingSource {
		if entry.RequestTiming != nil {
			return ErrInvalidRequestTiming
		}
		return nil
	}
	s := entry.RequestTiming
	if err := s.Validate(); err != nil {
		return ErrInvalidRequestTiming
	}
	if entry.UserID != s.UserID || entry.RootRequestID != s.RootRequestID {
		return ErrInvalidRequestTiming
	}
	entry.RequestTiming = s.Clone()
	entry.DedupeKey = requesttrace.TimingDedupeKey(s.ObservationID)
	entry.Level = "info"
	// Timing rows carry no usage or content. They never enter usage aggregates,
	// even when a producer accidentally copies fields from a consumption row.
	entry.Message, entry.RequestID, entry.ReservationID = "", "", ""
	entry.AttemptNumber = 0
	entry.SourceKind, entry.UpstreamModelID = "", ""
	entry.Username, entry.TokenName, entry.ModelName = "", "", ""
	entry.Quota, entry.PromptTokens, entry.CompletionTokens = 0, 0, 0
	entry.CacheReadTokens, entry.CacheCreation5mTokens, entry.CacheCreation1hTokens = 0, 0, 0
	entry.ChannelID, entry.SubscriptionAccountID, entry.ElapsedTime = 0, 0, 0
	entry.IsStream = false
	entry.UncachedInputTokens, entry.ReportedPromptTokens = 0, 0
	entry.ReportedTotalTokens, entry.BillableTotalTokens = 0, 0
	entry.UsageSemantics, entry.UsageProtocol, entry.UsageFieldShape = "", "", ""
	entry.UsageParseStatus, entry.UsageDecisionReason = "", ""
	entry.UsageContractVersion, entry.CanonicalPresent = 0, false
	return nil
}
