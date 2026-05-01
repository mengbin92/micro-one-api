package biz

import (
	"context"
	"errors"
	"strings"
	"time"
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
)

type User struct {
	ID          int64
	Username    string
	DisplayName string
	Group       string
	Status      int32
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
