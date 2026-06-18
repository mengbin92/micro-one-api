package service

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"google.golang.org/grpc"

	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/internal/log/biz"
	applogger "micro-one-api/internal/pkg/logger"
)

// LogService is the transport layer entry for log-service.
type LogService struct {
	logv1.UnimplementedLogServiceServer
	uc *biz.LogUsecase
}

func NewLogService(uc *biz.LogUsecase) *LogService {
	return &LogService{uc: uc}
}

// gRPC interface implementation

func (s *LogService) GetLog(ctx context.Context, req *logv1.GetLogRequest) (*logv1.GetLogResponse, error) {
	entry, err := s.uc.GetLog(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	return &logv1.GetLogResponse{
		Id:               entry.ID,
		Level:            entry.Level,
		Message:          entry.Message,
		Source:           entry.Source,
		RequestId:        entry.RequestID,
		UserId:           entry.UserID,
		CreatedAt:        entry.CreatedAt.Unix(),
		Username:         entry.Username,
		TokenName:        entry.TokenName,
		ModelName:        entry.ModelName,
		Quota:            entry.Quota,
		PromptTokens:     entry.PromptTokens,
		CompletionTokens: entry.CompletionTokens,
		CacheReadTokens:  entry.CacheReadTokens,
		ChannelId:        entry.ChannelID,
		ElapsedTime:      entry.ElapsedTime,
		IsStream:         entry.IsStream,
	}, nil
}

func (s *LogService) ListLogs(ctx context.Context, req *logv1.ListLogsRequest) (*logv1.ListLogsResponse, error) {
	entries, total, err := s.uc.ListLogs(ctx, req.Page, req.PageSize, req.Type, "", "")
	if err != nil {
		return nil, err
	}
	items := make([]*logv1.GetLogResponse, len(entries))
	for i, e := range entries {
		items[i] = logEntryToProto(e)
	}
	return &logv1.ListLogsResponse{Items: items, Total: total}, nil
}

func (s *LogService) IngestLog(ctx context.Context, req *logv1.IngestLogRequest) (*logv1.IngestLogResponse, error) {
	entry := &biz.LogEntry{
		Level:            req.Level,
		Message:          applogger.Sanitize(req.Message),
		Source:           req.Source,
		RequestID:        req.RequestId,
		UserID:           req.UserId,
		Username:         req.Username,
		TokenName:        req.TokenName,
		ModelName:        req.ModelName,
		Quota:            req.Quota,
		PromptTokens:     req.PromptTokens,
		CompletionTokens: req.CompletionTokens,
		CacheReadTokens:  req.CacheReadTokens,
		ChannelID:        req.ChannelId,
		ElapsedTime:      req.ElapsedTime,
		IsStream:         req.IsStream,
	}
	if err := s.uc.IngestLog(ctx, entry); err != nil {
		return nil, err
	}
	return &logv1.IngestLogResponse{Id: entry.ID}, nil
}

func logEntryToProto(e *biz.LogEntry) *logv1.GetLogResponse {
	return &logv1.GetLogResponse{
		Id:               e.ID,
		Level:            e.Level,
		Message:          e.Message,
		Source:           e.Source,
		RequestId:        e.RequestID,
		UserId:           e.UserID,
		CreatedAt:        e.CreatedAt.Unix(),
		Username:         e.Username,
		TokenName:        e.TokenName,
		ModelName:        e.ModelName,
		Quota:            e.Quota,
		PromptTokens:     e.PromptTokens,
		CompletionTokens: e.CompletionTokens,
		CacheReadTokens:  e.CacheReadTokens,
		ChannelId:        e.ChannelID,
		ElapsedTime:      e.ElapsedTime,
		IsStream:         e.IsStream,
	}
}

// HTTP handler implementations

func (s *LogService) HandleGetLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/v1/logs/")
	idStr = strings.TrimRight(idStr, "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid log id")
		return
	}
	entry, err := s.uc.GetLog(r.Context(), id)
	if err != nil {
		if err == biz.ErrLogNotFound {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, logEntryToMap(entry))
}

