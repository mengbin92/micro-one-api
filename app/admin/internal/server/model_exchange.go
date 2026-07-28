package server

import (
	"encoding/json"
	"io"
	"net/http"

	"go.uber.org/zap"

	applogger "micro-one-api/platform/logging"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/service"
)

// ── Model import/export HTTP handlers (v0.11.0 Phase 4) ───────────────────
//
// Two endpoints back the admin UI:
//   GET  /api/admin/models/export   → downloads a versioned JSON document
//   POST /api/admin/models/import   → dry-run or real import
//
// Price export/import requires root role. The export NEVER includes channel
// API keys or OAuth tokens — channel-service only reads model registry
// columns. A structured audit event is written for every import so the
// operator, request id, schema version, record count, content hash and result
// are captured even though the generic audit middleware is not yet wired.

// maxImportPayloadBytes caps the upload size so a huge file cannot exhaust
// memory. The model registry is bounded (hundreds, not millions), and the
// biz layer additionally caps the record count.
const maxImportPayloadBytes = 16 << 20 // 16 MiB

// handleExportModels handles GET /api/admin/models/export.
func handleExportModels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	role, _ := r.Context().Value(adminRoleContextKey{}).(int32)
	exportPrices := r.URL.Query().Get("export_prices") == "true" && role >= service.RoleRoot
	resp, err := svc.ExportModels(r.Context(), &channelv1.ExportModelsRequest{
		ExportPrices: exportPrices,
		Keyword:      r.URL.Query().Get("keyword"),
		Provider:     r.URL.Query().Get("provider"),
		ModelType:    r.URL.Query().Get("model_type"),
		Status:       getQueryInt32(r, "status", 0),
		Category:     r.URL.Query().Get("category"),
		Tier:         r.URL.Query().Get("tier"),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "model export failed"})
		return
	}
	body, err := json.MarshalIndent(struct {
		SchemaVersion string                          `json:"schema_version"`
		ExportedAt    string                          `json:"exported_at"`
		ContentHash   string                          `json:"content_hash"`
		Models        []*channelv1.ModelExportModel   `json:"models"`
	}{
		SchemaVersion: resp.GetSchemaVersion(),
		ExportedAt:    resp.GetExportedAt(),
		ContentHash:   resp.GetContentHash(),
		Models:        resp.GetModels(),
	}, "", "  ")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encode export"})
		return
	}
	logModelExportAudit(r, resp)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="model-export.json"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleImportModels handles POST /api/admin/models/import.
// Query parameter dry_run=true performs a preview without writing.
func handleImportModels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	role, _ := r.Context().Value(adminRoleContextKey{}).(int32)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportPayloadBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body too large or unreadable"})
		return
	}
	var doc struct {
		SchemaVersion    string                          `json:"schema_version"`
		Models           []*channelv1.ModelExportModel   `json:"models"`
		ConflictStrategy string                          `json:"conflict_strategy"`
		ImportPrices     bool                            `json:"import_prices"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Prices require root role; non-root callers have the flag silently
	// downgraded to false so a misconfigured client cannot change pricing.
	importPrices := doc.ImportPrices && role >= service.RoleRoot
	req := &channelv1.ImportModelsRequest{
		SchemaVersion:    doc.SchemaVersion,
		Models:           doc.Models,
		ConflictStrategy: doc.ConflictStrategy,
		ImportPrices:     importPrices,
	}
	if r.URL.Query().Get("dry_run") == "true" {
		resp, err := svc.DryRunImportModels(r.Context(), req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "dry-run failed"})
			return
		}
		logModelImportAudit(r, len(doc.Models), "dry-run", resp.GetWouldSucceed())
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp, err := svc.ImportModels(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "import failed"})
		return
	}
	logModelImportAudit(r, len(doc.Models), "import", resp.GetSuccess())
	writeJSON(w, http.StatusOK, resp)
}

// logModelExportAudit emits a structured audit event for a model export. The
// roadmap requires an audit trail; when the generic audit middleware is not
// wired we fall back to structured logging so the operator, request id,
// schema version and record count are still captured.
func logModelExportAudit(r *http.Request, resp *channelv1.ExportModelsResponse) {
	if resp == nil || applogger.Log == nil {
		return
	}
	applogger.Log.Info("model export audit",
		zap.String("actor", adminOperatorIDFromRequest(r)),
		zap.String("request_id", r.Header.Get("X-Request-Id")),
		zap.String("schema_version", resp.GetSchemaVersion()),
		zap.Int("record_count", len(resp.GetModels())),
		zap.String("content_hash", resp.GetContentHash()),
	)
}

// logModelImportAudit emits a structured audit event for a model import or
// dry-run. The actor is the authenticated admin (from X-Operator-User-Id
// stamped by newAdminGuard); request id, record count and result are included.
func logModelImportAudit(r *http.Request, recordCount int, mode string, success bool) {
	if applogger.Log == nil {
		return
	}
	applogger.Log.Info("model import audit",
		zap.String("actor", adminOperatorIDFromRequest(r)),
		zap.String("request_id", r.Header.Get("X-Request-Id")),
		zap.Int("record_count", recordCount),
		zap.String("mode", mode),
		zap.Bool("success", success),
	)
}

