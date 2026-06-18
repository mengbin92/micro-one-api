package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	applogger "micro-one-api/internal/pkg/logger"
	relayprovider "micro-one-api/internal/relay/provider"
)

func init() {
	// Initialize logger for mock server
	if err := applogger.Initialize("info", "console"); err != nil {
		panic(fmt.Sprintf("failed to initialize logger: %v", err))
	}
}

func main() {
	port := "9999"

	mux := http.NewServeMux()
	mux.HandleFunc("/chat/completions", handleChatCompletions)
	mux.HandleFunc("/health", handleHealth)

	applogger.Log.Info("mock upstream server listening", zap.String("port", port))
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		applogger.Log.Fatal("failed to start server", zap.Error(err))
	}
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req relayprovider.ChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, "missing authorization", http.StatusUnauthorized)
		return
	}

	// Safe logging - don't log sensitive auth header
	applogger.Log.Info("received chat completions request",
		zap.String("model", req.Model),
		zap.Int("messages", len(req.Messages)),
		zap.Bool("has_auth", authHeader != ""),
	)

	content := fmt.Sprintf("Response to: %s", req.Messages[0].Content)
	if len(req.Messages) > 0 {
		content = fmt.Sprintf("Mock response for %s from model %s", req.Messages[0].Content, req.Model)
	}

	resp := relayprovider.ChatCompletionsResponse{
		ID:      "mock-" + fmt.Sprintf("%d", time.Now().Unix()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []relayprovider.Choice{
			{
				Index: 0,
				Message: relayprovider.Message{
					Role:    "assistant",
					Content: content,
				},
				FinishReason: "stop",
			},
		},
		Usage: relayprovider.Usage{
			PromptTokens:     len(req.Messages[0].Content) / 4,
			CompletionTokens: len(content) / 4,
			TotalTokens:      (len(req.Messages[0].Content) + len(content)) / 4,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
