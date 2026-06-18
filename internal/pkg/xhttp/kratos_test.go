package xhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

func TestSafeKratosServerOptionsDoNotUseDefaultServeMux(t *testing.T) {
	origDefaultServeMux := http.DefaultServeMux
	t.Cleanup(func() {
		http.DefaultServeMux = origDefaultServeMux
	})
	http.DefaultServeMux = http.NewServeMux()
	http.DefaultServeMux.HandleFunc("/default-mux-only", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("http.DefaultServeMux should not handle unmatched Kratos routes")
	})

	srv := khttp.NewServer(SafeKratosServerOptions()...)
	srv.Route("/").GET("/registered", func(ctx khttp.Context) error {
		return ctx.String(http.StatusOK, "ok")
	})

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/default-mux-only", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unmatched status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/registered", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d, want 405", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), http.StatusText(http.StatusMethodNotAllowed)) {
		t.Fatalf("405 body = %q", rec.Body.String())
	}
}
