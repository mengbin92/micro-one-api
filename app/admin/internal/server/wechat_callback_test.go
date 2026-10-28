package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminWechatNotifyPreservesSignedBodyAndHeaders(t *testing.T) {
	const payload = `{"id":"event-1","resource":{"ciphertext":"signed-ciphertext"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload || r.Header.Get("Wechatpay-Signature") != "signed-value" || r.Header.Get("Wechatpay-Timestamp") != "12345" || r.Header.Get("Wechatpay-Nonce") != "nonce" || r.Header.Get("Wechatpay-Serial") != "serial" {
			t.Errorf("callback changed: body=%s headers=%v err=%v", body, r.Header, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	t.Setenv("BILLING_HTTP_ENDPOINT", upstream.URL)
	srv := NewHTTPServer(":0", nil, nil)
	for _, path := range []string{"/api/v1/user/payments/wechat/notify", "/api/user/payments/wechat/notify"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Wechatpay-Signature", "signed-value")
		req.Header.Set("Wechatpay-Timestamp", "12345")
		req.Header.Set("Wechatpay-Nonce", "nonce")
		req.Header.Set("Wechatpay-Serial", "serial")
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("path=%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}
