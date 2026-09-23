package biz

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
	"micro-one-api/pkg/jsonx"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

var (
	ErrUnsupportedNotificationType = errors.New("unsupported notification type")
	ErrNotificationSenderNotReady  = errors.New("notification sender is not configured")
)

type Sender interface {
	Send(ctx context.Context, n *Notification) error
}

type SenderConfig struct {
	WebhookURL         string
	SMTPHost           string
	SMTPPort           int
	SMTPUser           string
	SMTPPass           string
	SMTPFrom           string
	WeComWebhookURL    string
	DingTalkWebhookURL string
	FeishuWebhookURL   string
	SlackWebhookURL    string
}

type MultiSender struct {
	cfg        SenderConfig
	httpClient *http.Client
}

func NewMultiSender(cfg SenderConfig) *MultiSender {
	return &MultiSender{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *MultiSender) Send(ctx context.Context, n *Notification) error {
	switch n.Type {
	case NotifyTypeWebhook, NotifyTypeEvent:
		return s.sendWebhook(ctx, n)
	case NotifyTypeEmail:
		return s.sendEmail(ctx, n)
	case NotifyTypeWeCom:
		return s.sendWeCom(ctx, n)
	case NotifyTypeDingTalk:
		return s.sendDingTalk(ctx, n)
	case NotifyTypeFeishu:
		return s.sendFeishu(ctx, n)
	case NotifyTypeSlack:
		return s.sendSlack(ctx, n)
	default:
		return ErrUnsupportedNotificationType
	}
}

func (s *MultiSender) sendWebhook(ctx context.Context, n *Notification) error {
	endpoint := s.cfg.WebhookURL
	if isHTTPURL(n.Recipient) {
		endpoint = n.Recipient
	}
	if endpoint == "" {
		return ErrNotificationSenderNotReady
	}
	payload := map[string]any{
		"id":         n.ID,
		"type":       n.Type,
		"recipient":  n.Recipient,
		"subject":    n.Subject,
		"content":    n.Content,
		"created_at": n.CreatedAt.Unix(),
	}
	return s.sendJSONWebhook(ctx, endpoint, payload)
}

func isHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// normalizeWeComWebhookURL converts a bare key to full WeCom webhook URL if needed.
func normalizeWeComWebhookURL(raw string) string {
	if raw == "" {
		return raw
	}
	if isHTTPURL(raw) {
		return raw
	}
	return fmt.Sprintf("https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=%s", raw)
}

// normalizeDingTalkWebhookURL converts a bare access_token to full DingTalk webhook URL if needed.
func normalizeDingTalkWebhookURL(raw string) string {
	if raw == "" {
		return raw
	}
	if isHTTPURL(raw) {
		return raw
	}
	return fmt.Sprintf("https://oapi.dingtalk.com/robot/send?access_token=%s", raw)
}

func (s *MultiSender) sendEmail(ctx context.Context, n *Notification) error {
	if s.cfg.SMTPHost == "" || s.cfg.SMTPFrom == "" {
		return ErrNotificationSenderNotReady
	}
	if n.Recipient == "" {
		return ErrNotificationSenderNotReady
	}
	port := s.cfg.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", s.cfg.SMTPHost, port)
	headers := map[string]string{
		"From":         s.cfg.SMTPFrom,
		"To":           n.Recipient,
		"Subject":      n.Subject,
		"MIME-Version": "1.0",
		"Content-Type": "text/plain; charset=UTF-8",
	}
	var msg strings.Builder
	for k, v := range headers {
		msg.WriteString(k)
		msg.WriteString(": ")
		msg.WriteString(v)
		msg.WriteString("\r\n")
	}
	msg.WriteString("\r\n")
	msg.WriteString(n.Content)

	var auth smtp.Auth
	if s.cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPass, s.cfg.SMTPHost)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- smtp.SendMail(addr, auth, s.cfg.SMTPFrom, []string{n.Recipient}, []byte(msg.String()))
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}

type Dispatcher struct {
	uc       *NotifyUsecase
	sender   Sender
	interval time.Duration
	batch    int32
	maxRetry int
}

func NewDispatcher(uc *NotifyUsecase, sender Sender, interval time.Duration, batch int32, maxRetry int) *Dispatcher {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if batch <= 0 || batch > 100 {
		batch = 20
	}
	if maxRetry <= 0 {
		maxRetry = 3
	}
	return &Dispatcher{uc: uc, sender: sender, interval: interval, batch: batch, maxRetry: maxRetry}
}

func (d *Dispatcher) Start(ctx context.Context) func() {
	runCtx, cancel := context.WithCancel(ctx)
	if err := d.uc.RecoverProcessing(runCtx); err != nil {
		applogger.Log.Warn("notification processing recovery failed", zap.Error(err))
	}
	go d.loop(runCtx)
	return cancel
}

func (d *Dispatcher) DispatchOnce(ctx context.Context) error {
	items, err := d.uc.ListPending(ctx, d.batch, d.maxRetry)
	if err != nil {
		return err
	}
	for _, n := range items {
		if err := d.sender.Send(ctx, n); err != nil {
			result := "error"
			if errors.Is(err, ErrNotificationSenderNotReady) {
				result = "not_configured"
			}
			metrics.NotificationDelivery.WithLabelValues(result).Inc()
			if markErr := d.uc.CompleteProcessing(ctx, n, NotifyStatusFailed, d.maxRetry, err.Error()); markErr != nil {
				return markErr
			}
			continue
		}
		metrics.NotificationDelivery.WithLabelValues("sent").Inc()
		if err := d.uc.CompleteProcessing(ctx, n, NotifyStatusSent, d.maxRetry, ""); err != nil {
			return err
		}
	}
	return nil
}

