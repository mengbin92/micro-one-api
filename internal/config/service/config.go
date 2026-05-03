package service

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"

	configv1 "micro-one-api/api/config/v1"
	"micro-one-api/internal/config/biz"
)

// ConfigService is the transport layer entry for config-service.
type ConfigService struct {
	configv1.UnimplementedConfigServiceServer
	uc *biz.ConfigUsecase
}

func NewConfigService(uc *biz.ConfigUsecase) *ConfigService {
	return &ConfigService{uc: uc}
}

// gRPC interface implementation

func (s *ConfigService) GetConfig(ctx context.Context, req *configv1.GetConfigRequest) (*configv1.GetConfigResponse, error) {
	entry, err := s.uc.GetConfig(ctx, req.Namespace, req.Key)
	if err != nil {
		return nil, err
	}
	return &configv1.GetConfigResponse{
		Id:        entry.ID,
		Namespace: entry.Namespace,
		Key:       entry.Key,
		Value:     entry.Value,
		Comment:   entry.Comment,
		UpdatedAt: entry.UpdatedAt.Unix(),
	}, nil
}

func (s *ConfigService) ListConfigs(ctx context.Context, req *configv1.ListConfigsRequest) (*configv1.ListConfigsResponse, error) {
	entries, total, err := s.uc.ListConfigs(ctx, req.Namespace, req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	items := make([]*configv1.GetConfigResponse, len(entries))
	for i, e := range entries {
		items[i] = &configv1.GetConfigResponse{
			Id:        e.ID,
			Namespace: e.Namespace,
			Key:       e.Key,
			Value:     e.Value,
			Comment:   e.Comment,
			UpdatedAt: e.UpdatedAt.Unix(),
		}
	}
	return &configv1.ListConfigsResponse{Items: items, Total: total}, nil
}

func (s *ConfigService) SetConfig(ctx context.Context, req *configv1.SetConfigRequest) (*configv1.SetConfigResponse, error) {
	if err := s.uc.SetConfig(ctx, req.Namespace, req.Key, req.Value, req.Comment); err != nil {
		return nil, err
	}
	return &configv1.SetConfigResponse{Success: true}, nil
}

func (s *ConfigService) DeleteConfig(ctx context.Context, req *configv1.DeleteConfigRequest) (*configv1.DeleteConfigResponse, error) {
	if err := s.uc.DeleteConfig(ctx, req.Namespace, req.Key); err != nil {
		return nil, err
	}
	return &configv1.DeleteConfigResponse{Success: true}, nil
}

// HTTP handler implementations

func (s *ConfigService) HandleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	namespace, key := parseTwoSegments(r.URL.Path, "/v1/configs/")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "namespace and key are required")
		return
	}
	entry, err := s.uc.GetConfig(r.Context(), namespace, key)
	if err != nil {
		if err == biz.ErrConfigNotFound {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, configEntryToMap(entry))
}

func (s *ConfigService) HandleListConfigs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	namespace := strings.TrimPrefix(r.URL.Path, "/v1/configs/")
	namespace = strings.TrimRight(namespace, "/")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace is required")
		return
	}
	page, _ := strconv.ParseInt(r.URL.Query().Get("page"), 10, 32)
	pageSize, _ := strconv.ParseInt(r.URL.Query().Get("page_size"), 10, 32)
	entries, total, err := s.uc.ListConfigs(r.Context(), namespace, int32(page), int32(pageSize))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		items = append(items, configEntryToMap(e))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items, "total": total})
}

func (s *ConfigService) HandleSetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	namespace, key := parseTwoSegments(r.URL.Path, "/v1/configs/")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "namespace and key are required")
		return
	}
	var body struct {
		Value   string `json:"value"`
		Comment string `json:"comment"`
	}
	if err := sonic.ConfigStd.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.uc.SetConfig(r.Context(), namespace, key, body.Value, body.Comment); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *ConfigService) HandleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	namespace, key := parseTwoSegments(r.URL.Path, "/v1/configs/")
	if namespace == "" || key == "" {
		writeError(w, http.StatusBadRequest, "namespace and key are required")
		return
	}
	if err := s.uc.DeleteConfig(r.Context(), namespace, key); err != nil {
		if err == biz.ErrConfigNotFound {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// helpers

func parseTwoSegments(path, prefix string) (string, string) {
	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimRight(rest, "/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		return parts[0], ""
	}
	return parts[0], parts[1]
}

func configEntryToMap(e *biz.ConfigEntry) map[string]interface{} {
	return map[string]interface{}{
		"id":         e.ID,
		"namespace":  e.Namespace,
		"key":        e.Key,
		"value":      e.Value,
		"comment":    e.Comment,
		"updated_at": e.UpdatedAt,
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
