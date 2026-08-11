package biz

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"micro-one-api/platform/audit"
)

const (
	UserStatusEnabled  int32 = 1
	UserStatusDisabled int32 = 2

	// User role scale mirrors upstream one-api so existing semantics carry
	// over: only admin-or-higher can manage the platform; root is the
	// bootstrap account and cannot be demoted by other admins.
	RoleGuestUser  int32 = 0
	RoleCommonUser int32 = 1
	RoleAdminUser  int32 = 10
	RoleRootUser   int32 = 100

	TokenStatusEnabled   int32 = 1
	TokenStatusDisabled  int32 = 2
	TokenStatusExpired   int32 = 3
	TokenStatusExhausted int32 = 4
)

var (
	ErrInvalidToken         = errors.New("invalid token")
	ErrTokenExpired         = errors.New("token expired")
	ErrTokenExhausted       = errors.New("token exhausted")
	ErrTokenQuotaInvalid    = errors.New("token quota must be positive for a limited key")
	ErrTokenDisabled        = errors.New("token disabled")
	ErrTokenInUse           = errors.New("cannot delete current session token")
	ErrUserDisabled         = errors.New("user disabled")
	ErrTokenSubnetViolation = errors.New("token subnet restriction violated")
	ErrUserNotFound         = errors.New("user not found")
	ErrTokenNotFound        = errors.New("token not found")
	ErrTokenNameRequired    = errors.New("token name is required")
	ErrUserExists           = errors.New("user already exists")
	ErrInvalidPassword      = errors.New("invalid password")
	ErrOAuthUserNotFound    = errors.New("oauth user not found")
	ErrOAuthAlreadyBound    = errors.New("oauth identity already bound")
	// ErrSessionRevoked is returned when a session JWT predates the user's
	// current password epoch (review M6): the token was issued before the
	// password changed / a forced logout, so it must no longer be honored.
	ErrSessionRevoked = errors.New("session revoked")
)

type User struct {
	ID            int64
	Username      string
	DisplayName   string
	Email         string
	Group         string
	Status        int32
	Role          int32
	PasswordHash  string
	OAuthProvider string
	OAuthID       string
	Balance       int64
	AffCode       string
	InviterID     int64
	// PasswordChangedAt is the unix epoch (milliseconds) of the most recent
	// password change. It is embedded in session JWTs as `pwd_epoch`; any
	// session token whose epoch predates this value is rejected on
	// validation (review M6). This lets a password change / reset / logout
	// invalidate previously-issued sessions without a server-side revocation
	// list. Zero means "never set / migration", treated as no constraint.
	PasswordChangedAt int64
}

// IsAdmin reports whether the user has admin-or-higher privileges. Use this
// instead of comparing Username, so admins remain admins after renaming.
func (u *User) IsAdmin() bool {
	return u != nil && u.Role >= RoleAdminUser
}

// IsRoot reports whether the user is the bootstrap root account. Root is
// effectively an admin that other admins cannot demote.
func (u *User) IsRoot() bool {
	return u != nil && u.Role >= RoleRootUser
}

type OAuthIdentity struct {
	ID         int64
	UserID     int64
	Provider   string
	ProviderID string
	CreatedAt  int64
	UpdatedAt  int64
}

type Token struct {
	ID     int64
	UserID int64
	Name   string
	// Key holds the plaintext key at creation time (returned to the caller
	// exactly once). When loaded from storage it holds only a short display
	// prefix (first 8 + last 4 chars) — never the full secret — so a DB
	// leak cannot authenticate as the user.
	Key string
	// KeyHash is the HMAC-SHA256 of the full plaintext key, stored in the
	// key_hash column and used for O(1) lookups on the ValidateToken hot path.
	KeyHash        string
	Status         int32
	ExpiredAt      int64
	RemainQuota    int64
	UnlimitedQuota bool
	UsedQuota      int64
	AccessedAt     int64
	Subnet         string
	Models         []string
	CreatedAt      int64
}

// AuthSnapshot is the minimum authorization view returned to relay-gateway.
type AuthSnapshot struct {
	UserID        int64
	TokenID       int64
	TokenName     string
	Group         string
	AllowedModels []string
	UserEnabled   bool
	TokenEnabled  bool
}

type UserSessionClaims struct {
	UserID    int64  `json:"user_id"`
	Username  string `json:"username"`
	Role      int32  `json:"role"`
	TokenType string `json:"token_type"`
	// PwdEpoch carries the user's PasswordChangedAt (Unix ms) at signing time. The
	// validator rejects tokens whose PwdEpoch is older than the stored
	// PasswordChangedAt, so a password change revokes outstanding sessions.
	PwdEpoch int64 `json:"pwd_epoch,omitempty"`
	jwt.RegisteredClaims
}

