package server

import (
	"net/http"

	"micro-one-api/internal/pkg/metrics"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// RegisterRoutes registers HTTP routes to a Kratos *khttp.Server.
func (s *HTTPServer) RegisterRoutes(srv *khttp.Server) {
	chatHandler := s.handleChatCompletions
	if s.relayOrchestratorEnabled {
		chatHandler = s.handleChatCompletionsWithOrchestrator
	}
	s.handleFunc(srv, "/v1/chat/completions", chatHandler)
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
	s.handleFunc(srv, "/v1/responses", s.handleResponsesRelay)
	srv.HandlePrefix("/v1/responses/", http.HandlerFunc(s.handleResponsesRelay))
	s.handleFunc(srv, "/v1/usage", s.handleUsage)
	srv.HandleFunc("/v1/engines", s.handleUnsupportedOpenAIRoute("engines"))
	srv.HandlePrefix("/v1/engines/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("engines")))
	srv.HandleFunc("/v1/files", s.handleUnsupportedOpenAIRoute("files"))
	srv.HandlePrefix("/v1/files/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("files")))
	srv.HandleFunc("/v1/fine-tunes", s.handleUnsupportedOpenAIRoute("fine-tunes"))
	srv.HandlePrefix("/v1/fine-tunes/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine-tunes")))
	srv.HandleFunc("/v1/fine_tuning/jobs", s.handleUnsupportedOpenAIRoute("fine_tuning.jobs"))
	srv.HandlePrefix("/v1/fine_tuning/jobs/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine_tuning.jobs")))
	srv.HandleFunc("/v1/batches", s.handleUnsupportedOpenAIRoute("batches"))
	srv.HandlePrefix("/v1/batches/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("batches")))
	srv.HandleFunc("/v1/uploads", s.handleUnsupportedOpenAIRoute("uploads"))
	srv.HandlePrefix("/v1/uploads/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("uploads")))
	srv.HandleFunc("/v1/vector_stores", s.handleUnsupportedOpenAIRoute("vector_stores"))
	srv.HandlePrefix("/v1/vector_stores/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("vector_stores")))
	srv.HandleFunc("/v1/evals", s.handleUnsupportedOpenAIRoute("evals"))
	srv.HandlePrefix("/v1/evals/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("evals")))
	srv.HandleFunc("/v1/containers", s.handleUnsupportedOpenAIRoute("containers"))
	srv.HandlePrefix("/v1/containers/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("containers")))
	srv.HandlePrefix("/v1/fine_tuning/alpha/graders/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("graders")))
	srv.HandlePrefix("/v1/realtime/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("realtime")))
	srv.HandleFunc("/v1/conversations", s.handleUnsupportedOpenAIRoute("conversations"))
	srv.HandlePrefix("/v1/conversations/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("conversations")))
	srv.HandleFunc("/v1/assistants", s.handleUnsupportedOpenAIRoute("assistants"))
	srv.HandlePrefix("/v1/assistants/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("assistants")))
	srv.HandleFunc("/v1/threads", s.handleUnsupportedOpenAIRoute("threads"))
	srv.HandlePrefix("/v1/threads/", http.HandlerFunc(s.handleUnsupportedOpenAIRoute("threads")))
	srv.HandlePrefix("/v1/oneapi/proxy/", http.HandlerFunc(s.handleOneAPIProxy))

	// Anthropic Messages API inbound endpoint (for Claude Code CLI / native Anthropic SDK clients)
	s.handleFunc(srv, "/v1/messages", s.handleAnthropicMessages)
	s.handleFunc(srv, "/v1/models", s.handleModels)
	srv.HandlePrefix("/v1/models/", http.HandlerFunc(s.handleRetrieveModel))
	srv.HandleFunc("/api/status", s.handleAPIStatus)
	srv.HandleFunc("/api/models", s.handleDashboardModels)
	srv.HandleFunc("/api/group", s.handleGroups)
	srv.HandleFunc("/healthz", s.handleHealth)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
}

func (s *HTTPServer) handleFunc(srv *khttp.Server, pattern string, handler http.HandlerFunc) {
	if len(s.routeMiddleware) == 0 {
		srv.HandleFunc(pattern, handler)
		return
	}
	var h http.Handler = handler
	for i := len(s.routeMiddleware) - 1; i >= 0; i-- {
		h = s.routeMiddleware[i](h)
	}
	srv.Handle(pattern, h)
}
