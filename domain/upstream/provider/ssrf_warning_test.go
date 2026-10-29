package provider

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	applogger "micro-one-api/platform/logging"
)

func TestProviderSSRFBypassWarning(t *testing.T) {
	entry := os.Getenv("PROVIDER_SSRF_WARNING_TEST_ENTRY")
	if entry == "" {
		for _, entry := range []string{"factory", "http", "stream", "validation", "dial", "redirect", "late_logger"} {
			t.Run(entry, func(t *testing.T) {
				cmd := exec.Command(os.Args[0], "-test.run=^TestProviderSSRFBypassWarning$")
				cmd.Env = append(os.Environ(), "PROVIDER_SSRF_WARNING_TEST_ENTRY="+entry)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("isolated warning check failed: %v\n%s", err, output)
				}
			})
		}
		return
	}

	core, logs := observer.New(zap.WarnLevel)
	applogger.SetLoggerForTest(t, zap.New(core))
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "false")
	NewHTTPClient(time.Second)
	if logs.Len() != 0 {
		t.Fatal("warning emitted when SSRF protection is enabled")
	}
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")

	switch entry {
	case "factory":
		NewProviderFactory(time.Second)
	case "http":
		NewHTTPClient(time.Second)
	case "late_logger":
		previous := applogger.SwapLogger(zap.NewNop())
		NewHTTPClient(time.Second)
		applogger.SwapLogger(previous)
		NewHTTPClient(time.Second)
	case "stream":
		NewStreamHTTPClient(time.Second)
	case "validation":
		if err := ValidateBaseURL("http://127.0.0.1"); err != nil {
			t.Fatal(err)
		}
	case "dial":
		dialErr := errors.New("dial sentinel")
		dialer := ssrfSafeDialer{
			lookupIP: func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.IPv4(127, 0, 0, 1)}}, nil
			},
			dialContext: func(context.Context, string, string) (net.Conn, error) { return nil, dialErr },
		}
		if _, err := dialer.DialContext(context.Background(), "tcp", "upstream.test:80"); !errors.Is(err, dialErr) {
			t.Fatalf("dial error = %v, want dial sentinel", err)
		}
	case "redirect":
		req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := upstreamRedirectPolicy(false)(req, nil); err != nil {
			t.Fatal(err)
		}
	}
	if logs.Len() != 1 {
		t.Fatalf("SSRF bypass warnings = %d, want 1 at %s boundary", logs.Len(), entry)
	}
	warning := logs.All()[0]
	if warning.ContextMap()["setting"] != "PROVIDER_DISABLE_SSRF_CHECK" || !strings.Contains(warning.Message, "SSRF") {
		t.Fatalf("warning does not identify the global SSRF bypass: %+v", warning)
	}
	NewStreamHTTPClient(time.Second)
	NewHTTPClient(time.Second)
	if logs.Len() != 1 {
		t.Fatalf("SSRF bypass warning repeated during client construction: %d", logs.Len())
	}
}
