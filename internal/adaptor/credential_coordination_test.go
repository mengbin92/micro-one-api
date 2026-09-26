package adaptor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/credential"
)

type authoritativeTokenProvider struct{ err error }

func (p *authoritativeTokenProvider) RequiresAuthoritativeLookup() bool { return true }
func (p *authoritativeTokenProvider) GetAccessToken(context.Context, int64) (string, error) {
	return "current-authorization", p.err
}
func (p *authoritativeTokenProvider) Refresh(context.Context, int64) error { return nil }

func TestOAuthAdaptorsHonorAuthoritativeCredentials(t *testing.T) {
	for _, platform := range []string{"claude", "codex"} {
		for _, unavailable := range []bool{false, true} {
			t.Run(platform+map[bool]string{false: "/current", true: "/unavailable"}[unavailable], func(t *testing.T) {
				tokens := &authoritativeTokenProvider{}
				if unavailable {
					tokens.err = credential.ErrCoordinationUnavailable
				}
				rc := accountCtx(0, FormatOpenAIChatCompletions, []byte(`{"model":"test","messages":[]}`))
				rc.Account.AccessToken = "stale-selection-snapshot"
				var a Adaptor
				if platform == "claude" {
					a = NewClaudeOAuthAdaptor(tokens, nil, nil)
				} else {
					a = NewCodexOAuthAdaptor(tokens, nil, nil)
				}
				req, err := a.BuildUpstreamRequest(context.Background(), rc, FormatOpenAIChatCompletions, rc.RawBody)
				if unavailable {
					require.True(t, errors.Is(err, credential.ErrCoordinationUnavailable), "stale snapshot must not bypass coordination: %v", err)
					require.Nil(t, req)
				} else {
					require.NoError(t, err)
					require.Equal(t, "Bearer current-authorization", req.Header.Get("Authorization"))
				}
			})
		}
	}
}

func TestCoordinatedCredentialsPreserveStaticCredentials(t *testing.T) {
	for _, accountType := range []string{"setup_token", "static_key"} {
		t.Run(accountType, func(t *testing.T) {
			rc := accountCtx(0, FormatOpenAIChatCompletions, []byte(`{"model":"test","messages":[]}`))
			rc.Account.AccountType = accountType
			rc.Account.Platform = "kimi"
			rc.Account.AccessToken = "static-key"
			tokens := &authoritativeTokenProvider{err: credential.ErrNoRefreshToken}
			for _, a := range []Adaptor{NewClaudeOAuthAdaptor(tokens, nil, nil), NewCodexOAuthAdaptor(tokens, nil, nil)} {
				req, err := a.BuildUpstreamRequest(context.Background(), rc, FormatOpenAIChatCompletions, rc.RawBody)
				require.NoError(t, err)
				require.Equal(t, "Bearer static-key", req.Header.Get("Authorization"))
			}
		})
	}
}