type IdentityRepo interface {
	FindTokenByKey(ctx context.Context, key string) (*Token, error)
	FindUserByID(ctx context.Context, userID int64) (*User, error)
	FindUserByUsername(ctx context.Context, username string) (*User, error)
	FindUserByEmail(ctx context.Context, email string) (*User, error)
	FindUserByAffCode(ctx context.Context, affCode string) (*User, error)
	FindUserByOAuth(ctx context.Context, provider, oauthID string) (*User, error)
	FindOAuthIdentity(ctx context.Context, provider, providerID string) (*OAuthIdentity, error)
	FindOAuthIdentityByUserProvider(ctx context.Context, userID int64, provider string) (*OAuthIdentity, error)
	CreateOAuthIdentity(ctx context.Context, identity *OAuthIdentity) error
	CreateUser(ctx context.Context, user *User) error
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, userID int64) error
	IncreaseUserBalance(ctx context.Context, userID int64, amount int64) error
	CreateToken(ctx context.Context, token *Token) error
	FindTokenByID(ctx context.Context, userID, tokenID int64) (*Token, error)
	ListTokens(ctx context.Context, userID int64, page, pageSize int32, keyword string) ([]*Token, int64, error)
	UpdateToken(ctx context.Context, token *Token) error
	// ConsumeTokenQuota atomically decrements RemainQuota and increments
	// UsedQuota for the given token, returning the updated RemainQuota.
	// When RemainQuota reaches 0 the caller should mark the token
	// exhausted. The decrement is clamped at 0 (never goes negative).
	ConsumeTokenQuota(ctx context.Context, userID, tokenID, amount int64) (remaining int64, err error)
	DeleteToken(ctx context.Context, userID, tokenID int64) error
	ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*User, int64, error)
	CountUsers(ctx context.Context) (int64, error)
}

// loginAttempt tracks failed login attempts for rate limiting
type loginAttempt struct {
	count    int
	lastSeen time.Time
}

type IdentityUsecase struct {
	repo            IdentityRepo
	auditor         *audit.Auditor
	now             func() time.Time
	defaultQuota    int64
	sessionSecret   []byte
	sessionIssuer   string
	sessionDuration time.Duration
	loginLimiter    map[string]*loginAttempt
	loginMutex      sync.Mutex
}

const (
	maxLoginAttempts   = 5
	loginLockoutTime   = 5 * time.Minute
	loginLimiterCap    = 100_000 // hard ceiling on the in-memory login limiter (review L2)
	loginSweepInterval = 10 * time.Minute
)

// NewIdentityUsecase creates the identity usecase. auditor is the audit sink
// for login/logout events; it is injected (not a package singleton) so tests
// can assert on emitted events and the enabled flag is configurable.
func NewIdentityUsecase(repo IdentityRepo, auditor *audit.Auditor) *IdentityUsecase {
	defaultQuota := int64(1000000) // 1M tokens
	if v := os.Getenv("DEFAULT_USER_QUOTA"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			defaultQuota = n
		}
	}
	sessionSecret := []byte(os.Getenv("JWT_SECRET_KEY"))
	if len(sessionSecret) == 0 {
		sessionSecret = make([]byte, 32)
		if _, err := rand.Read(sessionSecret); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
	}
	sessionIssuer := os.Getenv("JWT_ISSUER")
	if sessionIssuer == "" {
		sessionIssuer = "micro-one-api"
	}
	sessionDuration := 24 * time.Hour
	if v := os.Getenv("USER_JWT_TOKEN_DURATION"); v != "" {
		if duration, err := time.ParseDuration(v); err == nil && duration > 0 {
			sessionDuration = duration
		}
	} else if v := os.Getenv("JWT_TOKEN_DURATION"); v != "" {
		if duration, err := time.ParseDuration(v); err == nil && duration > 0 {
			sessionDuration = duration
		}
	}
	if auditor == nil {
		auditor = audit.NewAuditor(true)
	}
	return &IdentityUsecase{
		repo:            repo,
		auditor:         auditor,
		now:             time.Now,
		defaultQuota:    defaultQuota,
		sessionSecret:   sessionSecret,
		sessionIssuer:   sessionIssuer,
		sessionDuration: sessionDuration,
		loginLimiter:    make(map[string]*loginAttempt),
	}
}

// ValidateToken validates an API access token. clientIP is the caller's
// remote IP (best-effort, may be empty when unavailable); when non-empty and
// the token carries a Subnet CIDR restriction, the IP must fall within the
// CIDR or the token is rejected (review M1 — previously the field was
// accepted/persisted but never enforced).
func (uc *IdentityUsecase) ValidateToken(ctx context.Context, key, clientIP string) (*Token, error) {
	if strings.TrimSpace(key) == "" {
		return nil, ErrInvalidToken
	}
	token, err := uc.repo.FindTokenByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if token.Status == TokenStatusExpired {
		return nil, ErrTokenExpired
	}
	if token.Status == TokenStatusExhausted {
		return nil, ErrTokenExhausted
	}
	if token.Status != TokenStatusEnabled {
		return nil, ErrTokenDisabled
	}
	if token.ExpiredAt > 0 && token.ExpiredAt < uc.now().Unix() {
		return nil, ErrTokenExpired
	}
	if !tokenSubnetAllows(token.Subnet, clientIP) {
		return nil, ErrTokenSubnetViolation
	}
	// High #5: enforce per-key quota. A limited key (UnlimitedQuota=false)
	// whose RemainQuota has been exhausted is rejected so a leaked key
	// cannot draw indefinitely. Previously these fields were persisted but
	// never checked, making the "limited quota" UI promise a no-op.
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		return nil, ErrTokenExhausted
	}
	return token, nil
}

