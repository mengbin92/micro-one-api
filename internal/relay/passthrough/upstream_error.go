package passthrough

import (
	"bytes"
	"net/http"
)

type Kind string

const (
	KindRetryable              Kind = "Retryable"
	KindRetryableOnSameAccount Kind = "RetryableOnSameAccount"
	KindNonRetryable           Kind = "NonRetryable"
	KindCyberBlocked           Kind = "CyberBlocked"
	KindPassthrough            Kind = "Passthrough"
	// KindRetryablePassthrough covers upstream 429s: we first try to fail over
	// to another subscription account (the upstream account is rate-limited, a
	// sibling account may still have quota), and only pass the original 429
	// (with Retry-After) back to the client once every candidate is exhausted.
	KindRetryablePassthrough Kind = "RetryablePassthrough"
)

type UpstreamError struct {
	StatusCode int
	Body       []byte
	Kind       Kind
}

func Classify(statusCode int, body []byte) UpstreamError {
	err := UpstreamError{StatusCode: statusCode, Body: body, Kind: KindNonRetryable}
	lowerBody := bytes.ToLower(body)
	if bytes.Contains(lowerBody, []byte("cyber_policy")) || bytes.Contains(lowerBody, []byte("cyber safety")) {
		err.Kind = KindCyberBlocked
		return err
	}
	switch {
	case statusCode == http.StatusTooManyRequests:
		err.Kind = KindRetryablePassthrough
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		err.Kind = KindPassthrough
	case statusCode >= 500:
		err.Kind = KindRetryable
	case statusCode == http.StatusConflict || statusCode == http.StatusLocked:
		err.Kind = KindRetryableOnSameAccount
	default:
		err.Kind = KindNonRetryable
	}
	return err
}

func (e UpstreamError) RetryableAcrossAccounts() bool {
	return e.Kind == KindRetryable || e.Kind == KindRetryablePassthrough
}

func (e UpstreamError) ShouldPassthrough() bool {
	return e.Kind == KindPassthrough || e.Kind == KindCyberBlocked || e.Kind == KindRetryablePassthrough
}
