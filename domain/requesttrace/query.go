package requesttrace

type TraceDetail struct {
	Summary              *TimingSummary     `json:"summary"`
	DetailState          string             `json:"detail_state"`
	MissingReasons       []string           `json:"missing_reasons"`
	Spans                []TraceSpan        `json:"spans"`
	Milestones           []TraceMilestone   `json:"milestones"`
	ObservedAtMS         int64              `json:"observed_at_ms"`
	DetailRetentionHours int32              `json:"detail_retention_hours"`
	Truncated            bool               `json:"truncated"`
	RelatedOperations    []RelatedOperation `json:"related_operations,omitempty"`
	// SettlementCurrent is the optional live settlement state read from the
	// billing owner at query time. It is absent when the caller lacks billing
	// permission, billing is unavailable, or no attempt was reserved.
	SettlementCurrent *SettlementCurrent `json:"settlement_current,omitempty"`
}

// SettlementCurrent is the authoritative settlement state at query time, as
// opposed to Summary.SettlementSnapshot which froze at request end.
type SettlementCurrent struct {
	Status       string `json:"status"`
	ObservedAtMS int64  `json:"observed_at_ms"`
	Source       string `json:"source"`
}
type RelatedOperation struct {
	TraceID       string  `json:"trace_id"`
	SpanID        string  `json:"span_id"`
	Phase         string  `json:"phase"`
	StartOffsetMS float64 `json:"start_offset_ms"`
	DurationMS    float64 `json:"duration_ms"`
	Outcome       string  `json:"outcome"`
	Shared        bool    `json:"shared"`
}
type TraceSpan struct {
	ID             string            `json:"id"`
	ParentID       string            `json:"parent_id"`
	Service        string            `json:"service"`
	Phase          string            `json:"phase"`
	AttemptNumber  int32             `json:"attempt_number"`
	StartOffsetMS  float64           `json:"start_offset_ms"`
	DurationMS     float64           `json:"duration_ms"`
	Outcome        string            `json:"outcome"`
	SafeAttributes map[string]string `json:"safe_attributes"`
}
type TraceMilestone struct {
	Kind       string  `json:"kind"`
	OffsetMS   float64 `json:"offset_ms"`
	OutputKind string  `json:"output_kind"`
}
