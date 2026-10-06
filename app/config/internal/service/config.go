package service

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/authz"
	"net/http"
	"strconv"
	"strings"

	"micro-one-api/pkg/jsonx"

	configv1 "micro-one-api/api/config/v1"
	"micro-one-api/app/config/internal/biz"
)

// ConfigService is the transport layer entry for config-service.
type ConfigService struct {
	ownerAuthorization *authz.Client
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
		Revision:  entry.Revision,
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
			Revision:  e.Revision,
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
	ctx = authorization.WithExpectedResourceRevision(ctx, req.ExpectedRevision)
	if req.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, req.Reason)
	}
	entry, err := s.uc.SetConfigEntry(ctx, req.Namespace, req.Key, req.Value, req.Comment)
	if err != nil {
		return nil, configMutationError(err)
	}
	return &configv1.SetConfigResponse{Success: true, Revision: entry.Revision}, nil
}

func (s *ConfigService) DeleteConfig(ctx context.Context, req *configv1.DeleteConfigRequest) (*configv1.DeleteConfigResponse, error) {
	ctx = authorization.WithExpectedResourceRevision(ctx, req.ExpectedRevision)
	if req.Reason != "" {
		ctx = authorization.WithWriteReason(ctx, req.Reason)
	}
	if err := s.uc.DeleteConfig(ctx, req.Namespace, req.Key); err != nil {
		return nil, configMutationError(err)
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
		authz.WriteHTTPError(w, configMutationError(err))
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
		authz.WriteHTTPError(w, configMutationError(err))
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		items = append(items, configEntryToMap(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
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
		Value            string `json:"value"`
		Comment          string `json:"comment"`
		ExpectedRevision string `json:"expected_revision"`
		Reason           string `json:"reason"`
	}
	if err := jsonx.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx, err := configMutationContext(r.Context(), body.ExpectedRevision, body.Reason)
	if err != nil {
		authz.WriteHTTPError(w, err)
		return
	}
	entry, err := s.uc.SetConfigEntry(ctx, namespace, key, body.Value, body.Comment)
	if err != nil {
		authz.WriteHTTPError(w, configMutationError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "revision": strconv.FormatInt(entry.Revision, 10)})
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
	ctx, err := configMutationContext(r.Context(), r.URL.Query().Get("expected_revision"), r.URL.Query().Get("reason"))
	if err != nil {
		authz.WriteHTTPError(w, err)
		return
	}
	if err := s.uc.DeleteConfig(ctx, namespace, key); err != nil {
		if err == biz.ErrConfigNotFound {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		authz.WriteHTTPError(w, configMutationError(err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *ConfigService) HandleOneAPIContent(namespace, key, defaultValue string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{
				"success": false,
				"message": "method not allowed",
			})
			return
		}
		value := defaultValue
		entry, err := s.uc.PublicContent(r.Context(), key)
		if err == nil && entry != nil {
			value = entry.Value
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": "",
			"data":    value,
		})
	}
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

func configEntryToMap(e *biz.ConfigEntry) map[string]any {
	return map[string]any{
		"id":         e.ID,
		"revision":   strconv.FormatInt(e.Revision, 10),
		"namespace":  e.Namespace,
		"key":        e.Key,
		"value":      e.Value,
		"comment":    e.Comment,
		"updated_at": e.UpdatedAt,
	}
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonx.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = jsonx.NewEncoder(w).Encode(map[string]any{"error": message})
}

func (s *ConfigService) SetAuthorization(r authorization.Resolver) {
	s.uc.SetAuthorization(r)
	s.ownerAuthorization, _ = r.(*authz.Client)
}
func (s *ConfigService) OwnerAuthorizationClient() *authz.Client { return s.ownerAuthorization }

func configMutationContext(ctx context.Context, expected, reason string) (context.Context, error) {
	if expected == "" {
		expected = "0"
	}
	revision, err := strconv.ParseUint(expected, 10, 64)
	if err != nil {
		return ctx, status.Error(codes.InvalidArgument, "invalid expected_revision")
	}
	ctx = authorization.WithExpectedResourceRevision(ctx, revision)
	if reason != "" {
		ctx = authorization.WithWriteReason(ctx, reason)
	}
	return ctx, nil
}
func configMutationError(err error) error {
	switch {
	case errors.Is(err, biz.ErrConfigRevisionConflict):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, biz.ErrConfigMutationRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	}
	return err
}
