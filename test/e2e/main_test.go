package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"micro-one-api/pkg/jsonx"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestStandaloneAccountFlow(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "fixture-admin")
	client := http.DefaultClient
	initialPassed, initialFailed := passed, failed
	t.Cleanup(func() { http.DefaultClient = client; passed, failed = initialPassed, initialFailed })
	http.DefaultClient = &http.Client{Transport: fixtureTransport(func(req *http.Request) (*http.Response, error) {
		body := ""
		code := http.StatusOK
		switch req.URL.Path {
		case "/v1/topup":
			var data struct {
				UserID string `json:"user_id"`
				Amount int64  `json:"amount"`
			}
			if req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer fixture-admin" || jsonx.NewDecoder(req.Body).Decode(&data) != nil || data.UserID != "42" || data.Amount != 1000000 {
				t.Fatalf("invalid funding request")
			}
			body = `{"success":true,"new_balance":1000100}`
		case "/api/token":
			var data struct {
				Name      string
				Unlimited bool `json:"unlimited_quota"`
				Models    []string
			}
			if req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer fixture-session" || jsonx.NewDecoder(req.Body).Decode(&data) != nil || data.Name == "" || !data.Unlimited || len(data.Models) != 1 || data.Models[0] != testModel {
				t.Fatalf("invalid self-service token request")
			}
			body = `{"success":true,"data":{"key":"fixture-result"}}`
		case "/v1/logs":
			code = http.StatusServiceUnavailable
			body = `{"success":false}`
		default:
			t.Fatalf("unexpected request to %s", req.URL.Path)
		}
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if got := stepTopUp(context.Background(), 42); got != 1000100 {
		t.Fatalf("balance = %d", got)
	}
	if got := stepCreateAPIToken(context.Background(), "fixture-session"); got != "fixture-result" {
		t.Fatal("no key issued")
	}
	stepVerifyLogs(context.Background(), 42)
	if failed != initialFailed+1 || passed != initialPassed+2 {
		t.Fatal("an unsuccessful logs response must fail the smoke test")
	}
}
