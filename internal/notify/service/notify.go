package service

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"

	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/internal/notify/biz"
)

// NotifyService is the transport layer entry for notify-worker.
type NotifyService struct {
	notifyv1.UnimplementedNotifyServiceServer
	uc *biz.NotifyUsecase
}

func NewNotifyService(uc *biz.NotifyUsecase) *NotifyService {
	return &NotifyService{uc: uc}
}

// gRPC interface implementation

func (s *NotifyService) CreateNotification(ctx context.Context, req *notifyv1.CreateNotificationRequest) (*notifyv1.CreateNotificationResponse, error) {
	n, err := s.uc.CreateNotification(ctx, req.Type, req.Recipient, req.Subject, req.Content)
	if err != nil {
		return nil, err
	}
	return &notifyv1.CreateNotificationResponse{
		Notification: notificationToProto(n),
	}, nil
}

func (s *NotifyService) GetNotification(ctx context.Context, req *notifyv1.GetNotificationRequest) (*notifyv1.GetNotificationResponse, error) {
	n, err := s.uc.GetNotification(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return &notifyv1.GetNotificationResponse{
		Notification: notificationToProto(n),
	}, nil
}

func (s *NotifyService) ListNotifications(ctx context.Context, req *notifyv1.ListNotificationsRequest) (*notifyv1.ListNotificationsResponse, error) {
	notifications, total, err := s.uc.ListNotifications(ctx, req.Page, req.PageSize, req.Type, req.Status)
	if err != nil {
		return nil, err
	}
	items := make([]*notifyv1.NotificationItem, len(notifications))
	for i, n := range notifications {
		items[i] = notificationToProto(n)
	}
	return &notifyv1.ListNotificationsResponse{Items: items, Total: total}, nil
}

func (s *NotifyService) UpdateNotificationStatus(ctx context.Context, req *notifyv1.UpdateNotificationStatusRequest) (*notifyv1.UpdateNotificationStatusResponse, error) {
	var err error
	switch req.Status {
	case biz.NotifyStatusSent:
		err = s.uc.MarkSent(ctx, req.Id)
	case biz.NotifyStatusFailed:
		err = s.uc.MarkFailed(ctx, req.Id)
	default:
		err = s.uc.MarkSent(ctx, req.Id)
	}
	if err != nil {
		return nil, err
	}
	return &notifyv1.UpdateNotificationStatusResponse{Success: true}, nil
}

func notificationToProto(n *biz.Notification) *notifyv1.NotificationItem {
	return &notifyv1.NotificationItem{
		Id:         n.ID,
		Type:       n.Type,
		Recipient:  n.Recipient,
		Subject:    n.Subject,
		Content:    n.Content,
		Status:     n.Status,
		RetryCount: int32(n.RetryCount),
		CreatedAt:  n.CreatedAt.Unix(),
		SentAt:     n.SentAt.Unix(),
	}
}

// HTTP handler implementations

func (s *NotifyService) HandleCreateNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Type      string `json:"type"`
		Recipient string `json:"recipient"`
		Subject   string `json:"subject"`
		Content   string `json:"content"`
	}
	if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	n, err := s.uc.CreateNotification(r.Context(), body.Type, body.Recipient, body.Subject, body.Content)
	if err != nil {
		if err == biz.ErrInvalidNotification {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, notificationToMap(n))
}

func (s *NotifyService) HandleGetNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/v1/notifications/")
	idStr = strings.TrimRight(idStr, "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid notification id")
		return
	}
	n, err := s.uc.GetNotification(r.Context(), id)
	if err != nil {
		if err == biz.ErrNotificationNotFound {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, notificationToMap(n))
}

func (s *NotifyService) HandleListNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.ParseInt(q.Get("page"), 10, 32)
	pageSize, _ := strconv.ParseInt(q.Get("page_size"), 10, 32)
	notifyType := q.Get("type")
	status := q.Get("status")
	notifications, total, err := s.uc.ListNotifications(r.Context(), int32(page), int32(pageSize), notifyType, status)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]interface{}, 0, len(notifications))
	for _, n := range notifications {
		items = append(items, notificationToMap(n))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": total})
}

func notificationToMap(n *biz.Notification) map[string]interface{} {
	return map[string]interface{}{
		"id": n.ID, "type": n.Type, "recipient": n.Recipient, "subject": n.Subject,
		"content": n.Content, "status": n.Status, "retry_count": n.RetryCount,
		"created_at": n.CreatedAt, "sent_at": n.SentAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	sonic.ConfigStd.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	sonic.ConfigStd.NewEncoder(w).Encode(map[string]interface{}{"error": message})
}
