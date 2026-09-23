package server

import (
	"fmt"
	"net/http"
	"slices"

	"micro-one-api/platform/metrics"
	appmiddleware "micro-one-api/platform/middleware"
	xtrace "micro-one-api/platform/tracing"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

type routeCategory string

const (
	routeCategoryExecution   routeCategory = "execution"
	routeCategoryReadOnly    routeCategory = "read_only"
	routeCategoryUnsupported routeCategory = "unsupported"

	relayExecutionPathRaw   = "raw"
	relayExecutionPathProxy = "proxy"
)

type routeDeclaration struct {
	category      routeCategory
	endpoint      string
	executionPath string
	stream        string
}

func executionRoute(endpoint, executionPath string) routeDeclaration {
	return routeDeclaration{
		category:      routeCategoryExecution,
		endpoint:      endpoint,
		executionPath: executionPath,
		stream:        relayStreamUnknown,
	}
}

func readOnlyRoute() routeDeclaration {
	return routeDeclaration{category: routeCategoryReadOnly}
}

func unsupportedRoute() routeDeclaration {
	return routeDeclaration{category: routeCategoryUnsupported}
}

func (d routeDeclaration) validate(pattern string) {
	switch d.category {
	case routeCategoryExecution:
		if d.endpoint == "" || d.executionPath == "" || d.stream == "" {
			panic(fmt.Sprintf("execution route %q has incomplete observation declaration", pattern))
		}
	case routeCategoryReadOnly, routeCategoryUnsupported:
		if d.endpoint != "" || d.executionPath != "" || d.stream != "" {
			panic(fmt.Sprintf("non-execution route %q declares execution observation", pattern))
		}
	default:
		panic(fmt.Sprintf("route %q has no observation category", pattern))
	}
}

// RegisterRoutes registers HTTP routes to a Kratos *khttp.Server.
func (s *HTTPServer) RegisterRoutes(srv *khttp.Server) {
	// Keep the gate wrapper installed even when the feature is disabled so
	// legacy traffic remains observable without changing its behavior.
	s.handleFunc(srv, "/v1/chat/completions", executionRoute(relayEndpointChatCompletions, relayExecutionPathLegacy), s.relayOrchestratorChatHandler)
	s.handleFunc(srv, "/v1/completions", executionRoute("/v1/completions", relayExecutionPathRaw), s.handleRawRelay("/completions", true))
	s.handleFunc(srv, "/v1/embeddings", executionRoute("/v1/embeddings", relayExecutionPathRaw), s.handleRawRelay("/embeddings", false))
	s.handleFunc(srv, "/v1/images/generations", executionRoute("/v1/images/generations", relayExecutionPathRaw), s.handleRawRelay("/images/generations", true))
	s.handleFunc(srv, "/v1/images/edits", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("images.edits"))
	s.handleFunc(srv, "/v1/images/variations", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("images.variations"))
	s.handleFunc(srv, "/v1/audio/transcriptions", executionRoute("/v1/audio/transcriptions", relayExecutionPathRaw), s.handleRawRelay("/audio/transcriptions", true))
	s.handleFunc(srv, "/v1/audio/translations", executionRoute("/v1/audio/translations", relayExecutionPathRaw), s.handleRawRelay("/audio/translations", true))
	s.handleFunc(srv, "/v1/audio/speech", executionRoute("/v1/audio/speech", relayExecutionPathRaw), s.handleRawRelay("/audio/speech", false))
	s.handleFunc(srv, "/v1/moderations", executionRoute("/v1/moderations", relayExecutionPathRaw), s.handleRawRelay("/moderations", false))
	s.handleFunc(srv, "/v1/edits", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("edits"))
	s.handleFunc(srv, "/v1/responses", executionRoute(relayEndpointResponses, relayExecutionPathLegacy), s.relayOrchestratorResponsesHandler)
	s.handlePrefix(srv, "/v1/responses/", executionRoute(relayEndpointResponses, relayExecutionPathLegacy), http.HandlerFunc(s.relayOrchestratorResponsesHandler))
	s.handleFunc(srv, "/v1/usage", readOnlyRoute(), s.handleUsage)
	s.handleFunc(srv, "/v1/subscription/usage", readOnlyRoute(), s.handleSubscriptionUsage)
	s.handleFunc(srv, "/v1/engines", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("engines"))
	s.handlePrefix(srv, "/v1/engines/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("engines")))
	s.handleFunc(srv, "/v1/files", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("files"))
	s.handlePrefix(srv, "/v1/files/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("files")))
	s.handleFunc(srv, "/v1/fine-tunes", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("fine-tunes"))
	s.handlePrefix(srv, "/v1/fine-tunes/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine-tunes")))
	s.handleFunc(srv, "/v1/fine_tuning/jobs", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("fine_tuning.jobs"))
	s.handlePrefix(srv, "/v1/fine_tuning/jobs/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("fine_tuning.jobs")))
	s.handleFunc(srv, "/v1/batches", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("batches"))
	s.handlePrefix(srv, "/v1/batches/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("batches")))
	s.handleFunc(srv, "/v1/uploads", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("uploads"))
	s.handlePrefix(srv, "/v1/uploads/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("uploads")))
	s.handleFunc(srv, "/v1/vector_stores", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("vector_stores"))
	s.handlePrefix(srv, "/v1/vector_stores/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("vector_stores")))
	s.handleFunc(srv, "/v1/evals", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("evals"))
	s.handlePrefix(srv, "/v1/evals/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("evals")))
	s.handleFunc(srv, "/v1/containers", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("containers"))
	s.handlePrefix(srv, "/v1/containers/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("containers")))
	s.handlePrefix(srv, "/v1/fine_tuning/alpha/graders/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("graders")))
	s.handlePrefix(srv, "/v1/realtime/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("realtime")))
	s.handleFunc(srv, "/v1/conversations", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("conversations"))
	s.handlePrefix(srv, "/v1/conversations/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("conversations")))
	s.handleFunc(srv, "/v1/assistants", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("assistants"))
	s.handlePrefix(srv, "/v1/assistants/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("assistants")))
	s.handleFunc(srv, "/v1/threads", unsupportedRoute(), s.handleUnsupportedOpenAIRoute("threads"))
	s.handlePrefix(srv, "/v1/threads/", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("threads")))
	s.handlePrefix(srv, "/v1/oneapi/proxy/", executionRoute("/v1/oneapi/proxy", relayExecutionPathProxy), http.HandlerFunc(s.handleOneAPIProxy))

	// Anthropic Messages API inbound endpoint (for Claude Code CLI / native Anthropic SDK clients)
	s.handleFunc(srv, "/v1/messages", executionRoute(relayEndpointMessages, relayExecutionPathLegacy), s.relayOrchestratorMessagesHandler)
	s.handleFunc(srv, "/v1/models", readOnlyRoute(), s.handleModels)
	s.handlePrefix(srv, "/v1/models/", readOnlyRoute(), http.HandlerFunc(s.handleRetrieveModel))
	// Register last within /v1: exact endpoints and resource prefixes win.
	unknown := s.wrapRoute("/v1/unknown", unsupportedRoute(), http.HandlerFunc(s.handleUnsupportedOpenAIRoute("unknown_endpoint")))
	srv.HandlePrefix("/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unknown.ServeHTTP(w, appmiddleware.WithMetricPath(r, "/v1/unknown"))
	}))
	s.handleFunc(srv, "/api/status", readOnlyRoute(), s.handleAPIStatus)
	s.handleFunc(srv, "/api/models", readOnlyRoute(), s.handleDashboardModels)
	s.handleFunc(srv, "/api/group", readOnlyRoute(), s.handleGroups)
	srv.HandleFunc("/healthz", s.handleHealth)
	srv.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.Handler().ServeHTTP(w, r)
	})
}

func (s *HTTPServer) handleFunc(srv *khttp.Server, pattern string, declaration routeDeclaration, handler http.HandlerFunc) {
	srv.Handle(pattern, s.wrapRoute(pattern, declaration, handler))
}

func (s *HTTPServer) wrapRoute(pattern string, declaration routeDeclaration, handler http.Handler) http.Handler {
	declaration.validate(pattern)
	var h http.Handler = s.withRequestBudget(handler)
	h = appmiddleware.RequestBodyLimitByPath(h)
	if declaration.category == routeCategoryExecution {
		// The inner wrapper restores optional ResponseWriter capabilities after
		// generic middleware wrappers. The outer wrapper below owns the single
		// lifecycle metric and also observes middleware short-circuits.
		h = observeRelayExecution(h, declaration.endpoint, declaration.executionPath, declaration.stream)
	}
	for _, v := range slices.Backward(s.routeMiddleware) {
		h = v(h)
	}
	if declaration.category == routeCategoryExecution {
		h = observeRelayExecution(h, declaration.endpoint, declaration.executionPath, declaration.stream)
	}
	h = xtrace.Middleware(h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, appmiddleware.WithMetricPath(r, pattern))
	})
}

func (s *HTTPServer) handlePrefix(srv *khttp.Server, pattern string, declaration routeDeclaration, handler http.Handler) {
	srv.HandlePrefix(pattern, s.wrapRoute(pattern, declaration, handler))
}
