package service

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/notify/internal/biz"
	"micro-one-api/platform/authz"
	"net/http"
	"strconv"
	"strings"
)

func managementError(err error) error {
	switch {
	case errors.Is(err, biz.ErrInvalidNotification):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, biz.ErrNotificationConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, biz.ErrNotificationNotFound):
		return status.Error(codes.NotFound, err.Error())
	}
	return err
}
func (s *NotifyService) AcknowledgeNotification(ctx context.Context, req *notifyv1.AcknowledgeNotificationRequest) (*notifyv1.GetNotificationResponse, error) {
	n, err := s.uc.AcknowledgeNotification(ctx, req.Id, req.ExpectedRevision, req.Reason)
	if err != nil {
		return nil, managementError(err)
	}
	item, err := notificationToProto(n)
	return &notifyv1.GetNotificationResponse{Notification: item}, err
}
func (s *NotifyService) TestNotificationRule(ctx context.Context, req *notifyv1.TestNotificationRuleRequest) (*notifyv1.CreateNotificationResponse, error) {
	n, err := s.uc.TestNotificationRule(ctx, req.Id, req.Reason)
	if err != nil {
		return nil, managementError(err)
	}
	item, err := notificationToProto(n)
	return &notifyv1.CreateNotificationResponse{Notification: item}, err
}
func ruleProto(r *biz.NotificationRule) *notifyv1.NotificationRule {
	return &notifyv1.NotificationRule{Id: r.ID, Name: r.Name, Event: r.Event, Type: r.Type, Recipient: r.Recipient, Enabled: r.Enabled, Revision: r.Revision}
}
func (s *NotifyService) ListNotificationRules(ctx context.Context, _ *notifyv1.ListNotificationRulesRequest) (*notifyv1.ListNotificationRulesResponse, error) {
	rows, err := s.uc.ListNotificationRules(ctx)
	if err != nil {
		return nil, managementError(err)
	}
	out := &notifyv1.ListNotificationRulesResponse{}
	for _, r := range rows {
		out.Items = append(out.Items, ruleProto(r))
	}
	return out, nil
}
func (s *NotifyService) UpdateNotificationRule(ctx context.Context, req *notifyv1.UpdateNotificationRuleRequest) (*notifyv1.UpdateNotificationRuleResponse, error) {
	if req.Rule == nil {
		return nil, status.Error(codes.InvalidArgument, "rule required")
	}
	p := req.Rule
	r := &biz.NotificationRule{ID: p.Id, Name: p.Name, Event: p.Event, Type: p.Type, Recipient: p.Recipient, Enabled: p.Enabled, Revision: p.Revision}
	if err := s.uc.SaveNotificationRule(ctx, r, req.Reason); err != nil {
		return nil, managementError(err)
	}
	return &notifyv1.UpdateNotificationRuleResponse{Rule: ruleProto(r)}, nil
}
func readManagement(r *http.Request, p proto.Message) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	return protojson.Unmarshal(body, p)
}
func writeManagement(w http.ResponseWriter, p proto.Message, err error) {
	if err != nil {
		authz.WriteHTTPError(w, managementError(err))
		return
	}
	data, err := protojson.Marshal(p)
	if err != nil {
		authz.WriteHTTPError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
func (s *NotifyService) HandleNotificationManagement(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(r.URL.Path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 4 && parts[1] == "notifications" && parts[3] == "acknowledge" && r.Method == http.MethodPost {
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		req := &notifyv1.AcknowledgeNotificationRequest{}
		if readManagement(r, req) != nil || req.Id != 0 && req.Id != id {
			w.WriteHeader(400)
			return
		}
		req.Id = id
		reply, err := s.AcknowledgeNotification(r.Context(), req)
		writeManagement(w, reply, err)
		return
	}
	if len(parts) == 2 && parts[1] == "notification-rules" && r.Method == http.MethodGet {
		reply, err := s.ListNotificationRules(r.Context(), &notifyv1.ListNotificationRulesRequest{})
		writeManagement(w, reply, err)
		return
	}
	if len(parts) == 3 && parts[1] == "notification-rules" && r.Method == http.MethodPut {
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		req := &notifyv1.UpdateNotificationRuleRequest{}
		if readManagement(r, req) != nil || req.Rule == nil || req.Rule.Id != 0 && req.Rule.Id != id {
			w.WriteHeader(400)
			return
		}
		req.Rule.Id = id
		reply, err := s.UpdateNotificationRule(r.Context(), req)
		writeManagement(w, reply, err)
		return
	}
	if len(parts) == 4 && parts[1] == "notification-rules" && parts[3] == "test" && r.Method == http.MethodPost {
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		req := &notifyv1.TestNotificationRuleRequest{}
		if readManagement(r, req) != nil || req.Id != 0 && req.Id != id {
			w.WriteHeader(400)
			return
		}
		req.Id = id
		reply, err := s.TestNotificationRule(r.Context(), req)
		writeManagement(w, reply, err)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}
