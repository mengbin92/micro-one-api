// Package serviceidentity authenticates opaque, independently provisioned
// service credentials. Caller names and shared signing keys confer no identity.
package serviceidentity

import (
	"context"
	"crypto/subtle"
	"fmt"
	"os"
	"slices"
	"strings"

	"micro-one-api/pkg/jsonx"
)

type Principal struct {
	Name      string
	Dedicated bool
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func FromContext(ctx context.Context) Principal {
	p, _ := ctx.Value(principalKey{}).(Principal)
	return p
}

// Verifier contains only the receiver's trusted callers. Configuration is read
// once at construction. Neither metadata nor a JWT service_name is trusted.
type Verifier struct {
	tokens map[string]string
	legacy string
	err    error
}

func NewVerifier(tokens map[string]string, legacy string) (*Verifier, error) {
	v := &Verifier{tokens: map[string]string{}, legacy: legacy}
	seen := map[string]bool{}
	for name, token := range tokens {
		if !slices.Contains([]string{"admin", "identity", "channel", "billing", "log", "config", "monitor", "notify", "relay", "rescue"}, name) || strings.TrimSpace(token) == "" || token == legacy || seen[token] {
			return nil, fmt.Errorf("invalid or non-independent service credentials")
		}
		seen[token] = true
		v.tokens[name] = token
	}
	return v, nil
}

func FromEnvironment(legacy string) *Verifier {
	tokens := map[string]string{}
	raw := os.Getenv("SERVICE_CALLER_TOKENS")
	if raw != "" {
		if err := jsonx.Unmarshal([]byte(raw), &tokens); err != nil {
			return &Verifier{err: fmt.Errorf("invalid SERVICE_CALLER_TOKENS")}
		}
	}
	v, err := NewVerifier(tokens, legacy)
	if err != nil {
		return &Verifier{err: err}
	}
	return v
}

func (v *Verifier) Authenticate(token string) (Principal, error) {
	if v == nil || v.err != nil || token == "" {
		return Principal{}, fmt.Errorf("service authentication unavailable")
	}
	for name, trusted := range v.tokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(trusted)) == 1 {
			return Principal{Name: name, Dedicated: true}, nil
		}
	}
	if v.legacy != "" && subtle.ConstantTimeCompare([]byte(token), []byte(v.legacy)) == 1 {
		return Principal{Name: "legacy-shared"}, nil
	}
	return Principal{}, fmt.Errorf("invalid service credential")
}

// ClientToken preserves compatibility while credentials are provisioned. A
// receiver will never issue a system capability to this shared fallback.
func ClientToken() string {
	if token := os.Getenv("SERVICE_IDENTITY_TOKEN"); token != "" {
		return token
	}
	return os.Getenv("SERVICE_TOKEN")
}

func Lookup(fullMethod string) (RPCPolicy, bool) {
	p, ok := rpcPolicies[fullMethod]
	p.UserCallers = slices.Clone(p.UserCallers)
	p.SystemCallers = slices.Clone(p.SystemCallers)
	return p, ok
}

func (p Principal) SystemCapability(fullMethod string) bool {
	policy, ok := Lookup(fullMethod)
	return ok && p.Dedicated && slices.Contains(policy.SystemCallers, p.Name)
}

func (p Principal) CanCall(fullMethod string) bool {
	policy, ok := Lookup(fullMethod)
	return ok && p.Dedicated && (slices.Contains(policy.UserCallers, p.Name) || slices.Contains(policy.SystemCallers, p.Name))
}
