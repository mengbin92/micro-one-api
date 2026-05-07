package biz

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	UserStatusEnabled  int32 = 1
	UserStatusDisabled int32 = 2

	TokenStatusEnabled   int32 = 1
	TokenStatusDisabled  int32 = 2
	TokenStatusExpired   int32 = 3
	TokenStatusExhausted int32 = 4
)

var (
	ErrInvalidToken   = errors.New("invalid token")
	ErrTokenExpired   = errors.New("token expired")
	ErrTokenExhausted = errors.New("token exhausted")
	ErrTokenDisabled  = errors.New("token disabled")
	ErrUserDisabled   = errors.New("user disabled")
	ErrUserNotFound   = errors.New("user not found")
	ErrTokenNotFound  = errors.New("token not found")
	ErrUserExists     = errors.New("user already exists")
	ErrInvalidPassword = errors.New("invalid password")
	ErrOAuthUserNotFound = errors.New("oauth user not found")
)

type User struct {
	ID            int64
	Username      string
	DisplayName   string
	Email         string
	Group         string
	Status        int32
	PasswordHash  string
	OAuthProvider string
	OAuthID       string
}

type Token struct {
	ID             int64
	UserID         int64
	Key            string
	Status         int32
	ExpiredAt      int64
	RemainQuota    int64
	UnlimitedQuota bool
	Models         []string
}

// AuthSnapshot is the minimum authorization view returned to relay-gateway.
type AuthSnapshot struct {
	UserID        int64
	TokenID       int64
	Group         string
	AllowedModels []string
	UserEnabled   bool
	TokenEnabled  bool
}

type IdentityRepo interface {
	FindTokenByKey(ctx context.Context, key string) (*Token, error)
	FindUserByID(ctx context.Context, userID int64) (*User, error)
	FindUserByUsername(ctx context.Context, username string) (*User, error)
	FindUserByOAuth(ctx context.Context, provider, oauthID string) (*User, error)
	CreateUser(ctx context.Context, user *User) error
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, userID int64) error
	CreateToken(ctx context.Context, token *Token) error
	ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*User, int64, error)
}

// loginAttempt tracks failed login attempts for rate limiting
type loginAttempt struct {
	count    int
	lastSeen time.Time
}

type IdentityUsecase struct {
	repo         IdentityRepo
	now          func() time.Time
	defaultQuota int64
	loginLimiter map[string]*loginAttempt
	loginMutex   sync.Mutex
}

const (
	maxLoginAttempts = 5
	loginLockoutTime = 5 * time.Minute
)

func NewIdentityUsecase(repo IdentityRepo) *IdentityUsecase {
	defaultQuota := int64(1000000) // 1M tokens
	if v := os.Getenv("DEFAULT_USER_QUOTA"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			defaultQuota = n
		}
	}
	return &IdentityUsecase{
		repo:         repo,
		now:          time.Now,
		defaultQuota: defaultQuota,
		loginLimiter: make(map[string]*loginAttempt),
	}
}

func (uc *IdentityUsecase) ValidateToken(ctx context.Context, key string) (*Token, error) {
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
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		return nil, ErrTokenExhausted
	}
	return token, nil
}

func (uc *IdentityUsecase) GetAuthSnapshot(ctx context.Context, key string) (*AuthSnapshot, error) {
	token, err := uc.ValidateToken(ctx, key)
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
		Group:         user.Group,
		AllowedModels: append([]string(nil), token.Models...),
		UserEnabled:   true,
		TokenEnabled:  true,
	}, nil
}

func (uc *IdentityUsecase) GetUser(ctx context.Context, userID int64) (*User, error) {
	return uc.repo.FindUserByID(ctx, userID)
}

// checkLoginRateLimit checks if a username is rate-limited due to too many failed attempts.
func (uc *IdentityUsecase) checkLoginRateLimit(username string) error {
	uc.loginMutex.Lock()
	defer uc.loginMutex.Unlock()

	attempt, exists := uc.loginLimiter[username]
	if !exists {
		return nil
	}

	// Clean up expired entries
	if uc.now().Sub(attempt.lastSeen) > loginLockoutTime {
		delete(uc.loginLimiter, username)
		return nil
	}

	if attempt.count >= maxLoginAttempts {
		return fmt.Errorf("too many failed login attempts, try again later")
	}

	return nil
}