// GetAuthSnapshot validates the token and returns the authorization view
// relay-gateway consumes. clientIP is forwarded to ValidateToken for the
// optional Subnet CIDR check (review M1).
func (uc *IdentityUsecase) GetAuthSnapshot(ctx context.Context, key, clientIP string) (*AuthSnapshot, error) {
	token, err := uc.ValidateToken(ctx, key, clientIP)
	if err != nil {
		return nil, err
	}
	user, err := uc.repo.FindUserByID(ctx, token.UserID)
	if err != nil {
		return nil, err
	}
	if user.Status != UserStatusEnabled {
		return nil, ErrUserDisabled
	}
	return &AuthSnapshot{
		UserID:        user.ID,
		TokenID:       token.ID,
		TokenName:     token.Name,
		Group:         user.Group,
		AllowedModels: append([]string(nil), token.Models...),
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

func (uc *IdentityUsecase) ValidateSessionToken(ctx context.Context, tokenString string) (*User, error) {
	tokenString = strings.TrimSpace(strings.TrimPrefix(tokenString, "Bearer "))
	if tokenString == "" {
		return nil, ErrInvalidToken
	}
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Name}),
		jwt.WithIssuer(uc.sessionIssuer),
		jwt.WithAudience("micro-one-api-web"),
	)
	token, err := parser.ParseWithClaims(tokenString, &UserSessionClaims{}, func(token *jwt.Token) (interface{}, error) {
		return uc.sessionSecret, nil
	})
	if err != nil {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*UserSessionClaims)
	if !ok || !token.Valid || claims.TokenType != "user_session" || claims.UserID <= 0 {
		return nil, ErrInvalidToken
	}
	user, err := uc.repo.FindUserByID(ctx, claims.UserID)
	if err != nil {
		return nil, err
	}
	if user.Status != UserStatusEnabled {
		return nil, ErrUserDisabled
	}
	// M6: reject sessions issued before the most recent password change /
	// forced logout. A non-zero PasswordChangedAt acts as a token epoch:
	// any session token whose embedded PwdEpoch is strictly older is
	// considered revoked. A zero PasswordChangedAt (migration / never set)
	// imposes no constraint so existing sessions keep working.
	if user.PasswordChangedAt > 0 && claims.PwdEpoch < user.PasswordChangedAt {
		return nil, ErrSessionRevoked
	}
	return user, nil
}

func (uc *IdentityUsecase) GetSessionSnapshot(ctx context.Context, token string) (*AuthSnapshot, error) {
	user, err := uc.ValidateSessionToken(ctx, token)
	if err != nil {
		return nil, err
	}
	return &AuthSnapshot{
		UserID:       user.ID,
		TokenID:      0,
		TokenName:    "web-session",
		Group:        user.Group,
		UserEnabled:  true,
		TokenEnabled: true,
	}, nil
}

func (uc *IdentityUsecase) GetUser(ctx context.Context, userID int64) (*User, error) {
	return uc.repo.FindUserByID(ctx, userID)
}

// checkLoginRateLimit checks if the given key is rate-limited due to too
// many failed attempts. The key is composed by loginRateKey (username+IP),
// so both per-username and per-IP spraying are bounded (review L2).
func (uc *IdentityUsecase) checkLoginRateLimit(key string) error {
	uc.loginMutex.Lock()
	defer uc.loginMutex.Unlock()

	attempt, exists := uc.loginLimiter[key]
	if !exists {
		return nil
	}

	// Clean up expired entries
	if uc.now().Sub(attempt.lastSeen) > loginLockoutTime {
		delete(uc.loginLimiter, key)
		return nil
	}

	if attempt.count >= maxLoginAttempts {
		return fmt.Errorf("too many failed login attempts, try again later")
	}

	return nil
}

// recordLoginFailure increments the failed login attempt counter for the key.
// To keep loginLimiter bounded (review L2), a single sweep of expired entries
// runs every loginSweepInterval; this prevents an attacker spamming unique
// usernames from growing the map without bound.
func (uc *IdentityUsecase) recordLoginFailure(key string) {
	uc.loginMutex.Lock()
	defer uc.loginMutex.Unlock()

	// Opportunistic sweep: bounded growth so unique-username spraying cannot
	// exhaust memory (the dedicated Cleanup loop also runs periodically).
	if len(uc.loginLimiter) > loginLimiterCap {
		uc.sweepLoginLimiterLocked()
	}

	attempt, exists := uc.loginLimiter[key]
	if !exists {
		uc.loginLimiter[key] = &loginAttempt{count: 1, lastSeen: uc.now()}
		return
	}

	// Reset if lockout period has passed
	if uc.now().Sub(attempt.lastSeen) > loginLockoutTime {
		uc.loginLimiter[key] = &loginAttempt{count: 1, lastSeen: uc.now()}
		return
	}

	attempt.count++
	attempt.lastSeen = uc.now()
}

// clearLoginAttempts removes rate limit state for a successful login.
func (uc *IdentityUsecase) clearLoginAttempts(key string) {
	uc.loginMutex.Lock()
	defer uc.loginMutex.Unlock()
	delete(uc.loginLimiter, key)
}

// sweepLoginLimiterLocked drops all entries whose lockout window has elapsed.
// Caller must hold loginMutex.
func (uc *IdentityUsecase) sweepLoginLimiterLocked() {
	now := uc.now()
	for k, a := range uc.loginLimiter {
		if now.Sub(a.lastSeen) > loginLockoutTime {
			delete(uc.loginLimiter, k)
		}
	}
}

