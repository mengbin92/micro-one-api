package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	billingv1 "micro-one-api/api/billing/v1"
	admintest "micro-one-api/app/admin/testutil"
	"micro-one-api/app/identity/internal/biz"
	identityconf "micro-one-api/app/identity/internal/conf"
	"micro-one-api/app/identity/internal/data"
	"micro-one-api/app/identity/internal/server"
	"micro-one-api/pkg/jsonx"
	dbtest "micro-one-api/platform/database/testutil"
)

type ownerMail struct{ recipient, body string }
type ownerMailSink struct {
	listener net.Listener
	messages chan ownerMail
	reject   atomic.Bool
	rejected chan struct{}
}

func newOwnerMailSink(t *testing.T) *ownerMailSink {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	sink := &ownerMailSink{listener: listener, messages: make(chan ownerMail, 16), rejected: make(chan struct{}, 4)}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go sink.receive(conn)
		}
	}()
	return sink
}

func (s *ownerMailSink) receive(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.WriteString(conn, "220 localhost ESMTP\r\n")
	reader := textproto.NewReader(bufio.NewReader(conn))
	var recipient string
	for {
		line, err := reader.ReadLine()
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "MAIL"):
			_, _ = io.WriteString(conn, "250 OK\r\n")
		case strings.HasPrefix(line, "RCPT TO:"):
			recipient = strings.Trim(line[len("RCPT TO:"):], "<>")
			_, _ = io.WriteString(conn, "250 OK\r\n")
		case line == "DATA":
			if s.reject.Load() {
				_, _ = io.WriteString(conn, "451 temporary failure\r\n")
				s.rejected <- struct{}{}
				return
			}
			_, _ = io.WriteString(conn, "354 send message\r\n")
			body, err := io.ReadAll(reader.DotReader())
			if err != nil {
				return
			}
			s.messages <- ownerMail{recipient, string(body)}
			_, _ = io.WriteString(conn, "250 accepted\r\n")
		case line == "QUIT":
			_, _ = io.WriteString(conn, "221 bye\r\n")
			return
		default:
			_, _ = io.WriteString(conn, "500 unsupported\r\n")
		}
	}
}

func (s *ownerMailSink) wait(t *testing.T, email string) string {
	t.Helper()
	select {
	case message := <-s.messages:
		require.Equal(t, email, message.recipient)
		return message.body
	case <-time.After(5 * time.Second):
		t.Fatal("local SMTP sink did not receive the accepted request")
		return ""
	}
}

type ownerBilling struct {
	billingv1.BillingServiceClient
	mu     sync.Mutex
	grants []*billingv1.TopUpQuotaRequest
}

func (b *ownerBilling) TopUpQuota(_ context.Context, request *billingv1.TopUpQuotaRequest, _ ...grpc.CallOption) (*billingv1.TopUpQuotaResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.grants = append(b.grants, request)
	return &billingv1.TopUpQuotaResponse{Success: true}, nil
}
func (b *ownerBilling) grantCount() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.grants) }

func (b *ownerBilling) requireDistinctIdempotencyKeys(t *testing.T) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	seen := map[string]bool{}
	for _, grant := range b.grants {
		require.NotEmpty(t, grant.RequestId, "every grant must use Billing ledger deduplication")
		key := grant.UserId + "/" + grant.RequestId
		require.False(t, seen[key], "different grants to the same wallet must not suppress one another")
		seen[key] = true
	}
}

type ownerReply struct {
	Success bool             `json:"success"`
	Message string           `json:"message"`
	Data    jsonx.RawMessage `json:"data"`
}

func ownerRequest(t *testing.T, base, method, path, bearer string, body any) (int, ownerReply) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := jsonx.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, base+path, reader)
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	var reply ownerReply
	require.NoError(t, jsonx.Unmarshal(raw, &reply))
	return response.StatusCode, reply
}

func ownerChallengeKey(purpose, email string) string {
	hash := sha256.Sum256([]byte(purpose + ":" + strings.ToLower(strings.TrimSpace(email))))
	return "identity:email-challenge:v1:" + hex.EncodeToString(hash[:])
}

func waitOwnerActivation(t *testing.T, redisClient *redis.Client, purpose, email string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return redisClient.HGet(context.Background(), ownerChallengeKey(purpose, email), "ready").Val() == "1"
	}, 5*time.Second, time.Millisecond)
}

