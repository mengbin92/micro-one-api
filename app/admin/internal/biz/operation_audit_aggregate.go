package biz

import (
	"context"
	"fmt"
	"sort"
)

type AggregatedOperationAuditRepo struct {
	local   OperationAuditRepo
	sources []OperationAuditRepo
}

const AggregatedOperationAuditCap = 5000

func NewAggregatedOperationAuditRepo(local OperationAuditRepo, sources ...OperationAuditRepo) *AggregatedOperationAuditRepo {
	out := &AggregatedOperationAuditRepo{local: local}
	for _, source := range sources {
		if source != nil {
			out.sources = append(out.sources, source)
		}
	}
	return out
}

func (r *AggregatedOperationAuditRepo) List(ctx context.Context, filter OperationAuditFilter) (OperationAuditBatch, error) {
	if r == nil || r.local == nil {
		return OperationAuditBatch{}, ErrOperationAuditUnavailable
	}
	if filter.Offset < 0 || filter.Limit <= 0 || filter.Limit > AggregatedOperationAuditCap || filter.Offset > AggregatedOperationAuditCap-filter.Limit {
		return OperationAuditBatch{}, fmt.Errorf("%w: pagination is limited to %d rows; narrow the time range or filters", ErrInvalidOperationAuditQuery, AggregatedOperationAuditCap)
	}
	fetch := filter
	fetch.Offset = 0
	fetch.Limit = filter.Offset + filter.Limit
	merged := OperationAuditBatch{Items: make([]OperationAudit, 0)}
	seen := make(map[string]struct{}, len(r.sources)+1)
	for _, repo := range append([]OperationAuditRepo{r.local}, r.sources...) {
		batch, err := repo.List(ctx, fetch)
		if err != nil {
			return OperationAuditBatch{}, err
		}
		if batch.StoreID == "" || batch.Total < 0 || int64(len(batch.Items)) != min(batch.Total, int64(fetch.Limit)) {
			return OperationAuditBatch{}, ErrOperationAuditUnavailable
		}
		// Several owners can share one physical audit table; count its rows once.
		if _, exists := seen[batch.StoreID]; exists {
			continue
		}
		seen[batch.StoreID] = struct{}{}
		merged.Items = append(merged.Items, batch.Items...)
		merged.Total += batch.Total
	}
	sort.Slice(merged.Items, func(i, j int) bool {
		if merged.Items[i].OccurredAt != merged.Items[j].OccurredAt {
			return merged.Items[i].OccurredAt > merged.Items[j].OccurredAt
		}
		return merged.Items[i].EventID > merged.Items[j].EventID
	})
	if filter.Offset >= len(merged.Items) {
		merged.Items = []OperationAudit{}
		return merged, nil
	}
	merged.Items = merged.Items[filter.Offset:]
	if filter.Limit < len(merged.Items) {
		merged.Items = merged.Items[:filter.Limit]
	}
	return merged, nil
}