// Login authenticates a user and returns a session token. clientIP is the
// caller's best-effort remote IP; it is folded into the rate-limit key so a
// single attacker cannot password-spray across many usernames from one IP
// (review L2) and per-username spraying from many IPs is still capped.
func (uc *IdentityUsecase) Login(ctx context.Context, username, password, clientIP string) (*User, string, error) {
	if username == "" || password == "" {
		return nil, "", ErrInvalidPassword
	}

	// Medium #9: check BOTH the per-IP and per-username rate-limit buckets.
	// A single IP spraying many usernames and a single username being stuffed
	// from many IPs are each independently capped at maxLoginAttempts.
	ipKey, userKey := loginRateKeys(username, clientIP)
	if err := uc.checkLoginRateLimit(ipKey); err != nil {
		uc.auditor.LogUserLogin(ctx, 0, username, clientIP, false)
		return nil, "", err
	}
	if err := uc.checkLoginRateLimit(userKey); err != nil {
		uc.auditor.LogUserLogin(ctx, 0, username, clientIP, false)
		return nil, "", err
	}

	// recordBothFailures records the failure in both buckets.
	recordBothFailures := func() {
		if ipKey != "" {
			uc.recordLoginFailure(ipKey)
		}
		uc.recordLoginFailure(userKey)
	}

	user, err := uc.repo.FindUserByUsername(ctx, username)
	if err != nil || user == nil {
		// L2: unknown users run a dummy bcrypt compare so the response time
		// does not reveal whether the username exists (timing oracle).
		uc.dummyBcryptCompare()
		recordBothFailures()
		uc.auditor.LogUserLogin(ctx, 0, username, clientIP, false)
		return nil, "", ErrInvalidPassword
	}
	if user.Status != UserStatusEnabled {
		recordBothFailures()
		uc.auditor.LogUserLogin(ctx, user.ID, username, clientIP, false)
		return nil, "", ErrUserDisabled
	}
	if user.PasswordHash == "" {
		recordBothFailures()
		uc.auditor.LogUserLogin(ctx, user.ID, username, clientIP, false)
		return nil, "", ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		recordBothFailures()
		uc.auditor.LogUserLogin(ctx, user.ID, username, clientIP, false)
		return nil, "", ErrInvalidPassword
	}

	// Clear both buckets on success.
	if ipKey != "" {
		uc.clearLoginAttempts(ipKey)
	}
	uc.clearLoginAttempts(userKey)

	token, err := uc.generateSessionToken(user)
	if err != nil {
		return nil, "", err
	}
	uc.auditor.LogUserLogin(ctx, user.ID, username, clientIP, true)
	return user, token, nil
}

func (uc *IdentityUsecase) Register(ctx context.Context, username, password, email, group string) (*User, error) {
	return uc.RegisterWithAffCode(ctx, username, password, email, group, "")
}

func (uc *IdentityUsecase) RegisterWithAffCode(ctx context.Context, username, password, email, group, affCode string) (*User, error) {
	existing, _ := uc.repo.FindUserByUsername(ctx, username)
	if existing != nil {
		return nil, ErrUserExists
	}
	var inviter *User
	if strings.TrimSpace(affCode) != "" {
		found, err := uc.repo.FindUserByAffCode(ctx, strings.TrimSpace(affCode))
		if err != nil {
			return nil, fmt.Errorf("invalid aff code")
		}
		inviter = found
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	newAffCode, err := uc.generateUniqueAffCode(ctx)
	if err != nil {
		return nil, err
	}
	user := &User{
		Username:     username,
		DisplayName:  username,
		Email:        email,
		Group:        group,
		Status:       UserStatusEnabled,
		PasswordHash: string(hash),
		AffCode:      newAffCode,
	}
	if inviter != nil {
		user.InviterID = inviter.ID
	}
	if err := uc.repo.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (uc *IdentityUsecase) GetOrCreateAffCode(ctx context.Context, userID int64) (string, error) {
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(user.AffCode) != "" {
		return user.AffCode, nil
	}
	code, err := uc.generateUniqueAffCode(ctx)
	if err != nil {
		return "", err
	}
	user.AffCode = code
	if err := uc.repo.UpdateUser(ctx, user); err != nil {
		return "", err
	}
	return code, nil
}

func (uc *IdentityUsecase) generateUniqueAffCode(ctx context.Context) (string, error) {
	for i := 0; i < 5; i++ {
		code := uc.generateAffCode()
		if _, err := uc.repo.FindUserByAffCode(ctx, code); errors.Is(err, ErrUserNotFound) {
			return code, nil
		}
	}
	return "", fmt.Errorf("failed to generate unique aff code")
}

func (uc *IdentityUsecase) generateAffCode() string {
	const letters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
			continue
		}
		b[i] = letters[n.Int64()]
	}
	return string(b)
}

type CreateAccessTokenOptions struct {
	RemainQuota    int64
	UnlimitedQuota bool
	Subnet         string
}

type UpdateAccessTokenOptions struct {
	Name           string
	Models         []string
	ExpireAt       int64
	Status         int32
	RemainQuota    int64
	UnlimitedQuota bool
	Subnet         string
}

