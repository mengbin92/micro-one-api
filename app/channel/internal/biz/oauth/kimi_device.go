package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

const kimiDevicePollTimeout = 15 * time.Second

var ErrKimiDeviceRetryable = errors.New("Kimi device authorization is temporarily unavailable")

func (s *Service) startKimiDevice(ctx context.Context, session *Session, result *AuthURLResult) error {
	body, status, err := s.kimiDeviceRequest(ctx, s.deviceURL, s.kimiDeviceClientID, url.Values{})
	if err != nil {
		return err
	}
	var reply struct {
		Code         string `json:"device_code"`
		UserCode     string `json:"user_code"`
		ManualURL    string `json:"verification_uri"`
		Verification string `json:"verification_uri_complete"`
		ExpiresIn    int64  `json:"expires_in"`
		Interval     int64  `json:"interval"`
	}
	if status != http.StatusOK || jsonx.Unmarshal(body, &reply) != nil {
		return fmt.Errorf("invalid Kimi device authorization response")
	}
	if reply.Verification == "" {
		reply.Verification = reply.ManualURL
	}
	if strings.TrimSpace(reply.Code) == "" || len(reply.Code) > 4096 || strings.TrimSpace(reply.UserCode) == "" || len(reply.UserCode) > 64 ||
		!validKimiVerificationURL(reply.Verification) || reply.ExpiresIn <= 0 || reply.ExpiresIn > 3600 ||
		reply.Interval < 0 || reply.Interval > 60 {
		return fmt.Errorf("invalid Kimi device authorization response")
	}
	if reply.Interval == 0 {
		reply.Interval = 5
	}
	now := s.now()
	interval := time.Duration(reply.Interval) * time.Second
	expires := now.Add(time.Duration(reply.ExpiresIn) * time.Second)
	session.Device = &DeviceSession{Code: reply.Code, ClientID: s.kimiDeviceClientID, ExpiresAt: expires, PollAt: now.Add(interval), Interval: interval}
	result.AuthURL, result.UserCode = reply.Verification, reply.UserCode
	result.ExpiresAt = min(result.ExpiresAt, expires.Unix())
	return nil
}

func validKimiVerificationURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 4096 || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "kimi.com" || strings.HasSuffix(host, ".kimi.com") ||
		host == "kimi.ai" || strings.HasSuffix(host, ".kimi.ai")
}

func (s *Service) pollKimiDevice(ctx context.Context, session *Session) (*tokenResponse, error) {
	device := session.Device
	now := s.now()
	if !now.Before(session.ExpiresAt(s.store.TTL())) {
		return nil, ErrInvalidSession
	}
	if now.Before(device.PollAt) {
		return nil, ErrDevicePending
	}
	// Keep the session readable while claiming a single request. The deadline
	// lets another replica resume polling if its owner exits before a result.
	claimed := *session
	claimedDevice := *device
	claimedDevice.PollAt = now.Add(max(device.Interval, kimiDevicePollTimeout+5*time.Second))
	claimed.Device = &claimedDevice
	swapped, err := s.store.CompareAndSwap(session, &claimed, now)
	if err != nil {
		return nil, authorization.ErrWriteStorageUnavailable
	}
	if !swapped {
		if _, ok := s.store.Get(session.ID, s.now()); !ok {
			return nil, ErrInvalidSession
		}
		return nil, ErrDevicePending
	}

	pollCtx, cancel := context.WithTimeout(ctx, min(kimiDevicePollTimeout, session.ExpiresAt(s.store.TTL()).Sub(now)))
	defer cancel()
	body, status, requestErr := s.kimiDeviceRequest(pollCtx, s.tokenURLs[PlatformKimi], device.ClientID, url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {device.Code},
	})
	var token tokenResponse
	var resultErr error
	interval := device.Interval
	switch {
	case requestErr != nil || status == http.StatusTooManyRequests || status >= 500:
		interval *= 2
		resultErr = ErrKimiDeviceRetryable
	case status == http.StatusOK:
		if jsonx.Unmarshal(body, &token) != nil || strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" ||
			token.ExpiresIn <= 0 || token.ExpiresIn > 366*86400 || (token.TokenType != "" && !strings.EqualFold(token.TokenType, "Bearer")) {
			resultErr = fmt.Errorf("invalid Kimi token response")
		}
	default:
		var reply struct {
			Error string `json:"error"`
		}
		if jsonx.Unmarshal(body, &reply) != nil {
			resultErr = fmt.Errorf("invalid Kimi token response")
			break
		}
		switch reply.Error {
		case "expired_token", "access_denied":
			resultErr = ErrInvalidSession
		case "slow_down":
			interval += 5 * time.Second
			resultErr = ErrDevicePending
		case "authorization_pending":
			resultErr = ErrDevicePending
		default:
			resultErr = fmt.Errorf("Kimi device exchange failed: status=%d", status)
		}
	}
	now = s.now()
	var next *Session
	if resultErr == ErrDevicePending || resultErr == ErrKimiDeviceRetryable {
		pending := *device
		pending.Interval, pending.PollAt = interval, now.Add(interval)
		restored := *session
		restored.Device = &pending
		next = &restored
	}
	swapped, err = s.store.CompareAndSwap(&claimed, next, now)
	if err != nil {
		return nil, authorization.ErrWriteStorageUnavailable
	}
	if !swapped {
		return nil, ErrInvalidSession
	}
	if resultErr != nil {
		return nil, resultErr
	}
	return &token, nil
}

func (s *Service) kimiDeviceRequest(ctx context.Context, endpoint, clientID string, form url.Values) ([]byte, int, error) {
	form.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, errors.New("invalid Kimi OAuth endpoint")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := *s.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("Kimi device request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil || len(body) == 64<<10 {
		return nil, 0, errors.New("invalid Kimi OAuth response size")
	}
	return body, resp.StatusCode, nil
}
