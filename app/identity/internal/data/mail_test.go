package data

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/mail"
	"net/textproto"
	"net/url"
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

func TestSMTPCodeDelivererRejectsRecipientInjection(t *testing.T) {
	config, messages := startMailSink(t, false)
	sender := NewSMTPCodeDeliverer(config)
	for _, email := range []string{
		"victim@example.com\r\nBcc: attacker@example.net",
		"victim@example.com\nContent-Type: text/html",
		"victim@example.com,attacker@example.net",
		"Attacker <victim@example.com>",
		"victim@example.com\r\n\r\n<script>alert(1)</script>",
	} {
		if err := sender.DeliverVerificationCode(context.Background(), email, "test-code"); !errors.Is(err, biz.ErrEmailInvalid) {
			t.Fatalf("verification accepted injected recipient %q: %v", email, err)
		}
		if err := sender.DeliverResetToken(context.Background(), email, "test-token"); !errors.Is(err, biz.ErrEmailInvalid) {
			t.Fatalf("reset accepted injected recipient %q: %v", email, err)
		}
	}
	select {
	case <-messages:
		t.Fatal("invalid recipient reached SMTP DATA")
	default:
	}
}

func TestSMTPResetLinkPreservesTrustedOriginAndEncodesRecipient(t *testing.T) {
	config, messages := startMailSink(t, false)
	config.PublicBaseURL += "/console"
	email := "O'Hara+tag&token=other@example.com"
	token := "test-token\r\nBcc: attacker@example.net"
	if err := NewSMTPCodeDeliverer(config).DeliverResetToken(context.Background(), email, token); err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(strings.NewReader(<-messages))
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("To") != email || message.Header.Get("Bcc") != "" || message.Header.Get("Content-Type") != "text/plain; charset=UTF-8" {
		t.Fatal("recipient or MIME headers changed")
	}
	body, err := io.ReadAll(message.Body)
	if err != nil {
		t.Fatal(err)
	}
	var resetURL *url.URL
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, config.PublicBaseURL) {
			resetURL, err = url.Parse(strings.TrimSpace(line))
			break
		}
	}
	if err != nil || resetURL == nil {
		t.Fatal("missing reset URL")
	}
	fragment, err := url.ParseQuery(resetURL.Fragment)
	if err != nil || resetURL.Scheme != "https" || resetURL.Host != "console.example.test" || resetURL.Path != "/console/reset-password" || resetURL.Query().Get("email") != email || len(resetURL.Query()) != 1 || fragment.Get("token") != token || len(fragment) != 1 {
		t.Fatal("untrusted values changed the reset URL structure")
	}
}

func TestSMTPBodyDotTerminatorCannotInjectCommands(t *testing.T) {
	config, messages := startMailSink(t, false)
	code := "test-code\r\n.\r\nRCPT TO:<attacker@example.net>\r\nDATA\r\n"
	if err := NewSMTPCodeDeliverer(config).DeliverVerificationCode(context.Background(), "victim@example.com", code); err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(strings.NewReader(<-messages))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(message.Body)
	if err != nil || !strings.Contains(string(body), "test-code\n.\nRCPT TO:<attacker@example.net>\nDATA\n") || message.Header.Get("Bcc") != "" {
		t.Fatal("SMTP DATA terminator escaped the plain-text message body")
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