func (s *LogService) HandleListLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()
	page, _ := strconv.ParseInt(q.Get("page"), 10, 32)
	pageSize, _ := strconv.ParseInt(q.Get("page_size"), 10, 32)
	level := q.Get("type")
	entries, total, err := s.uc.ListLogs(r.Context(), int32(page), int32(pageSize), level, "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		items = append(items, logEntryToMap(e))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": total})
}

func (s *LogService) HandleIngestLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Level     string `json:"level"`
		Message   string `json:"message"`
		Source    string `json:"source"`
		RequestID string `json:"request_id"`
		UserID    int64  `json:"user_id"`
	}
	if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	entry := &biz.LogEntry{
		Level:     body.Level,
		Message:   applogger.Sanitize(body.Message),
		Source:    body.Source,
		RequestID: body.RequestID,
		UserID:    body.UserID,
	}
	if err := s.uc.IngestLog(r.Context(), entry); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, logEntryToMap(entry))
}

func (s *LogService) HandleDeleteLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q := r.URL.Query()
	endTime, err := parseUnixQuery(q.Get("end_time"))
	if err != nil || endTime.IsZero() {
		writeError(w, http.StatusBadRequest, "end_time is required")
		return
	}
	startTime, err := parseUnixQuery(q.Get("start_time"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid start_time")
		return
	}
	var userID int64
	if raw := q.Get("user_id"); raw != "" {
		userID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || userID < 0 {
			writeError(w, http.StatusBadRequest, "invalid user_id")
			return
		}
	}
	deleted, err := s.uc.DeleteLogs(r.Context(), biz.DeleteLogsFilter{
		Level:     q.Get("type"),
		Source:    q.Get("source"),
		UserID:    userID,
		StartTime: startTime,
		EndTime:   endTime,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": deleted})
}

func parseUnixQuery(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return time.Time{}, err
	}
	return time.Unix(value, 0), nil
}

func (s *LogService) HandleOneAPIUserLogs(w http.ResponseWriter, r *http.Request, identityClient identityv1.IdentityServiceClient) {
	if r.Method != http.MethodGet {
		writeOneAPI(w, http.StatusMethodNotAllowed, false, "method not allowed", nil)
		return
	}
	userID, ok := authUserIDFromRequest(w, r, identityClient)
	if !ok {
		return
	}
	page, pageSize := oneAPIPage(r)
	entries, _, err := s.uc.ListUserLogs(r.Context(), userID, page, pageSize, r.URL.Query().Get("type"), "")
	if err != nil {
		writeOneAPI(w, http.StatusOK, false, err.Error(), nil)
		return
	}
	writeOneAPI(w, http.StatusOK, true, "", logEntriesToMaps(entries))
}

func (s *LogService) HandleOneAPIUserLogSearch(w http.ResponseWriter, r *http.Request, identityClient identityv1.IdentityServiceClient) {
	if r.Method != http.MethodGet {
		writeOneAPI(w, http.StatusMethodNotAllowed, false, "method not allowed", nil)
		return
	}
	userID, ok := authUserIDFromRequest(w, r, identityClient)
	if !ok {
		return
	}
	page, pageSize := oneAPIPage(r)
	entries, _, err := s.uc.ListUserLogs(r.Context(), userID, page, pageSize, r.URL.Query().Get("type"), r.URL.Query().Get("keyword"))
	if err != nil {
		writeOneAPI(w, http.StatusOK, false, err.Error(), nil)
		return
	}
	writeOneAPI(w, http.StatusOK, true, "", logEntriesToMaps(entries))
}

func (s *LogService) HandleOneAPIUserLogStats(w http.ResponseWriter, r *http.Request, identityClient identityv1.IdentityServiceClient) {
	if r.Method != http.MethodGet {
		writeOneAPI(w, http.StatusMethodNotAllowed, false, "method not allowed", nil)
		return
	}
	userID, ok := authUserIDFromRequest(w, r, identityClient)
	if !ok {
		return
	}
	entries, total, err := s.uc.ListUserLogs(r.Context(), userID, 1, 1000, r.URL.Query().Get("type"), "")
	if err != nil {
		writeOneAPI(w, http.StatusOK, false, err.Error(), nil)
		return
	}
	countByType := map[string]int64{}
	for _, entry := range entries {
		countByType[entry.Level]++
	}
	usage, err := s.uc.UserUsageStats(r.Context(), userID, time.Time{}, time.Time{})
	if err != nil {
		writeOneAPI(w, http.StatusOK, false, err.Error(), nil)
		return
	}
	writeOneAPI(w, http.StatusOK, true, "", map[string]interface{}{
		"total":         total,
		"sampled_count": len(entries),
		"count_by_type": countByType,
		"usage":         usage,
	})
}

func logEntryToMap(e *biz.LogEntry) map[string]interface{} {
	return map[string]interface{}{
		"id":                e.ID,
		"level":             e.Level,
		"type":              e.Level,
		"message":           e.Message,
		"source":            e.Source,
		"request_id":        e.RequestID,
		"user_id":           e.UserID,
		"created_at":        e.CreatedAt.Unix(),
		"username":          e.Username,
		"token_name":        e.TokenName,
		"model_name":        e.ModelName,
		"quota":             e.Quota,
		"prompt_tokens":     e.PromptTokens,
		"completion_tokens": e.CompletionTokens,
		"cache_read_tokens": e.CacheReadTokens,
		"channel":           e.ChannelID,
		"elapsed_time":      e.ElapsedTime,
		"is_stream":         e.IsStream,
	}
}

func logEntriesToMaps(entries []*biz.LogEntry) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(entries))
	for _, entry := range entries {
		items = append(items, logEntryToMap(entry))
	}
	return items
}

