package data

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"micro-one-api/app/identity/internal/biz"
)

func startMailSink(t *testing.T, reject bool) (SMTPConfig, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 2)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.WriteString(conn, "220 localhost ESMTP\r\n")
				reader := textproto.NewReader(bufio.NewReader(conn))
				for {
					line, err := reader.ReadLine()
					if err != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						_, _ = io.WriteString(conn, "250 localhost\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						_, _ = io.WriteString(conn, "250 OK\r\n")
					case line == "DATA":
						if reject {
							_, _ = io.WriteString(conn, "451 temporary failure\r\n")
							return
						}
						_, _ = io.WriteString(conn, "354 send message\r\n")
						body, err := io.ReadAll(reader.DotReader())
						if err != nil {
							return
						}
						messages <- string(body)
						_, _ = io.WriteString(conn, "250 accepted\r\n")
					case line == "QUIT":
						_, _ = io.WriteString(conn, "221 bye\r\n")
						return
					default:
						_, _ = io.WriteString(conn, "500 unsupported\r\n")
					}
				}
			}(connection)
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	return SMTPConfig{Host: host, Port: port, Mode: "plain", From: "Identity <no-reply@example.test>", PublicBaseURL: "https://console.example.test"}, messages
}

func TestSMTPCodeDelivererReceivesVerificationAndResetMessages(t *testing.T) {
	config, messages := startMailSink(t, false)
	sender := NewSMTPCodeDeliverer(config)
	if !sender.(interface{ Available() bool }).Available() {
		t.Fatal("configured sender unavailable")
	}
	if err := sender.DeliverVerificationCode(context.Background(), "TEST@example.com", "secret-verification"); err != nil {
		t.Fatal(err)
	}
	if err := sender.DeliverResetToken(context.Background(), "test+tag@example.com", "secret-reset"); err != nil {
		t.Fatal(err)
	}
	verification, reset := <-messages, <-messages
	if !strings.Contains(verification, "secret-verification") || !strings.Contains(verification, "To: TEST@example.com") || strings.Contains(verification, "secret-reset") {
		t.Fatalf("unexpected verification message")
	}
	if !strings.Contains(reset, "https://console.example.test/reset-password?email=test%2Btag%40example.com#token=secret-reset") || !strings.Contains(reset, "10") {
		t.Fatalf("unexpected reset message")
	}
}

func TestSMTPCodeDelivererRejectsFailureAndUnconfiguredTransport(t *testing.T) {
	config, _ := startMailSink(t, true)
	if err := NewSMTPCodeDeliverer(config).DeliverVerificationCode(context.Background(), "test@example.com", "secret"); !errors.Is(err, biz.ErrMailUnavailable) {
		t.Fatalf("SMTP rejection = %v", err)
	}
	config.Mode = "starttls"
	if err := NewSMTPCodeDeliverer(config).DeliverVerificationCode(context.Background(), "test@example.com", "secret"); !errors.Is(err, biz.ErrMailUnavailable) {
		t.Fatalf("STARTTLS downgrade = %v", err)
	}
	for _, cfg := range []SMTPConfig{{}, {Host: "mail.example.com", Mode: "plain", From: "x@example.com", PublicBaseURL: "https://x.example.com"}, {Host: "localhost", From: "x@example.com\r\nBcc: y@example.com", PublicBaseURL: "https://x.example.com"}, {Host: "localhost", From: "x@example.com", PublicBaseURL: "javascript:alert(1)"}} {
		sender := NewSMTPCodeDeliverer(cfg)
		if sender.(interface{ Available() bool }).Available() {
			t.Fatal("invalid configuration reported available")
		}
		if err := sender.DeliverVerificationCode(context.Background(), "test@example.com", "secret"); !errors.Is(err, biz.ErrMailUnavailable) {
			t.Fatalf("invalid configuration = %v", err)
		}
	}
}

func TestSMTPEnvironmentReusesNotificationConfigWithoutMixingCredentials(t *testing.T) {
	for _, suffix := range []string{"HOST", "PORT", "USER", "PASS", "FROM", "MODE"} {
		t.Setenv("IDENTITY_SMTP_"+suffix, "")
	}
	t.Setenv("IDENTITY_PUBLIC_BASE_URL", "https://console.example.test")
	t.Setenv("NOTIFY_SMTP_HOST", "smtp.example.test")
	t.Setenv("NOTIFY_SMTP_PORT", "465")
	t.Setenv("NOTIFY_SMTP_USER", "existing-user")
	t.Setenv("NOTIFY_SMTP_PASS", "existing-pass")
	t.Setenv("NOTIFY_SMTP_FROM", "notify@example.test")
	t.Setenv("NOTIFY_SMTP_MODE", "")
	sender := NewCodeDelivererFromEnv().(*smtpCodeDeliverer)
	if !sender.Available() || sender.config.Mode != "tls" || sender.config.User != "existing-user" || sender.config.Password != "existing-pass" {
		t.Fatal("notification SMTP settings were not reused")
	}
	t.Setenv("IDENTITY_SMTP_HOST", "identity-smtp.example.test")
	t.Setenv("IDENTITY_SMTP_FROM", "identity@example.test")
	sender = NewCodeDelivererFromEnv().(*smtpCodeDeliverer)
	if !sender.Available() || sender.config.Mode != "starttls" || sender.config.User != "" || sender.config.Password != "" || sender.config.Port != "587" {
		t.Fatal("identity SMTP group mixed notification credentials")
	}
	t.Setenv("IDENTITY_PUBLIC_BASE_URL", "")
	if NewCodeDelivererFromEnv().(*smtpCodeDeliverer).Available() {
		t.Fatal("missing public console URL reported available")
	}
}
