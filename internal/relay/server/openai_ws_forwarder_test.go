package server

import (
	"net/http"
	"testing"
)

func TestIsOpenAIWSUpgradeRequest(t *testing.T) {
	tests := []struct {
		name   string
		method string
		hdr    http.Header
		want   bool
	}{
		{
			name:   "valid upgrade",
			method: http.MethodGet,
			hdr: http.Header{
				"Upgrade":    []string{"websocket"},
				"Connection": []string{"Upgrade"},
			},
			want: true,
		},
		{
			name:   "valid upgrade mixed case connection",
			method: http.MethodGet,
			hdr: http.Header{
				"Upgrade":    []string{"WebSocket"},
				"Connection": []string{"keep-alive, Upgrade"},
			},
			want: true,
		},
		{
			name:   "missing upgrade header",
			method: http.MethodGet,
			hdr: http.Header{
				"Connection": []string{"Upgrade"},
			},
			want: false,
		},
		{
			name:   "upgrade not websocket",
			method: http.MethodGet,
			hdr: http.Header{
				"Upgrade":    []string{"h2c"},
				"Connection": []string{"Upgrade"},
			},
			want: false,
		},
		{
			name:   "connection lacks upgrade",
			method: http.MethodGet,
			hdr: http.Header{
				"Upgrade":    []string{"websocket"},
				"Connection": []string{"keep-alive"},
			},
			want: false,
		},
		{
			name:   "post without upgrade headers",
			method: http.MethodPost,
			hdr:    http.Header{},
			want:   false,
		},
		{
			name:   "nil request",
			method: "",
			hdr:    nil,
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r *http.Request
			if tt.hdr != nil {
				r = &http.Request{
					Method: tt.method,
					Header: tt.hdr,
				}
			}
			if got := isOpenAIWSUpgradeRequest(r); got != tt.want {
				t.Errorf("isOpenAIWSUpgradeRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractOpenAIWSClientModel(t *testing.T) {
	tests := []struct {
		name    string
		message []byte
		want    string
	}{
		{
			name:    "model present",
			message: []byte(`{"type":"response.create","model":"gpt-5","stream":true}`),
			want:    "gpt-5",
		},
		{
			name:    "model with whitespace",
			message: []byte(`{"model":"  codex-1  "}`),
			want:    "codex-1",
		},
		{
			name:    "no model",
			message: []byte(`{"type":"response.create","stream":true}`),
			want:    "",
		},
		{
			name:    "invalid json",
			message: []byte(`{not json`),
			want:    "",
		},
		{
			name:    "empty",
			message: []byte(``),
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractOpenAIWSClientModel(tt.message); got != tt.want {
				t.Errorf("extractOpenAIWSClientModel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteOpenAIWSModel(t *testing.T) {
	t.Run("no rewrite when models equal", func(t *testing.T) {
		in := []byte(`{"type":"response.create","model":"gpt-5","input":[]}`)
		out := rewriteOpenAIWSModel(in, "gpt-5", "gpt-5")
		if string(out) != string(in) {
			t.Errorf("expected unchanged payload, got %s", string(out))
		}
	})
	t.Run("rewrites model when mapped", func(t *testing.T) {
		in := []byte(`{"type":"response.create","model":"gpt-5","input":[{"role":"user"}]}`)
		out := rewriteOpenAIWSModel(in, "gpt-5", "gpt-5-2025-08-07")
		model := extractOpenAIWSClientModel(out)
		if model != "gpt-5-2025-08-07" {
			t.Errorf("expected rewritten model, got %q (payload=%s)", model, string(out))
		}
	})
	t.Run("no rewrite when client model empty", func(t *testing.T) {
		in := []byte(`{"model":"gpt-5"}`)
		out := rewriteOpenAIWSModel(in, "", "gpt-5-2025")
		if string(out) != string(in) {
			t.Errorf("expected unchanged payload, got %s", string(out))
		}
	})
	t.Run("invalid json returns original", func(t *testing.T) {
		in := []byte(`{broken`)
		out := rewriteOpenAIWSModel(in, "a", "b")
		if string(out) != string(in) {
			t.Errorf("expected unchanged payload, got %s", string(out))
		}
	})
}
