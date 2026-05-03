package biz

import (
	"context"
	"errors"
	"strings"
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
)

type User struct {
	ID           int64
	Username     string
	DisplayName  string
	Email        string
	Group        string
	Status       int32
	PasswordHash string
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
	CreateUser(ctx context.Context, user *User) error
	UpdateUser(ctx context.Context, user *User) error
	DeleteUser(ctx context.Context, userID int64) error
	CreateToken(ctx context.Context, token *Token) error
	ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*User, int64, error)
}

type IdentityUsecase struct {
	repo IdentityRepo
	now  func() time.Time
}

func NewIdentityUsecase(repo IdentityRepo) *IdentityUsecase {
	return &IdentityUsecase{
		repo: repo,
		now:  time.Now,
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

func (uc *IdentityUsecase) Login(ctx context.Context, username, password string) (*User, string, error) {
	if username == "" || password == "" {
		return nil, "", ErrInvalidPassword
	}
	user, err := uc.repo.FindUserByUsername(ctx, username)
	if err != nil {
		return nil, "", err
	}
	if user.Status != UserStatusEnabled {
		return nil, "", ErrUserDisabled
	}
	if user.PasswordHash == "" {
		return nil, "", ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, "", ErrInvalidPassword
	}
	token := uc.generateToken()
	tokenRecord := &Token{
		UserID:         user.ID,
		Key:            token,
		Status:         TokenStatusEnabled,
		UnlimitedQuota: true,
		Models:         []string{},
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
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
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