// recordLoginFailure increments the failed login attempt counter.
func (uc *IdentityUsecase) recordLoginFailure(username string) {
	uc.loginMutex.Lock()
	defer uc.loginMutex.Unlock()

	attempt, exists := uc.loginLimiter[username]
	if !exists {
		uc.loginLimiter[username] = &loginAttempt{count: 1, lastSeen: uc.now()}
		return
	}

	// Reset if lockout period has passed
	if uc.now().Sub(attempt.lastSeen) > loginLockoutTime {
		uc.loginLimiter[username] = &loginAttempt{count: 1, lastSeen: uc.now()}
		return
	}

	attempt.count++
	attempt.lastSeen = uc.now()
}

// clearLoginAttempts removes rate limit state for a successful login.
func (uc *IdentityUsecase) clearLoginAttempts(username string) {
	uc.loginMutex.Lock()
	defer uc.loginMutex.Unlock()
	delete(uc.loginLimiter, username)
}

func (uc *IdentityUsecase) Login(ctx context.Context, username, password string) (*User, string, error) {
	if username == "" || password == "" {
		return nil, "", ErrInvalidPassword
	}

	if err := uc.checkLoginRateLimit(username); err != nil {
		return nil, "", err
	}

	user, err := uc.repo.FindUserByUsername(ctx, username)
	if err != nil {
		uc.recordLoginFailure(username)
		return nil, "", err
	}
	if user.Status != UserStatusEnabled {
		return nil, "", ErrUserDisabled
	}
	if user.PasswordHash == "" {
		uc.recordLoginFailure(username)
		return nil, "", ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		uc.recordLoginFailure(username)
		return nil, "", ErrInvalidPassword
	}

	uc.clearLoginAttempts(username)

	token := uc.generateToken()
	tokenRecord := &Token{
		UserID:      user.ID,
		Key:         token,
		Status:      TokenStatusEnabled,
		RemainQuota: uc.defaultQuota,
		Models:      []string{},
	}
	if err := uc.repo.CreateToken(ctx, tokenRecord); err != nil {
		return nil, "", err
	}
	return user, token, nil
}

func (uc *IdentityUsecase) Register(ctx context.Context, username, password, email, group string) (*User, error) {
	existing, _ := uc.repo.FindUserByUsername(ctx, username)
	if existing != nil {
		return nil, ErrUserExists
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
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
	}
	if err := uc.repo.CreateUser(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

func (uc *IdentityUsecase) CreateAccessToken(ctx context.Context, userID int64, name string, models []string, expireAt int64) (*Token, error) {
	user, err := uc.repo.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user.Status != UserStatusEnabled {
		return nil, ErrUserDisabled
	}
	token := &Token{
		UserID:    userID,
		Key:       uc.generateToken(),
		Status:    TokenStatusEnabled,
		ExpiredAt: expireAt,
		Models:   models,
	}
	if err := uc.repo.CreateToken(ctx, token); err != nil {
		return nil, err
	}
	return token, nil
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

func (uc *IdentityUsecase) DeleteUser(ctx context.Context, userID int64) error {
	return uc.repo.DeleteUser(ctx, userID)
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

// OAuthLogin finds or creates a user by OAuth provider identity, then returns a token.
func (uc *IdentityUsecase) OAuthLogin(ctx context.Context, provider, oauthID, username, email, displayName string) (*User, string, error) {
	// Try to find existing OAuth user
	user, err := uc.repo.FindUserByOAuth(ctx, provider, oauthID)
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
	}

	if user.Status != UserStatusEnabled {
		return nil, "", ErrUserDisabled
	}

	// Generate token
	token := uc.generateToken()
	tokenRecord := &Token{
		UserID:      user.ID,
		Key:         token,
		Status:      TokenStatusEnabled,
		RemainQuota: uc.defaultQuota,
		Models:      []string{},
	}
	if err := uc.repo.CreateToken(ctx, tokenRecord); err != nil {
		return nil, "", err
	}

	return user, token, nil
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