func (uc *IdentityUsecase) CreateAccessToken(ctx context.Context, userID int64, name string, models []string, expireAt int64, opts ...CreateAccessTokenOptions) (*Token, error) {
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.Status != UserStatusEnabled {
		return nil, ErrUserDisabled
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrTokenNameRequired
	}
	options := CreateAccessTokenOptions{RemainQuota: uc.defaultQuota, UnlimitedQuota: true}
	if len(opts) > 0 {
		options = opts[0]
		if options.RemainQuota == 0 && options.UnlimitedQuota {
			// Caller did not pin a finite quota; keep the default allowance.
			options.RemainQuota = uc.defaultQuota
		}
		// H2: respect the caller's UnlimitedQuota/RemainQuota rather than
		// silently forcing unlimited. Previously both create and update
		// overwrote unlimited_quota=true, defeating the per-key quota UI.
		// Guard against a degenerate "finite but zero" token: a limited key
		// with RemainQuota <= 0 is born exhausted and can never be used,
		// because ValidateToken rejects it before the first request. Refuse
		// the creation explicitly instead of persisting a dead key.
		if !options.UnlimitedQuota && options.RemainQuota <= 0 {
			return nil, ErrTokenQuotaInvalid
		}
	}
	now := uc.now().Unix()
	plaintextKey := uc.generateToken()
	token := &Token{
		UserID:         userID,
		Name:           name,
		Key:            plaintextKey,
		KeyHash:        HashTokenKey(plaintextKey),
		Status:         TokenStatusEnabled,
		ExpiredAt:      expireAt,
		RemainQuota:    options.RemainQuota,
		UnlimitedQuota: options.UnlimitedQuota,
		Subnet:         options.Subnet,
		Models:         models,
		CreatedAt:      now,
		AccessedAt:     now,
	}
	if err := uc.repo.CreateToken(ctx, token); err != nil {
		return nil, err
	}
	return token, nil
}

func (uc *IdentityUsecase) ListAccessTokens(ctx context.Context, userID int64, page, pageSize int32, keyword string) ([]*Token, int64, error) {
	if _, err := uc.repo.FindUserByID(ctx, userID); err != nil {
		return nil, 0, err
	}
	// L8: return the repo's authoritative total instead of the filtered page
	// length. Both the DB and memory repos already exclude empty-name tokens
	// from their count, so the page length was always <= pageSize and broke
	// pagination past the first page.
	tokens, total, err := uc.repo.ListTokens(ctx, userID, page, pageSize, keyword)
	if err != nil {
		return nil, 0, err
	}
	return tokens, total, nil
}

func (uc *IdentityUsecase) GetAccessToken(ctx context.Context, userID, tokenID int64) (*Token, error) {
	token, err := uc.repo.FindTokenByID(ctx, userID, tokenID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token.Name) == "" {
		return nil, ErrTokenNotFound
	}
	return token, nil
}

func (uc *IdentityUsecase) UpdateAccessToken(ctx context.Context, userID, tokenID int64, name string, models []string, expireAt int64, status int32, remainQuota int64, unlimitedQuota bool) (*Token, error) {
	return uc.UpdateAccessTokenWithOptions(ctx, userID, tokenID, UpdateAccessTokenOptions{
		Name:           name,
		Models:         models,
		ExpireAt:       expireAt,
		Status:         status,
		RemainQuota:    remainQuota,
		UnlimitedQuota: unlimitedQuota,
	})
}

func (uc *IdentityUsecase) UpdateAccessTokenWithOptions(ctx context.Context, userID, tokenID int64, opts UpdateAccessTokenOptions) (*Token, error) {
	token, err := uc.repo.FindTokenByID(ctx, userID, tokenID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token.Name) == "" {
		return nil, ErrTokenNotFound
	}
	if opts.Name != "" {
		token.Name = opts.Name
	}
	if opts.Models != nil {
		token.Models = opts.Models
	}
	if opts.ExpireAt != 0 {
		token.ExpiredAt = opts.ExpireAt
	}
	if opts.Status != 0 {
		token.Status = opts.Status
	}
	if opts.RemainQuota >= 0 {
		token.RemainQuota = opts.RemainQuota
	}
	// H2: respect the caller's unlimited_quota flag. Previously this was
	// unconditionally set to true, discarding a configured finite quota.
	token.UnlimitedQuota = opts.UnlimitedQuota
	token.Subnet = opts.Subnet
	if err := uc.repo.UpdateToken(ctx, token); err != nil {
		return nil, err
	}
	return token, nil
}

// ConsumeTokenQuota deducts `amount` from the token's RemainQuota and adds it
// to UsedQuota. Unlimited keys are a no-op. When the remaining quota reaches
// zero the token is marked TokenStatusExhausted so subsequent ValidateToken
// calls reject it. This is the per-key enforcement path that was previously
// missing (review High #5): the relay gateway calls this after billing settles
// so a limited key cannot exceed its configured quota even if the user wallet
// still has balance.
func (uc *IdentityUsecase) ConsumeTokenQuota(ctx context.Context, userID, tokenID, amount int64) (int64, error) {
	if amount <= 0 {
		return 0, nil
	}
	token, err := uc.repo.FindTokenByID(ctx, userID, tokenID)
	if err != nil {
		return 0, err
	}
	if token.UnlimitedQuota {
		return -1, nil
	}
	remaining, err := uc.repo.ConsumeTokenQuota(ctx, userID, tokenID, amount)
	if err != nil {
		return 0, err
	}
	return remaining, nil
}

func (uc *IdentityUsecase) DeleteAccessToken(ctx context.Context, userID, tokenID int64) error {
	token, err := uc.repo.FindTokenByID(ctx, userID, tokenID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token.Name) == "" {
		return ErrTokenNotFound
	}
	return uc.repo.DeleteToken(ctx, userID, tokenID)
}