func (d *Dispatcher) loop(ctx context.Context) {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	if err := d.DispatchOnce(ctx); err != nil {
		applogger.Log.Warn("notification dispatch failed", zap.Error(err))
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.DispatchOnce(ctx); err != nil {
				applogger.Log.Warn("notification dispatch failed", zap.Error(err))
			}
		}
	}
}

// sendWeCom sends a notification to WeCom (Enterprise WeChat) webhook.
func (s *MultiSender) sendWeCom(ctx context.Context, n *Notification) error {
	var endpoint string
	// If recipient is provided, use it (normalize if it's a bare key)
	if n.Recipient != "" {
		endpoint = normalizeWeComWebhookURL(n.Recipient)
	} else {
		// Use default endpoint from config
		endpoint = normalizeWeComWebhookURL(s.cfg.WeComWebhookURL)
	}
	if endpoint == "" {
		return ErrNotificationSenderNotReady
	}
	// Build message content with subject included
	content := n.Subject + "\n" + n.Content
	payload := map[string]any{
		"msgtype": "text",
		"text": map[string]any{
			"content": content,
		},
	}
	return s.sendJSONWebhook(ctx, endpoint, payload)
}

// sendDingTalk sends a notification to DingTalk webhook.
func (s *MultiSender) sendDingTalk(ctx context.Context, n *Notification) error {
	var endpoint string
	// If recipient is provided, use it (normalize if it's a bare token)
	if n.Recipient != "" {
		endpoint = normalizeDingTalkWebhookURL(n.Recipient)
	} else {
		// Use default endpoint from config
		endpoint = normalizeDingTalkWebhookURL(s.cfg.DingTalkWebhookURL)
	}
	if endpoint == "" {
		return ErrNotificationSenderNotReady
	}
	// Build message content with subject included
	content := n.Subject + "\n" + n.Content
	payload := map[string]any{
		"msgtype": "text",
		"text": map[string]any{
			"content": content,
		},
	}
	return s.sendJSONWebhook(ctx, endpoint, payload)
}

// sendFeishu sends a notification to Feishu (Lark) webhook.
func (s *MultiSender) sendFeishu(ctx context.Context, n *Notification) error {
	var endpoint string
	// Use recipient as webhook URL if provided
	if n.Recipient != "" {
		endpoint = n.Recipient
	} else {
		// Use default endpoint from config
		endpoint = s.cfg.FeishuWebhookURL
	}
	if endpoint == "" {
		return ErrNotificationSenderNotReady
	}
	// Build message content with subject included
	content := n.Subject + "\n" + n.Content
	payload := map[string]any{
		"msg_type": "text",
		"content": map[string]any{
			"text": content,
		},
	}
	return s.sendJSONWebhook(ctx, endpoint, payload)
}

// sendSlack sends a notification to Slack incoming webhook.
func (s *MultiSender) sendSlack(ctx context.Context, n *Notification) error {
	var endpoint string
	// Use recipient as webhook URL if provided
	if n.Recipient != "" {
		endpoint = n.Recipient
	} else {
		// Use default endpoint from config
		endpoint = s.cfg.SlackWebhookURL
	}
	if endpoint == "" {
		return ErrNotificationSenderNotReady
	}
	// Build message content with subject included
	content := n.Subject + "\n" + n.Content
	payload := map[string]any{
		"text": content,
	}
	return s.sendJSONWebhook(ctx, endpoint, payload)
}

// sendJSONWebhook is a helper for sending JSON payloads to webhook endpoints.
func (s *MultiSender) sendJSONWebhook(ctx context.Context, endpoint string, payload map[string]any) error {
	body, err := jsonx.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read webhook response: %w", err)
	}
	trimmedBody := bytes.TrimSpace(responseBody)
	if len(trimmedBody) > 0 {
		// Slack's webhook endpoint returns the plain-text body "ok" on
		// success; all other non-empty bodies must follow the JSON contract.
		if bytes.EqualFold(trimmedBody, []byte("ok")) {
			return nil
		}
		var result map[string]any
		if err := jsonx.Unmarshal(trimmedBody, &result); err != nil {
			return fmt.Errorf("invalid webhook response: %w", err)
		}
		if rejectedCode(result["errcode"]) {
			return fmt.Errorf("webhook rejected notification: errcode=%v errmsg=%v", result["errcode"], result["errmsg"])
		}
		if rejectedCode(result["code"]) {
			return fmt.Errorf("webhook rejected notification: code=%v message=%v", result["code"], result["message"])
		}
		if success, ok := result["success"].(bool); ok && !success {
			return fmt.Errorf("webhook rejected notification: success=false")
		}
	}
	return nil
}

func rejectedCode(value any) bool {
	switch v := value.(type) {
	case float64:
		return v != 0
	case string:
		return v != "" && v != "0" && v != "OK" && v != "ok"
	default:
		return false
	}
}
