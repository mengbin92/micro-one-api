// Package writeauditclient adapts owner audit RPCs to the admin read model.
package writeauditclient

import (
	"context"
	"google.golang.org/grpc/metadata"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	configv1 "micro-one-api/api/config/v1"
	logv1 "micro-one-api/api/log/v1"
	monitorv1 "micro-one-api/api/monitor/v1"
	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
)

// Owner reads use the admin system identity after the console's global IAM check.
func systemReadContext(ctx context.Context) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Delete("x-operator-authorization")
	md.Delete("x-authorization-reason")
	md.Delete("x-authorization-reason-bin")
	return metadata.NewOutgoingContext(authorization.WithCredential(ctx, ""), md)
}

type channelSource struct {
	client channelv1.ChannelServiceClient
}

func NewChannelSource(client channelv1.ChannelServiceClient) biz.OperationAuditRepo {
	return &channelSource{client: client}
}
func (s *channelSource) List(ctx context.Context, filter biz.OperationAuditFilter) (biz.OperationAuditBatch, error) {
	if s == nil || s.client == nil {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	if filter.Offset != 0 || filter.Limit < 1 || filter.Limit > 5000 {
		return biz.OperationAuditBatch{}, biz.ErrInvalidOperationAuditQuery
	}
	reply, err := s.client.ListWriteAudits(systemReadContext(ctx), &channelv1.ListWriteAuditsRequest{
		ActorUserId: filter.ActorUserID, Operation: filter.Operation, ResourceId: filter.ResourceID, Result: filter.Result,
		StartTimeMs: filter.StartTimeMS, EndTimeMs: filter.EndTimeMS, Page: 0, PageSize: int32(filter.Limit),
	})
	if err != nil {
		return biz.OperationAuditBatch{}, err
	}
	if reply == nil || reply.StoreId == "" || reply.Total < 0 || int64(len(reply.Items)) > reply.Total {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	items := make([]biz.OperationAudit, 0, len(reply.Items))
	for _, item := range reply.Items {
		if item == nil {
			return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
		}
		items = append(items, biz.OperationAudit{EventID: item.EventId, ActorUserID: item.ActorUserId, Operation: item.Operation,
			ResourceID: item.ResourceId, DecisionVersions: item.DecisionVersions, Reason: item.Reason, OccurredAt: item.OccurredAt, Result: item.Result})
	}
	return biz.OperationAuditBatch{Items: items, Total: reply.Total, StoreID: reply.StoreId}, nil
}

type billingSource struct {
	client billingv1.BillingServiceClient
}

func NewBillingSource(client billingv1.BillingServiceClient) biz.OperationAuditRepo {
	return &billingSource{client: client}
}
func (s *billingSource) List(ctx context.Context, filter biz.OperationAuditFilter) (biz.OperationAuditBatch, error) {
	if s == nil || s.client == nil {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	if filter.Offset != 0 || filter.Limit < 1 || filter.Limit > 5000 {
		return biz.OperationAuditBatch{}, biz.ErrInvalidOperationAuditQuery
	}
	reply, err := s.client.ListWriteAudits(systemReadContext(ctx), &billingv1.ListWriteAuditsRequest{
		ActorUserId: filter.ActorUserID, Operation: filter.Operation, ResourceId: filter.ResourceID, Result: filter.Result,
		StartTimeMs: filter.StartTimeMS, EndTimeMs: filter.EndTimeMS, Page: 0, PageSize: int32(filter.Limit),
	})
	if err != nil {
		return biz.OperationAuditBatch{}, err
	}
	if reply == nil || reply.StoreId == "" || reply.Total < 0 || int64(len(reply.Items)) > reply.Total {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	items := make([]biz.OperationAudit, 0, len(reply.Items))
	for _, item := range reply.Items {
		if item == nil {
			return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
		}
		items = append(items, biz.OperationAudit{EventID: item.EventId, ActorUserID: item.ActorUserId, Operation: item.Operation,
			ResourceID: item.ResourceId, DecisionVersions: item.DecisionVersions, Reason: item.Reason, OccurredAt: item.OccurredAt, Result: item.Result})
	}
	return biz.OperationAuditBatch{Items: items, Total: reply.Total, StoreID: reply.StoreId}, nil
}

type configSource struct{ client configv1.ConfigServiceClient }

func NewConfigSource(client configv1.ConfigServiceClient) biz.OperationAuditRepo {
	return &configSource{client: client}
}
func (s *configSource) List(ctx context.Context, filter biz.OperationAuditFilter) (biz.OperationAuditBatch, error) {
	if s == nil || s.client == nil {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	if filter.Offset != 0 || filter.Limit < 1 || filter.Limit > 5000 {
		return biz.OperationAuditBatch{}, biz.ErrInvalidOperationAuditQuery
	}
	reply, err := s.client.ListWriteAudits(systemReadContext(ctx), &configv1.ListWriteAuditsRequest{
		ActorUserId: filter.ActorUserID, Operation: filter.Operation, ResourceId: filter.ResourceID, Result: filter.Result,
		StartTimeMs: filter.StartTimeMS, EndTimeMs: filter.EndTimeMS, Page: 0, PageSize: int32(filter.Limit),
	})
	if err != nil {
		return biz.OperationAuditBatch{}, err
	}
	if reply == nil || reply.StoreId == "" || reply.Total < 0 || int64(len(reply.Items)) > reply.Total {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	items := make([]biz.OperationAudit, 0, len(reply.Items))
	for _, item := range reply.Items {
		if item == nil {
			return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
		}
		items = append(items, biz.OperationAudit{EventID: item.EventId, ActorUserID: item.ActorUserId, Operation: item.Operation,
			ResourceID: item.ResourceId, DecisionVersions: item.DecisionVersions, Reason: item.Reason, OccurredAt: item.OccurredAt, Result: item.Result})
	}
	return biz.OperationAuditBatch{Items: items, Total: reply.Total, StoreID: reply.StoreId}, nil
}

type logSource struct{ client logv1.LogServiceClient }

func NewLogSource(client logv1.LogServiceClient) biz.OperationAuditRepo {
	return &logSource{client: client}
}
func (s *logSource) List(ctx context.Context, filter biz.OperationAuditFilter) (biz.OperationAuditBatch, error) {
	if s == nil || s.client == nil {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	if filter.Offset != 0 || filter.Limit < 1 || filter.Limit > 5000 {
		return biz.OperationAuditBatch{}, biz.ErrInvalidOperationAuditQuery
	}
	reply, err := s.client.ListWriteAudits(systemReadContext(ctx), &logv1.ListWriteAuditsRequest{
		ActorUserId: filter.ActorUserID, Operation: filter.Operation, ResourceId: filter.ResourceID, Result: filter.Result,
		StartTimeMs: filter.StartTimeMS, EndTimeMs: filter.EndTimeMS, Page: 0, PageSize: int32(filter.Limit),
	})
	if err != nil {
		return biz.OperationAuditBatch{}, err
	}
	if reply == nil || reply.StoreId == "" || reply.Total < 0 || int64(len(reply.Items)) > reply.Total {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	items := make([]biz.OperationAudit, 0, len(reply.Items))
	for _, item := range reply.Items {
		if item == nil {
			return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
		}
		items = append(items, biz.OperationAudit{EventID: item.EventId, ActorUserID: item.ActorUserId, Operation: item.Operation,
			ResourceID: item.ResourceId, DecisionVersions: item.DecisionVersions, Reason: item.Reason, OccurredAt: item.OccurredAt, Result: item.Result})
	}
	return biz.OperationAuditBatch{Items: items, Total: reply.Total, StoreID: reply.StoreId}, nil
}

type monitorSource struct {
	client monitorv1.MonitorServiceClient
}

func NewMonitorSource(client monitorv1.MonitorServiceClient) biz.OperationAuditRepo {
	return &monitorSource{client: client}
}
func (s *monitorSource) List(ctx context.Context, filter biz.OperationAuditFilter) (biz.OperationAuditBatch, error) {
	if s == nil || s.client == nil {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	if filter.Offset != 0 || filter.Limit < 1 || filter.Limit > 5000 {
		return biz.OperationAuditBatch{}, biz.ErrInvalidOperationAuditQuery
	}
	reply, err := s.client.ListWriteAudits(systemReadContext(ctx), &monitorv1.ListWriteAuditsRequest{
		ActorUserId: filter.ActorUserID, Operation: filter.Operation, ResourceId: filter.ResourceID, Result: filter.Result,
		StartTimeMs: filter.StartTimeMS, EndTimeMs: filter.EndTimeMS, Page: 0, PageSize: int32(filter.Limit),
	})
	if err != nil {
		return biz.OperationAuditBatch{}, err
	}
	if reply == nil || reply.StoreId == "" || reply.Total < 0 || int64(len(reply.Items)) > reply.Total {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	items := make([]biz.OperationAudit, 0, len(reply.Items))
	for _, item := range reply.Items {
		if item == nil {
			return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
		}
		items = append(items, biz.OperationAudit{EventID: item.EventId, ActorUserID: item.ActorUserId, Operation: item.Operation,
			ResourceID: item.ResourceId, DecisionVersions: item.DecisionVersions, Reason: item.Reason, OccurredAt: item.OccurredAt, Result: item.Result})
	}
	return biz.OperationAuditBatch{Items: items, Total: reply.Total, StoreID: reply.StoreId}, nil
}

type notifySource struct{ client notifyv1.NotifyServiceClient }

func NewNotifySource(client notifyv1.NotifyServiceClient) biz.OperationAuditRepo {
	return &notifySource{client: client}
}
func (s *notifySource) List(ctx context.Context, filter biz.OperationAuditFilter) (biz.OperationAuditBatch, error) {
	if s == nil || s.client == nil {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	if filter.Offset != 0 || filter.Limit < 1 || filter.Limit > 5000 {
		return biz.OperationAuditBatch{}, biz.ErrInvalidOperationAuditQuery
	}
	reply, err := s.client.ListWriteAudits(systemReadContext(ctx), &notifyv1.ListWriteAuditsRequest{
		ActorUserId: filter.ActorUserID, Operation: filter.Operation, ResourceId: filter.ResourceID, Result: filter.Result,
		StartTimeMs: filter.StartTimeMS, EndTimeMs: filter.EndTimeMS, Page: 0, PageSize: int32(filter.Limit),
	})
	if err != nil {
		return biz.OperationAuditBatch{}, err
	}
	if reply == nil || reply.StoreId == "" || reply.Total < 0 || int64(len(reply.Items)) > reply.Total {
		return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
	}
	items := make([]biz.OperationAudit, 0, len(reply.Items))
	for _, item := range reply.Items {
		if item == nil {
			return biz.OperationAuditBatch{}, biz.ErrOperationAuditUnavailable
		}
		items = append(items, biz.OperationAudit{EventID: item.EventId, ActorUserID: item.ActorUserId, Operation: item.Operation,
			ResourceID: item.ResourceId, DecisionVersions: item.DecisionVersions, Reason: item.Reason, OccurredAt: item.OccurredAt, Result: item.Result})
	}
	return biz.OperationAuditBatch{Items: items, Total: reply.Total, StoreID: reply.StoreId}, nil
}
