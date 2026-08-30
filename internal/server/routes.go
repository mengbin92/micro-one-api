package server

import (
	"net/http"
	"slices"

	"micro-one-api/platform/metrics"
	appmiddleware "micro-one-api/platform/middleware"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// RegisterRoutes registers HTTP routes to a Kratos *khttp.Server.
func (s *HTTPServer) RegisterRoutes(srv *khttp.Server) {
	// Keep the gate wrapper installed even when the feature is disabled so
	// legacy traffic remains observable without changing its behavior.
	s.handleFunc(srv, "/v1/chat/completions", s.relayOrchestratorChatHandler)
	s.handleFunc(srv, "/v1/completions", s.handleRawRelay("/completions", true))
	s.handleFunc(srv, "/v1/embeddings", s.handleRawRelay("/embeddings", false))
	s.handleFunc(srv, "/v1/images/generations", s.handleRawRelay("/images/generations", true))
	s.handleFunc(srv, "/v1/images/edits", s.handleUnsupportedOpenAIRoute("images.edits"))
	s.handleFunc(srv, "/v1/images/variations", s.handleUnsupportedOpenAIRoute("images.variations"))
	s.handleFunc(srv, "/v1/audio/transcriptions", s.handleRawRelay("/audio/transcriptions", true))
	s.handleFunc(srv, "/v1/audio/translations", s.handleRawRelay("/audio/translations", true))
	s.handleFunc(srv, "/v1/audio/speech", s.handleRawRelay("/audio/speech", false))
	s.handleFunc(srv, "/v1/moderations", s.handleRawRelay("/moderations", false))
	s.handleFunc(srv, "/v1/edits", s.handleUnsupportedOpenAIRoute("edits"))
	s.handleFunc(srv, "/v1/responses", s.relayOrchestratorResponsesHandler)
	s.handlePrefix(srv, "/v1/responses/", http.HandlerFunc(s.relayOrchestratorResponsesHandler))
	s.handleFunc(srv, "/v1/usage", s.handleUsage)
	s.handleFunc(srv, "/v1/subscription/usage", s.handleSubscriptionUsage)
	s.handleFunc(srv, "/v1/engines", s.handleUnsupportedOpenAIRoute("engines"))
	s.handlePrefix(srv, "/v1/engines/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("engines")))
	s.handleFunc(srv, "/v1/files", s.handleUnsupportedOpenAIRoute("files"))
	s.handlePrefix(srv, "/v1/files/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("files")))
	s.handleFunc(srv, "/v1/fine-tunes", s.handleUnsupportedOpenAIRoute("fine-tunes"))
	s.handlePrefix(srv, "/v1/fine-tunes/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine-tunes")))
	s.handleFunc(srv, "/v1/fine_tuning/jobs", s.handleUnsupportedOpenAIRoute("fine_tuning.jobs"))
	s.handlePrefix(srv, "/v1/fine_tuning/jobs/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine_tuning.jobs")))
	s.handleFunc(srv, "/v1/batches", s.handleUnsupportedOpenAIRoute("batches"))
	s.handlePrefix(srv, "/v1/batches/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("batches")))
	s.handleFunc(srv, "/v1/uploads", s.handleUnsupportedOpenAIRoute("uploads"))
	s.handlePrefix(srv, "/v1/uploads/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("uploads")))
	s.handleFunc(srv, "/v1/vector_stores", s.handleUnsupportedOpenAIRoute("vector_stores"))
	s.handlePrefix(srv, "/v1/vector_stores/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("vector_stores")))
	s.handleFunc(srv, "/v1/evals", s.handleUnsupportedOpenAIRoute("evals"))
	s.handlePrefix(srv, "/v1/evals/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("evals")))
	s.handleFunc(srv, "/v1/containers", s.handleUnsupportedOpenAIRoute("containers"))
	s.handlePrefix(srv, "/v1/containers/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("containers")))
	s.handlePrefix(srv, "/v1/fine_tuning/alpha/graders/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("graders")))
	s.handlePrefix(srv, "/v1/realtime/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("realtime")))
	s.handleFunc(srv, "/v1/conversations", s.handleUnsupportedOpenAIRoute("conversations"))
	s.handlePrefix(srv, "/v1/conversations/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("conversations")))
	s.handleFunc(srv, "/v1/assistants", s.handleUnsupportedOpenAIRoute("assistants"))
	s.handlePrefix(srv, "/v1/assistants/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("assistants")))
	s.handleFunc(srv, "/v1/threads", s.handleUnsupportedOpenAIRoute("threads"))
	s.handlePrefix(srv, "/v1/threads/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("threads")))
	s.handlePrefix(srv, "/v1/oneapi/proxy/", http.HandlerFunc(s.handleOneAPIProxy))

	// Anthropic Messages API inbound endpoint (for Claude Code CLI / native Anthropic SDK clients)
	s.handleFunc(srv, "/v1/messages", s.relayOrchestratorMessagesHandler)
	s.handleFunc(srv, "/v1/models", s.handleModels)
	s.handlePrefix(srv, "/v1/models/", http.HandlerFunc(s.handleRetrieveModel))
	s.handleFunc(srv, "/api/status", s.handleAPIStatus)
	s.handleFunc(srv, "/api/models", s.handleDashboardModels)
	s.handleFunc(srv, "/api/group", s.handleGroups)
	srv.HandleFunc("/healthz", s.handleHealth)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
}

func (s *HTTPServer) handleFunc(srv *khttp.Server, pattern string, handler http.HandlerFunc) {
	srv.Handle(pattern, s.wrapRoute(handler))
}

func (s *HTTPServer) wrapRoute(handler http.Handler) http.Handler {
	var h http.Handler = handler
	h = appmiddleware.RequestBodyLimitByPath(h)
	for _, v := range slices.Backward(s.routeMiddleware) {
		h = v(h)
	}
	return h
}

func (s *HTTPServer) handlePrefix(srv *khttp.Server, pattern string, handler http.Handler) {
	srv.HandlePrefix(pattern, s.wrapRoute(handler))
}
