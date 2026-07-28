package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"

	applogger "micro-one-api/platform/logging"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/service"
	appmiddleware "micro-one-api/platform/middleware"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	exportPrices := r.URL.Query().Get("export_prices") == "true"
	if exportPrices && role < service.RoleRoot {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "root role is required to export prices"})
		return
	}
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
		SchemaVersion string                        `json:"schema_version"`
		ExportedAt    string                        `json:"exported_at"`
		ContentHash   string                        `json:"content_hash"`
		Models        []*channelv1.ModelExportModel `json:"models"`
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
//
// A SHA-256 content hash of the raw upload body is computed so the audit
// record can identify the exact document even when the import fails before
// classification (code review #9). The audit runs via defer so BOTH success
// and error paths leave a trace — a failed/rolled-back import is recorded
// with success=false and the error category.
func handleImportModels(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	role, _ := r.Context().Value(adminRoleContextKey{}).(int32)
	// Set up the audit BEFORE reading the body so even a payload-too-large
	// or body-read failure leaves a trace (code review LOW-1).
	audit := importAuditInfo{
		actor:         modelExchangeAuditActor(r),
		requestID:     modelExchangeAuditRequestID(r),
		contentHash:   "",
		schemaVersion: "",
		recordCount:   0,
		mode:          "import",
		success:       false,
		errorCat:      "",
	}
	if r.URL.Query().Get("dry_run") == "true" {
		audit.mode = "dry-run"
	}
	defer func() {
		logModelImportAuditDeferred(audit)
	}()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportPayloadBytes))
	if err != nil {
		audit.errorCat = "body_read_failed"
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body too large or unreadable"})
		return
	}
	// Compute content hash of the raw upload so the audit record is traceable
	// to the exact document (code review #9).
	audit.contentHash = sha256Hex(body)

	var doc struct {
		SchemaVersion    string                        `json:"schema_version"`
		Models           []*channelv1.ModelExportModel `json:"models"`
		ConflictStrategy string                        `json:"conflict_strategy"`
		ImportPrices     bool                          `json:"import_prices"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		audit.errorCat = "invalid_json"
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	audit.schemaVersion = doc.SchemaVersion
	audit.recordCount = len(doc.Models)

	if doc.ImportPrices && role < service.RoleRoot {
		audit.errorCat = "forbidden_prices"
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "root role is required to import prices"})
		return
	}
	req := &channelv1.ImportModelsRequest{
		SchemaVersion:    doc.SchemaVersion,
		Models:           doc.Models,
		ConflictStrategy: doc.ConflictStrategy,
		ImportPrices:     doc.ImportPrices,
	}
	if r.URL.Query().Get("dry_run") == "true" {
		resp, err := svc.DryRunImportModels(r.Context(), req)
		if err != nil {
			audit.errorCat = classifyImportError(err)
			writeModelImportError(w, err, "dry-run failed")
			return
		}
		audit.success = resp.GetWouldSucceed()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp, err := svc.ImportModels(r.Context(), req)
	if err != nil {
		audit.errorCat = classifyImportError(err)
		writeModelImportError(w, err, "import failed")
		return
	}
	audit.success = resp.GetSuccess()
	writeJSON(w, http.StatusOK, resp)
}

func writeModelImportError(w http.ResponseWriter, err error, fallback string) {
	code := http.StatusInternalServerError
	switch status.Code(err) {
	case codes.InvalidArgument:
		code = http.StatusBadRequest
	case codes.AlreadyExists, codes.Aborted:
		code = http.StatusConflict
	case codes.PermissionDenied:
		code = http.StatusForbidden
	}
	writeJSON(w, code, map[string]string{"error": fallback})
}

func modelExchangeAuditActor(r *http.Request) string {
	if actor := strings.TrimSpace(adminOperatorIDFromRequest(r)); actor != "" {
		return actor
	}
	return "unknown-authenticated-operator"
}

func modelExchangeAuditRequestID(r *http.Request) string {
	if r != nil {
		if requestID := strings.TrimSpace(appmiddleware.GetRequestID(r.Context())); requestID != "" {
			return requestID
		}
	}
	return "missing-request-id"
}

// importAuditInfo carries all fields the import audit log needs. It is filled
// progressively as the handler runs and emitted once via defer (code review
// #9).
type importAuditInfo struct {
	actor         string
	requestID     string
	contentHash   string
	schemaVersion string
	recordCount   int
	mode          string
	success       bool
	errorCat      string
}

// sha256Hex returns the hex-encoded SHA-256 of the input.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// classifyImportError maps an import error to a low-cardinality category for
// the audit record so failed imports are traceable without leaking internal
// details.
func classifyImportError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch status.Code(err) {
	case codes.AlreadyExists, codes.Aborted:
		return "conflict"
	case codes.InvalidArgument:
		return "invalid_record"
	case codes.PermissionDenied:
		return "forbidden"
	}
	switch {
	case contains(msg, "conflict"), contains(msg, "ErrImportConflict"):
		return "conflict"
	case contains(msg, "invalid"), contains(msg, "ErrImportInvalidRecord"):
		return "invalid_record"
	case contains(msg, "foreign key"), contains(msg, "does not exist"):
		return "dangling_fk"
	default:
		return "internal_error"
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
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
		zap.String("actor", modelExchangeAuditActor(r)),
		zap.String("request_id", modelExchangeAuditRequestID(r)),
		zap.String("schema_version", resp.GetSchemaVersion()),
		zap.Int("record_count", len(resp.GetModels())),
		zap.String("content_hash", resp.GetContentHash()),
	)
}

// logModelImportAuditDeferred emits a structured audit event for a model import
// or dry-run. It includes the content hash and schema version (code review #9)
// and runs on every exit path (success AND error) so failed/rolled-back
// imports are always traceable. The error category is included when the import
// failed before producing a summary.
func logModelImportAuditDeferred(info importAuditInfo) {
	if applogger.Log == nil {
		return
	}
	fields := []zap.Field{
		zap.String("actor", info.actor),
		zap.String("request_id", info.requestID),
		zap.String("content_hash", info.contentHash),
		zap.String("schema_version", info.schemaVersion),
		zap.Int("record_count", info.recordCount),
		zap.String("mode", info.mode),
		zap.Bool("success", info.success),
	}
	if info.errorCat != "" {
		fields = append(fields, zap.String("error_category", info.errorCat))
	}
	applogger.Log.Info("model import audit", fields...)
}
