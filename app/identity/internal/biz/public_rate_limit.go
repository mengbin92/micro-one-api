package biz

import (
	"context"
	"time"
)

type PublicRequestLimiter interface {
	AllowPublicRequest(context.Context, string, int, time.Duration) (bool, error)
}

func (uc *IdentityUsecase) AllowPublicRequest(ctx context.Context, key string, limit int, window time.Duration) bool {
	limiter, ok := uc.repo.(PublicRequestLimiter)
	if !ok {
		return false
	}
	allowed, err := limiter.AllowPublicRequest(ctx, key, limit, window)
	return err == nil && allowed
}
