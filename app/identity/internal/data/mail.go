package data

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"micro-one-api/app/identity/internal/biz"
)

// SMTPConfig is transport configuration, never an HTTP response or domain model.
type SMTPConfig struct {
	Host, Port, User, Password, From, Mode, PublicBaseURL string
	Timeout                                               time.Duration
}

type smtpCodeDeliverer struct {
	config  SMTPConfig
	from    *mail.Address
	baseURL *url.URL
	valid   bool
}

// NewCodeDelivererFromEnv reads identity-specific secrets without exposing them
// through public system options. An incomplete configuration is unavailable.
func NewCodeDelivererFromEnv() biz.CodeDeliverer {
	prefix := "IDENTITY_SMTP_"
	if strings.TrimSpace(os.Getenv(prefix+"HOST")) == "" {
		prefix = "NOTIFY_SMTP_"
	}
	return NewSMTPCodeDeliverer(SMTPConfig{
		Host: os.Getenv(prefix + "HOST"), Port: os.Getenv(prefix + "PORT"),
		User: os.Getenv(prefix + "USER"), Password: os.Getenv(prefix + "PASS"),
		From: os.Getenv(prefix + "FROM"), Mode: os.Getenv(prefix + "MODE"),
		PublicBaseURL: os.Getenv("IDENTITY_PUBLIC_BASE_URL"),
	})
}

func NewSMTPCodeDeliverer(config SMTPConfig) biz.CodeDeliverer {
	config.Host = strings.TrimSpace(config.Host)
	config.Mode = strings.ToLower(strings.TrimSpace(config.Mode))
	if config.Mode == "" {
		config.Mode = "starttls"
		if config.Port == "465" {
			config.Mode = "tls"
		}
	}
	if config.Port == "" {
		config.Port = "587"
		if config.Mode == "tls" {
			config.Port = "465"
		}
	}
	if config.Timeout <= 0 || config.Timeout > 30*time.Second {
		config.Timeout = 10 * time.Second
	}
	value := &smtpCodeDeliverer{config: config}
	if config.Host == "" || strings.ContainsAny(config.Host, "\r\n") || strings.ContainsAny(config.From, "\r\n") {
		return value
	}
	port, err := strconv.Atoi(config.Port)
	if err != nil || port <= 0 || port > 65535 {
		return value
	}
	if config.Mode != "starttls" && config.Mode != "tls" && config.Mode != "plain" {
		return value
	}
	// Unencrypted transport is restricted to loopback mail sinks. Production
	// delivery always verifies TLS certificates and never falls back to plaintext.
	if config.Mode == "plain" {
		ip := net.ParseIP(config.Host)
		if (ip == nil || !ip.IsLoopback()) && config.Host != "localhost" {
			return value
		}
	}
	from, err := mail.ParseAddress(config.From)
	if err != nil {
		return value
	}
	baseURL, err := url.Parse(strings.TrimSpace(config.PublicBaseURL))
	if err != nil || baseURL.Host == "" || (baseURL.Scheme != "https" && baseURL.Scheme != "http") || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return value
	}
	value.from, value.baseURL, value.valid = from, baseURL, true
	return value
}

func (s *smtpCodeDeliverer) Available() bool { return s != nil && s.valid }

func (s *smtpCodeDeliverer) DeliverVerificationCode(ctx context.Context, email, code string) error {
	return s.send(ctx, email, "Verify your email / 验证邮箱", "Your email verification code / 您的邮箱验证码：\r\n\r\n"+code+"\r\n\r\nValid for 10 minutes and one use. / 10 分钟内有效，仅可使用一次。\r\nIf you did not request this, ignore this message. / 如非本人操作，请忽略。\r\n")
}

func (s *smtpCodeDeliverer) DeliverResetToken(ctx context.Context, email, token string) error {
	if !s.Available() {
		return biz.ErrMailUnavailable
	}
	resetURL := *s.baseURL
	resetURL.Path = strings.TrimRight(resetURL.Path, "/") + "/reset-password"
	resetURL.RawPath = ""
	resetURL.RawQuery = url.Values{"email": {strings.TrimSpace(email)}}.Encode()
	resetURL.Fragment = url.Values{"token": {token}}.Encode()
	return s.send(ctx, email, "Reset your password / 重置密码", "Reset your password / 请打开链接重置密码：\r\n\r\n"+resetURL.String()+"\r\n\r\nValid for 10 minutes and one use. / 10 分钟内有效，仅可使用一次。\r\nIf you did not request this, ignore this message. / 如非本人操作，请忽略。\r\n")
}

func (s *smtpCodeDeliverer) send(ctx context.Context, email, subject, body string) error {
	if !s.Available() {
		return biz.ErrMailUnavailable
	}
	email = strings.TrimSpace(email)
	_, err := biz.NormalizeEmail(email)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	address := net.JoinHostPort(s.config.Host, s.config.Port)
	dialer := &net.Dialer{}
	tlsConfig := &tls.Config{ServerName: s.config.Host, MinVersion: tls.VersionTLS12}
	var conn net.Conn
	if s.config.Mode == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return biz.ErrMailUnavailable
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return biz.ErrMailUnavailable
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	client, err := smtp.NewClient(conn, s.config.Host)
	if err != nil {
		return biz.ErrMailUnavailable
	}
	defer client.Close()
	if s.config.Mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return biz.ErrMailUnavailable
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return biz.ErrMailUnavailable
		}
	}
	if s.config.User != "" {
		if err := client.Auth(smtp.PlainAuth("", s.config.User, s.config.Password, s.config.Host)); err != nil {
			return biz.ErrMailUnavailable
		}
	}
	if err := client.Mail(s.from.Address); err != nil {
		return biz.ErrMailUnavailable
	}
	if err := client.Rcpt(email); err != nil {
		return biz.ErrMailUnavailable
	}
	writer, err := client.Data()
	if err != nil {
		return biz.ErrMailUnavailable
	}
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s", s.from.String(), email, mime.QEncoding.Encode("UTF-8", subject), body)
	if _, err := writer.Write([]byte(message)); err != nil {
		return biz.ErrMailUnavailable
	}
	if err := writer.Close(); err != nil {
		return biz.ErrMailUnavailable
	}
	// DATA's final 250 is the submission boundary. A lost QUIT response must
	// not revoke an already accepted message or prompt a duplicate submission.
	_ = client.Quit()
	return nil
}