func (uc *IdentityUsecase) DeleteAccessTokenForAuth(ctx context.Context, auth *AuthSnapshot, tokenID int64) error {
	if auth == nil {
		return ErrInvalidToken
	}
	if auth.TokenID == tokenID {
		return ErrTokenInUse
	}
	return uc.DeleteAccessToken(ctx, auth.UserID, tokenID)
}

func (uc *IdentityUsecase) ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*User, int64, error) {
	return uc.repo.ListUsers(ctx, page, pageSize, keyword, group, status)
}

func (uc *IdentityUsecase) CreateUser(ctx context.Context, username, displayName, email, password, group string, quota int64) (*User, error) {
	existing, _ := uc.repo.FindUserByUsername(ctx, username)
	if existing != nil {
		return nil, ErrUserExists
	}
	user := &User{
		Username:    username,
		DisplayName: displayName,
		Email:       email,
		Group:       group,
		Status:      UserStatusEnabled,
	}
	if password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		user.PasswordHash = string(hash)
	}
	if err := uc.repo.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (uc *IdentityUsecase) UpdateUser(ctx context.Context, userID int64, displayName, email, group string, status int32) error {
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	if displayName != "" {
		user.DisplayName = displayName
	}
	if email != "" {
		user.Email = email
	}
	if group != "" {
		user.Group = group
	}
	user.Status = status
	return uc.repo.UpdateUser(ctx, user)
}

var (
	ErrInvalidRole           = errors.New("invalid role")
	ErrCannotChangeRootRole  = errors.New("cannot change root user role")
	ErrOperatorNotAdmin      = errors.New("operator is not an admin")
	ErrCannotChangeSelf      = errors.New("operator cannot change own role")
	ErrCannotOutrankOperator = errors.New("target role would meet or exceed operator role")
)

// SetRole updates a user's role. The new role must be one of the named
// constants (Guest/Common/Admin); promoting to root via this path is not
// allowed — the root account is only created by bootstrap. Demoting an
// existing root user is also refused so an admin cannot accidentally lock
// every operator out of the system.
//
// When operator is non-nil it MUST already be loaded from the database
// (transport layer decides where it came from — JWT, header, etc.). The
// following checks apply:
//   - operator must be admin or higher
//   - operator cannot change its own role
//   - operator must strictly outrank the target user (you cannot demote
//     someone at or above your own rank)
//   - new role must be strictly below operator's role (you cannot promote
//     someone to your own level or above)
//
// Passing operator == nil represents a system-level call (e.g. bootstrap,
// admin-reset CLI) and skips operator-vs-target comparisons. The
// root-protection and invalid-role checks still apply.
func (uc *IdentityUsecase) SetRole(ctx context.Context, operator *User, userID int64, role int32) (*User, error) {
	switch role {
	case RoleGuestUser, RoleCommonUser, RoleAdminUser:
	default:
		return nil, ErrInvalidRole
	}
	if operator != nil {
		if !operator.IsAdmin() {
			return nil, ErrOperatorNotAdmin
		}
		if operator.ID == userID {
			return nil, ErrCannotChangeSelf
		}
		if role >= operator.Role {
			return nil, ErrCannotOutrankOperator
		}
	}
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.IsRoot() {
		return nil, ErrCannotChangeRootRole
	}
	if operator != nil && user.Role >= operator.Role {
		return nil, ErrCannotOutrankOperator
	}
	if user.Role == role {
		return user, nil
	}
	user.Role = role
	if err := uc.repo.UpdateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// ErrCurrentPasswordRequired is returned when UpdateSelf attempts a
// sensitive change (username or password) without confirming the current
// password. This stops a stolen session token from locking out the real
// user by changing the password.
var ErrCurrentPasswordRequired = errors.New("current password is required to change username or password")

func (uc *IdentityUsecase) UpdateSelf(ctx context.Context, userID int64, username, displayName, password, currentPassword string, updateDisplayName bool) error {
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	// M7: changing username or password is a sensitive mutation. Require
	// the current password (verified against the stored bcrypt hash) so a
	// stolen/unattended session cannot lock out the real owner. Display
	// name edits are cosmetic and stay session-gated only.
	sensitiveChange := (username != "" && username != user.Username) || password != ""
	if sensitiveChange {
		if currentPassword == "" {
			return ErrCurrentPasswordRequired
		}
		if user.PasswordHash == "" {
			return ErrInvalidPassword
		}
		if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(currentPassword)); err != nil {
			return ErrInvalidPassword
		}
	}
	if username != "" && username != user.Username {
		existing, err := uc.repo.FindUserByUsername(ctx, username)
		if err == nil && existing != nil && existing.ID != userID {
			return ErrUserExists
		}
		if err != nil && !errors.Is(err, ErrUserNotFound) {
			return err
		}
		user.Username = username
	}
	if updateDisplayName {
		user.DisplayName = displayName
	}
	if password != "" {
		if len(password) < 8 {
			return fmt.Errorf("password must be at least 8 characters")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		user.PasswordHash = string(hash)
		// M6: a password change revokes all previously-issued sessions by
		// advancing the stored epoch past the PwdEpoch stamped in any
		// outstanding JWT.
		user.PasswordChangedAt = nextPasswordEpoch(user.PasswordChangedAt, uc.now())
	}
	return uc.repo.UpdateUser(ctx, user)
}

func (uc *IdentityUsecase) UpdateSelfEmail(ctx context.Context, userID int64, email string) error {
	if strings.TrimSpace(email) == "" {
		return fmt.Errorf("email is required")
	}
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	user.Email = email
	return uc.repo.UpdateUser(ctx, user)
}

func (uc *IdentityUsecase) DeleteUser(ctx context.Context, userID int64) error {
	return uc.repo.DeleteUser(ctx, userID)
}

func (uc *IdentityUsecase) ResetPasswordByEmail(ctx context.Context, email, password string) error {
	if email == "" || password == "" {
		return ErrInvalidPassword
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	user, err := uc.repo.FindUserByEmail(ctx, email)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user.PasswordHash = string(hash)
	// M6: revoke all prior sessions for this user (a reset should not leave
	// pre-reset tokens valid).
	user.PasswordChangedAt = nextPasswordEpoch(user.PasswordChangedAt, uc.now())
	return uc.repo.UpdateUser(ctx, user)
}

// InvalidateAllSessions revokes every outstanding session token for the user
// by advancing the password epoch past the PwdEpoch of any currently-issued
// JWT (review M6). It is the server-side primitive behind logout-all /
// forced-sign-out: unlike per-token JTI blacklists it needs no distributed
// revocation store.
func (uc *IdentityUsecase) InvalidateAllSessions(ctx context.Context, userID int64) error {
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return err
	}
	user.PasswordChangedAt = nextPasswordEpoch(user.PasswordChangedAt, uc.now())
	return uc.repo.UpdateUser(ctx, user)
}

func (uc *IdentityUsecase) generateToken() string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 32)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		b[i] = letters[n.Int64()]
	}
	return string(b)
}

