// Deterministic local upstream; it never contacts a paid provider.
package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"micro-one-api/pkg/jsonx"
)

var calls atomic.Int64

func main() {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		jsonx.NewEncoder(w).Encode(map[string]any{"calls": calls.Load()})
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			ws(w, r)
			return
		}
		var req map[string]any
		if jsonx.NewDecoder(r.Body).Decode(&req) != nil {
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		id := fmt.Sprintf("resp_e2e_%d", n)
		// Delay occurs after reserve and before usage is returned, so an in-flight price change is observable.
		if strings.Contains(fmt.Sprint(req), "freeze-price") {
			time.Sleep(1500 * time.Millisecond)
		}
		stream, _ := req["stream"].(bool)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "embeddings") {
			jsonx.NewEncoder(w).Encode(map[string]any{"object": "list", "model": req["model"], "data": []any{map[string]any{"object": "embedding", "index": 0, "embedding": []float64{.1, .2}}}, "usage": map[string]any{"prompt_tokens": 10, "total_tokens": 10}})
			return
		}
		if strings.Contains(r.URL.Path, "responses") {
			response := map[string]any{"id": id, "object": "response", "status": "completed", "model": req["model"], "output": []any{map[string]any{"type": "message", "role": "assistant", "id": "msg_e2e", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "routing e2e"}}}}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}}
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				event(w, "response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "model": req["model"]}})
				event(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": 0, "content_index": 0, "delta": "routing e2e"})
				event(w, "response.completed", map[string]any{"type": "response.completed", "response": response})
			} else {
				jsonx.NewEncoder(w).Encode(response)
			}
			return
		}
		model := fmt.Sprint(req["model"])
		if strings.HasPrefix(model, "fault-") {
			http.Error(w, "controlled upstream failure", 500)
			return
		}
		if strings.HasPrefix(model, "partial-") {
			w.Header().Set("Content-Type", "text/event-stream")
			if strings.Contains(r.URL.Path, "messages") {
				event(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]any{"input_tokens": 4, "output_tokens": 0}}})
				event(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "partial"}})
			} else {
				event(w, "", map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "partial"}}}})
			}
			return
		}
		if strings.Contains(r.URL.Path, "messages") {
			w.Header().Set("Content-Type", "text/event-stream")
			event(w, "message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": id, "type": "message", "role": "assistant", "model": model, "content": []any{}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 0}}})
			event(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 20}})
			event(w, "message_stop", map[string]any{"type": "message_stop"})
			return
		}
		usage := map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, chunk := range []any{map[string]any{"id": id, "object": "chat.completion.chunk", "model": req["model"], "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "routing e2e"}, "finish_reason": nil}}}, map[string]any{"id": id, "object": "chat.completion.chunk", "model": req["model"], "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": usage}} {
				raw, _ := jsonx.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		jsonx.NewEncoder(w).Encode(map[string]any{"id": id, "object": "chat.completion", "model": req["model"], "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "routing e2e"}, "finish_reason": "stop"}}, "usage": usage})
	})
	// G114: serve through an explicit *http.Server so read-side timeouts
	// apply. WriteTimeout deliberately stays unset: this mock streams SSE
	// chunks and hosts websocket upgrades (and sleeps 1.5s on freeze-price
	// fixtures), which a write deadline would truncate mid-stream.
	server := &http.Server{
		Addr:              ":9999",
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}
func event(w http.ResponseWriter, name string, payload any) {
	raw, _ := jsonx.Marshal(payload)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}
func ws(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.CloseNow()
	for {
		_, _, err = c.Read(r.Context())
		if err != nil {
			return
		}
		id := fmt.Sprintf("resp_ws_%d", calls.Add(1))
		for _, v := range []any{map[string]any{"type": "response.created", "response": map[string]any{"id": id, "status": "in_progress", "object": "response"}}, map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "object": "response", "output": []any{}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 20, "total_tokens": 120}}}} {
			raw, _ := jsonx.Marshal(v)
			if c.Write(r.Context(), websocket.MessageText, raw) != nil {
				return
			}
		}
	}
}
