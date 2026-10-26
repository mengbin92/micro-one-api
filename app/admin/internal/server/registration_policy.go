package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"micro-one-api/pkg/jsonx"
)

// publicRegistrationPolicy mirrors only Identity's public policy fields. The
// owner enforces this policy; Admin must not infer it from its editable options
// or relay SMTP/Turnstile secrets through the public status response.
type publicRegistrationPolicy struct {
	RegistrationEnabled           bool     `json:"registration_enabled"`
	EmailVerification             bool     `json:"email_verification"`
	EmailDomainRestrictionEnabled bool     `json:"email_domain_restriction_enabled"`
	EmailDomainWhitelist          []string `json:"email_domain_whitelist"`
	TurnstileCheck                bool     `json:"turnstile_check"`
	TurnstileSiteKey              string   `json:"turnstile_site_key"`
	EmailDeliveryConfigured       bool     `json:"email_delivery_configured"`
}

type registrationPolicyReader struct {
	endpoint *url.URL
	client   *http.Client
}

func newRegistrationPolicyReader(identityEndpoint string) registrationPolicyReader {
	target, err := parseReverseProxyTarget(identityEndpoint)
	if err != nil {
		return registrationPolicyReader{}
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/api/registration/policy"
	target.RawPath, target.RawQuery, target.Fragment = "", "", ""
	return registrationPolicyReader{
		endpoint: target,
		client: &http.Client{
			Timeout: 3 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (r registrationPolicyReader) read(ctx context.Context) (publicRegistrationPolicy, bool) {
	// Keep the status/branding endpoint available while disabling registration
	// when Identity is unreachable, misconfigured, or returns an invalid policy.
	closed := publicRegistrationPolicy{EmailDomainWhitelist: []string{}}
	if r.endpoint == nil || r.client == nil {
		return closed, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.endpoint.String(), nil)
	if err != nil {
		return closed, false
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req) // #nosec G704 -- fixed operator-configured Identity endpoint validated at construction; redirects are disabled.
	if err != nil {
		return closed, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return closed, false
	}
	const maxPolicyBytes = 32 * 1024
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPolicyBytes+1))
	if err != nil || len(data) > maxPolicyBytes {
		return closed, false
	}
	var envelope struct {
		Success bool                      `json:"success"`
		Data    *publicRegistrationPolicy `json:"data"`
	}
	if err := jsonx.Unmarshal(data, &envelope); err != nil || !envelope.Success || envelope.Data == nil {
		return closed, false
	}
	policy := *envelope.Data
	if policy.EmailDomainWhitelist == nil {
		policy.EmailDomainWhitelist = []string{}
	}
	return policy, true
}