// tokenHashSecret returns the HMAC key used to hash access-token keys. It
// prefers the dedicated TOKEN_HASH_KEY, falls back to JWT_SECRET_KEY (so
// deployments that already rotate a JWT secret get token-key rotation for
// free), and finally a fixed dev default so unit tests work without env.
// Rotating the secret invalidates every existing token (keys can no longer
// be looked up), which is the desired break-glass behaviour.
func tokenHashSecret() []byte {
	if v := os.Getenv("TOKEN_HASH_KEY"); v != "" {
		return []byte(v)
	}
	if v := os.Getenv("JWT_SECRET_KEY"); v != "" {
		return []byte(v)
	}
	return []byte("micro-one-api-token-hash-default")
}

// HashTokenKey returns the lowercase hex HMAC-SHA256 of a plaintext token
// key. HMAC is deterministic for a given key+secret, so ValidateToken can
// hash the incoming bearer token and look it up by key_hash in O(1).
func HashTokenKey(key string) string {
	mac := hmac.New(sha256.New, tokenHashSecret())
	mac.Write([]byte(key))
	return hex.EncodeToString(mac.Sum(nil))
}

// TokenDisplayPrefix returns a short, non-secret slice of a plaintext key
// (first 8 + last 4 chars) that preserves the existing masked-key display
// ("abcd****wxyz") without storing enough to authenticate.
func TokenDisplayPrefix(key string) string {
	if len(key) <= 12 {
		return key
	}
	return key[:8] + key[len(key)-4:]
}

