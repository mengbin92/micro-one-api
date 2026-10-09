package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseCacheExpiredCapacity(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/models", nil)
	h := ResponseCacheMiddleware(&CacheConfig{MaxSize: 1, TTL: 10 * time.Millisecond, Methods: []string{"GET"}, Paths: []string{"/v1/models"}})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("fresh")) }))
	h.ServeHTTP(httptest.NewRecorder(), r)
	time.Sleep(20 * time.Millisecond)
	h.ServeHTTP(httptest.NewRecorder(), r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Header().Get("X-Cache") != "HIT" {
		t.Fatal("expired entry permanently consumed capacity")
	}
}
