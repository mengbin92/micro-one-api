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
	// KindOverloaded covers upstream 529 (Anthropic "Overloaded"). Semantically
	// distinct from a 429 rate-limit: the account is not over quota, the upstream
	// is momentarily saturated. We fail over to a sibling account like a 429, and
	// pass the original 529 (with Retry-After) back once every candidate is
	// exhausted, but cool the account down for a dedicated (typically shorter)
	// duration — see runtimeBlockDuration.
	KindOverloaded Kind = "Overloaded"
)

// StatusOverloaded is the non-standard 529 "Overloaded" status some upstreams
// (notably Anthropic) return when momentarily saturated.
const StatusOverloaded = 529

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
	case statusCode == StatusOverloaded:
		err.Kind = KindOverloaded
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
	return e.Kind == KindRetryable || e.Kind == KindRetryablePassthrough || e.Kind == KindOverloaded
}

// RetryableOnSameAccount reports whether the error is a transient condition that
// warrants a short in-place retry on the SAME account before failing over to a
// sibling (upstream 409/423).
func (e UpstreamError) RetryableOnSameAccount() bool {
	return e.Kind == KindRetryableOnSameAccount
}

func (e UpstreamError) ShouldPassthrough() bool {
	return e.Kind == KindPassthrough || e.Kind == KindCyberBlocked || e.Kind == KindRetryablePassthrough || e.Kind == KindOverloaded
}