// This exercises actual BFF and owner HTTP servers, independent SQL/Redis clients,
// the complete SQLite migration set and SMTP protocol. It never uses deployment
// configuration or sends mail outside loopback. The optional Redis address must
// point at a disposable local server dedicated to tests.
func TestEmailAuthenticationOwnerIntegration(t *testing.T) {
	for _, mode := range []string{"legacy", "iam"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			redisAddress := os.Getenv("IDENTITY_CHALLENGE_REDIS_TEST_ADDR")
			if redisAddress == "" {
				redisAddress = miniredis.RunT(t).Addr()
			} else {
				host, _, err := net.SplitHostPort(redisAddress)
				require.NoError(t, err)
				require.Contains(t, []string{"127.0.0.1", "localhost", "::1"}, host)
			}
			t.Setenv("REDIS_ADDR", redisAddress)
			t.Setenv("REDIS_PASSWORD", "")
			t.Setenv("JWT_SECRET_KEY", "a1-owner-integration-only-secret")
			t.Setenv("TOKEN_HASH_KEY", "a1-owner-integration-only-token-key")
			t.Setenv("IDENTITY_ROUTING_V2", "false")
			t.Setenv("IDENTITY_SCHEMA", "")
			t.Setenv("DATABASE_SCHEMA", "")
			t.Setenv("ADMIN_SCHEMA", "")
			db := dbtest.RoutingContextDB(t, "sqlite")
			require.NoError(t, db.Exec("PRAGMA journal_mode=WAL").Error)
			var files []struct{ Name, File string }
			require.NoError(t, db.Raw("PRAGMA database_list").Scan(&files).Error)
			var path string
			for _, file := range files {
				if file.Name == "main" {
					path = file.File
				}
			}
			require.NotEmpty(t, path)
			if mode == "iam" {
				require.NoError(t, db.Table("iam_policy_state").Where("id = 1").Updates(map[string]any{"authorization_mode": "iam", "cutover_state": "complete", "cutover_batch_id": "a1-owner-test", "cutover_verified_at": time.Now().UnixMilli()}).Error)
			}
			for key, value := range map[string]string{"AmountForNewUser": "13", "AmountForInviter": "17", "AmountForInvitee": "11"} {
				require.NoError(t, db.Exec("INSERT INTO system_options (option_key,option_value) VALUES (?,?) ON CONFLICT(option_key) DO UPDATE SET option_value=excluded.option_value", key, value).Error)
			}
			sink := newOwnerMailSink(t)
			host, port, err := net.SplitHostPort(sink.listener.Addr().String())
			require.NoError(t, err)
			for key, value := range map[string]string{"HOST": host, "PORT": port, "USER": "", "PASS": "", "FROM": "identity@example.test", "MODE": "plain"} {
				t.Setenv("IDENTITY_SMTP_"+key, value)
			}
			t.Setenv("IDENTITY_PUBLIC_BASE_URL", "https://console.example.test")
			redisClient := redis.NewClient(&redis.Options{Addr: redisAddress})
			t.Cleanup(func() { _ = redisClient.Close() })
			cfg := &Config{Bootstrap: &identityconf.Bootstrap{Registration: &identityconf.Registration{Enabled: true, EmailVerificationEnabled: true}}}
			billing := &ownerBilling{}
			var frontends []*httptest.Server
			var identities []*biz.IdentityUsecase
			for range 2 {
				repository, err := data.NewRepositoryFromEnv("sqlite3", path+"?_foreign_keys=on&_busy_timeout=5000", "")
				require.NoError(t, err)
				identity := newIdentityUsecase(repository, nil)
				policy := emailAuthenticationPolicy(cfg, repository)
				require.True(t, policy.Challenges.Available())
				owner := httptest.NewServer(server.NewHTTPServerWithRegistrationPolicy(":0", identity, nil, policy, billing))
				t.Cleanup(owner.Close)
				frontend := httptest.NewServer(admintest.NewPublicIdentityHTTP(owner.URL))
				t.Cleanup(frontend.Close)
				frontends = append(frontends, frontend)
				identities = append(identities, identity)
			}
			first, second := frontends[0].URL, frontends[1].URL
			status, reply := ownerRequest(t, first, http.MethodGet, "/api/status", "", nil)
			require.Equal(t, http.StatusOK, status)
			require.True(t, reply.Success, reply.Message)
			var publicPolicy map[string]any
			require.NoError(t, jsonx.Unmarshal(reply.Data, &publicPolicy))
			for _, field := range []string{"registration_enabled", "email_verification", "email_delivery_configured", "registration_policy_available"} {
				require.Equal(t, true, publicPolicy[field], field)
			}
			unique := fmt.Sprintf("%s-%d", mode, time.Now().UnixNano())
			email, retryEmail := unique+"@example.test", "retry-"+unique+"@example.test"
			t.Cleanup(func() {
				for _, address := range []string{email, retryEmail} {
					_ = redisClient.Del(context.Background(), ownerChallengeKey(biz.ChallengeVerification, address), ownerChallengeKey(biz.ChallengePasswordReset, address)).Err()
				}
			})
			inviter, err := identities[0].Register(ctx, "inviter", "password123", "inviter-"+email, "default")
			require.NoError(t, err)
			status, reply = ownerRequest(t, first, http.MethodGet, "/api/verification?email="+url.QueryEscape(email), "", nil)
			require.Equal(t, http.StatusOK, status)
			require.True(t, reply.Success, reply.Message)
			verificationMail := sink.wait(t, email)
			code := regexp.MustCompile(`\b[a-f0-9]{32}\b`).FindString(verificationMail)
			require.NotEmpty(t, code)
			registration := map[string]any{"username": "invitee", "password": "password123", "email": email, "verification_code": code, "aff_code": inviter.AffCode}
			_, reply = ownerRequest(t, second, http.MethodPost, "/api/user/register", "", registration)
			require.True(t, reply.Success, reply.Message)
			require.Equal(t, 3, billing.grantCount(), "registration and invitation grants are issued once")
			billing.requireDistinctIdempotencyKeys(t)
			_, reply = ownerRequest(t, first, http.MethodPost, "/api/user/register", "", registration)
			require.False(t, reply.Success, "a consumed registration code cannot issue grants again")
			require.Equal(t, 3, billing.grantCount())
			var registrations int64
			require.NoError(t, db.Table("users").Where("username = ?", "invitee").Count(&registrations).Error)
			require.Equal(t, int64(1), registrations)
			_, reply = ownerRequest(t, first, http.MethodPost, "/api/user/login", "", map[string]string{"username": "invitee", "password": "password123"})
			require.True(t, reply.Success, reply.Message)
			var login struct {
				Token string `json:"token"`
			}
			require.NoError(t, jsonx.Unmarshal(reply.Data, &login))
			require.NotEmpty(t, login.Token)
			_, reply = ownerRequest(t, second, http.MethodGet, "/api/user/self", login.Token, nil)
			require.True(t, reply.Success, reply.Message)
			_, known := ownerRequest(t, first, http.MethodGet, "/api/reset_password?email="+url.QueryEscape(email), "", nil)
			_, unknown := ownerRequest(t, first, http.MethodGet, "/api/reset_password?email="+url.QueryEscape("unknown-"+email), "", nil)
			require.True(t, known.Success)
			require.Equal(t, known, unknown, "public acceptance must not expose account existence")
			resetMail := sink.wait(t, email)
			link := regexp.MustCompile(`https://console\.example\.test/reset-password\?[^\s]+`).FindString(resetMail)
			require.NotEmpty(t, link)
			resetURL, err := url.Parse(link)
			require.NoError(t, err)
			fragment, err := url.ParseQuery(resetURL.Fragment)
			require.NoError(t, err)
			token := fragment.Get("token")
			require.NotEmpty(t, token)
			require.Empty(t, resetURL.Query().Get("token"), "reset secrets must not appear in HTTP query logs")
			waitOwnerActivation(t, redisClient, biz.ChallengePasswordReset, email)
			reset := map[string]string{"email": email, "token": token, "password": "changed123"}
			_, reply = ownerRequest(t, second, http.MethodPost, "/api/user/reset", "", reset)
			require.True(t, reply.Success, reply.Message)
			_, reply = ownerRequest(t, first, http.MethodPost, "/api/user/reset", "", reset)
			require.False(t, reply.Success, "reset token replay must fail on the other replica")
			status, _ = ownerRequest(t, first, http.MethodGet, "/api/user/self", login.Token, nil)
			require.Equal(t, http.StatusUnauthorized, status, "reset must revoke sessions across SQL connections")
			_, reply = ownerRequest(t, second, http.MethodPost, "/api/user/login", "", map[string]string{"username": "invitee", "password": "changed123"})
			require.True(t, reply.Success, reply.Message)
			_, err = identities[0].Register(ctx, "retry-user", "password123", retryEmail, "default")
			require.NoError(t, err)
			sink.reject.Store(true)
			_, failed := ownerRequest(t, first, http.MethodGet, "/api/reset_password?email="+url.QueryEscape(retryEmail), "", nil)
			require.Equal(t, known, failed, "delivery failures must not expose account state")
			select {
			case <-sink.rejected:
			case <-time.After(5 * time.Second):
				t.Fatal("expected SMTP failure was not exercised")
			}
			require.Eventually(t, func() bool {
				return redisClient.Exists(ctx, ownerChallengeKey(biz.ChallengePasswordReset, retryEmail)).Val() == 0
			}, 5*time.Second, time.Millisecond, "failed delivery must revoke its pending challenge")
			sink.reject.Store(false)
			_, reply = ownerRequest(t, second, http.MethodGet, "/api/reset_password?email="+url.QueryEscape(retryEmail), "", nil)
			require.True(t, reply.Success, reply.Message)
			require.Contains(t, sink.wait(t, retryEmail), "#token=", "a failed delivery can be retried immediately through another replica")
			waitOwnerActivation(t, redisClient, biz.ChallengePasswordReset, retryEmail)
		})
	}
}