func authUserIDFromRequest(w http.ResponseWriter, r *http.Request, identityClient identityv1.IdentityServiceClient) (int64, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		writeOneAPI(w, http.StatusUnauthorized, false, "unauthorized", nil)
		return 0, false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		writeOneAPI(w, http.StatusUnauthorized, false, "unauthorized", nil)
		return 0, false
	}
	if identityClient == nil {
		writeOneAPI(w, http.StatusServiceUnavailable, false, "identity service unavailable", nil)
		return 0, false
	}
	resp, err := identityClient.GetAuthSnapshot(r.Context(), &identityv1.GetAuthSnapshotRequest{Token: token}, grpc.WaitForReady(false))
	if err != nil || resp.GetUserId() == 0 || !resp.GetUserEnabled() || !resp.GetTokenEnabled() {
		writeOneAPI(w, http.StatusUnauthorized, false, "unauthorized", nil)
		return 0, false
	}
	return resp.GetUserId(), true
}

func oneAPIPage(r *http.Request) (int32, int32) {
	page := int32(1)
	if pRaw := r.URL.Query().Get("p"); pRaw != "" {
		if p, err := strconv.ParseInt(pRaw, 10, 32); err == nil && p >= 0 {
			page = int32(p) + 1
		}
	} else if pageRaw := r.URL.Query().Get("page"); pageRaw != "" {
		if p, err := strconv.ParseInt(pageRaw, 10, 32); err == nil && p > 0 {
			page = int32(p)
		}
	}
	pageSize := int32(20)
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 32); err == nil && n > 0 {
			pageSize = int32(n)
		}
	}
	return page, pageSize
}

func writeOneAPI(w http.ResponseWriter, status int, success bool, message string, data interface{}) {
	resp := map[string]interface{}{
		"success": success,
		"message": message,
	}
	if data != nil {
		resp["data"] = data
	}
	writeJSON(w, status, resp)
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