func (uc *IdentityUsecase) generateSessionToken(user *User) (string, error) {
	if user == nil || user.ID <= 0 {
		return "", ErrUserNotFound
	}
	now := uc.now()
	claims := UserSessionClaims{
		UserID:    user.ID,
		Username:  user.Username,
		Role:      user.Role,
		TokenType: "user_session",
		// Stamp the current password epoch so the validator can reject this
		// session once the password changes / a forced logout bumps the
		// epoch (review M6).
		PwdEpoch: user.PasswordChangedAt,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        uc.generateToken(),
			Issuer:    uc.sessionIssuer,
			Subject:   strconv.FormatInt(user.ID, 10),
			Audience:  []string{"micro-one-api-web"},
			ExpiresAt: jwt.NewNumericDate(now.Add(uc.sessionDuration)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(uc.sessionSecret)
}

// OAuthLogin finds or creates a user by OAuth provider identity, then returns a token.
func (uc *IdentityUsecase) OAuthLogin(ctx context.Context, provider, oauthID, username, email, displayName string) (*User, string, error) {
	identity, err := uc.repo.FindOAuthIdentity(ctx, provider, oauthID)
	if err != nil && !errors.Is(err, ErrOAuthUserNotFound) {
		return nil, "", err
	}
	var user *User
	if identity != nil {
		user, err = uc.repo.FindUserByID(ctx, identity.UserID)
		if err != nil {
			return nil, "", err
		}
	}
	if user == nil {
		user, err = uc.repo.FindUserByOAuth(ctx, provider, oauthID)
	}
	if err != nil && !errors.Is(err, ErrOAuthUserNotFound) {
		return nil, "", err
	}

	if user == nil {
		// Create new OAuth user
		if displayName == "" {
			displayName = username
		}
		user = &User{
			Username:      username,
			DisplayName:   displayName,
			Email:         email,
			Group:         "default",
			Status:        UserStatusEnabled,
			OAuthProvider: provider,
			OAuthID:       oauthID,
		}
		if err := uc.repo.CreateUser(ctx, user); err != nil {
			return nil, "", err
		}
		_, identityErr := uc.repo.FindOAuthIdentity(ctx, provider, oauthID)
		if errors.Is(identityErr, ErrOAuthUserNotFound) {
			now := uc.now().Unix()
			if err := uc.repo.CreateOAuthIdentity(ctx, &OAuthIdentity{
				UserID:     user.ID,
				Provider:   provider,
				ProviderID: oauthID,
				CreatedAt:  now,
				UpdatedAt:  now,
			}); err != nil {
				return nil, "", err
			}
		} else if identityErr != nil {
			return nil, "", identityErr
		}
	}

	if user.Status != UserStatusEnabled {
		return nil, "", ErrUserDisabled
	}

	token, err := uc.generateSessionToken(user)
	if err != nil {
		return nil, "", err
	}

	return user, token, nil
}

func (uc *IdentityUsecase) BindOAuthIdentity(ctx context.Context, userID int64, provider, oauthID string) (*User, error) {
	provider = strings.TrimSpace(provider)
	oauthID = strings.TrimSpace(oauthID)
	if provider == "" || oauthID == "" {
		return nil, ErrOAuthUserNotFound
	}
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.Status != UserStatusEnabled {
		return nil, ErrUserDisabled
	}
	boundIdentity, err := uc.repo.FindOAuthIdentity(ctx, provider, oauthID)
	if err != nil && !errors.Is(err, ErrOAuthUserNotFound) {
		return nil, err
	}
	if boundIdentity != nil && boundIdentity.UserID != userID {
		return nil, ErrOAuthAlreadyBound
	}
	userProviderIdentity, err := uc.repo.FindOAuthIdentityByUserProvider(ctx, userID, provider)
	if err != nil && !errors.Is(err, ErrOAuthUserNotFound) {
		return nil, err
	}
	if userProviderIdentity != nil && userProviderIdentity.ProviderID != oauthID {
		return nil, ErrOAuthAlreadyBound
	}
	legacyUser, err := uc.repo.FindUserByOAuth(ctx, provider, oauthID)
	if err != nil && !errors.Is(err, ErrOAuthUserNotFound) {
		return nil, err
	}
	if legacyUser != nil && legacyUser.ID != userID {
		return nil, ErrOAuthAlreadyBound
	}
	if userProviderIdentity == nil {
		now := uc.now().Unix()
		if err := uc.repo.CreateOAuthIdentity(ctx, &OAuthIdentity{
			UserID:     userID,
			Provider:   provider,
			ProviderID: oauthID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}); err != nil {
			return nil, err
		}
	}
	return user, nil
}

func SplitCSVPtr(input *string) []string {
	if input == nil {
		return nil
	}
	return splitCSV(*input)
}

func splitCSV(input string) []string {
	raw := strings.Split(input, ",")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func nextPasswordEpoch(current int64, now time.Time) int64 {
	next := now.UnixMilli()
	if next <= current {
		return current + 1
	}
	return next
}

// loginRateKeys returns the per-IP and per-username rate-limit keys for a
// login attempt. Tracking both independently (review Medium #9) means:
//   - a single attacker IP is capped at maxLoginAttempts across ALL usernames
//     (password spraying from one IP);
//   - a single username is capped at maxLoginAttempts across ALL IPs
//     (credential stuffing from a botnet).
//
// When no IP is available only the username key is used (preserving the
// previous behavior so login still works behind proxies that strip it).
func loginRateKeys(username, clientIP string) (ipKey, userKey string) {
	if clientIP != "" {
		ipKey = "ip:" + clientIP
	}
	userKey = "user:" + username
	return ipKey, userKey
}

// dummyBcryptCompare performs a throwaway bcrypt comparison with a fixed hash.
// Unknown usernames hit this path so their response time matches a real
// failed login, closing the username-enumeration timing oracle (review L2).
var dummyLoginHash = func() string {
	h, _ := bcrypt.GenerateFromPassword([]byte("timing-equalization-fixed-secret"), bcrypt.DefaultCost)
	return string(h)
}()

func (uc *IdentityUsecase) dummyBcryptCompare() {
	_ = bcrypt.CompareHashAndPassword([]byte(dummyLoginHash), []byte(""))
}

// tokenSubnetAllows reports whether clientIP is permitted by the token's
// optional Subnet CIDR restriction (review M1). An empty subnet disables the
// restriction (backwards compatible). An empty clientIP is allowed only when
// no restriction is set; when a restriction exists but the caller cannot
// supply an IP, the token is rejected (fail-closed) so the field is never a
// silent no-op.
func tokenSubnetAllows(subnet, clientIP string) bool {
	subnet = strings.TrimSpace(subnet)
	if subnet == "" {
		return true
	}
	clientIP = strings.TrimSpace(clientIP)
	if clientIP == "" {
		return false
	}
	// Accept host-qualified CIDRs (e.g. "10.0.0.5/24") by extracting the network.
	_, ipNet, err := net.ParseCIDR(strings.TrimSpace(subnet))
	if err != nil {
		// Not a valid CIDR — do not silently bypass the configured restriction.
		return false
	}
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}
	return ipNet.Contains(ip)
}

// SweepLoginLimiter is the periodic cleanup entry point that bounds the
// in-memory login limiter. It is safe to call from a background ticker.
func (uc *IdentityUsecase) SweepLoginLimiter() {
	uc.loginMutex.Lock()
	defer uc.loginMutex.Unlock()
	uc.sweepLoginLimiterLocked()
}
