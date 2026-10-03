package biz

import (
	"context"
	"micro-one-api/domain/authorization"
)

// SystemOption is the domain object for a system configuration entry.
type SystemOption struct {
	Key   string
	Value string
}

// SystemOptionsRepo is the repository interface for system options
// persistence. It is declared in biz (the inversion seam) and implemented
// by data.
type SystemOptionsRepo interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// SystemOptionsUsecase wraps the repo with domain-level operations.
// Business rules (legacy aliases, default merging) live here so the
// service layer stays a pass-through DTO↔DO converter.
type SystemOptionsUsecase struct {
	authorization authorization.Resolver
	repo          SystemOptionsRepo
}

// NewSystemOptionsUsecase creates a new usecase.
func NewSystemOptionsUsecase(repo SystemOptionsRepo) *SystemOptionsUsecase {
	return &SystemOptionsUsecase{repo: repo}
}

// Repo exposes the underlying repo so callers that need raw access
// (e.g. nil-checks for "storage not configured") can do so without
// the service holding a second copy of the interface.
func (uc *SystemOptionsUsecase) Repo() SystemOptionsRepo {
	return uc.repo
}

// Get returns the value for a key.
func (uc *SystemOptionsUsecase) Get(ctx context.Context, key string) (string, error) {
	if uc == nil || uc.repo == nil {
		return "", nil
	}
	var err error
	ctx, err = uc.prepareRead(ctx, key)
	if err != nil {
		return "", err
	}
	raw, err := uc.repo.Get(ctx, key)
	if authorization.External(ctx) && SensitiveSystemOption(key) {
		raw = ""
	}
	return raw, err
}

// Set upserts a key-value pair.
func (uc *SystemOptionsUsecase) Set(ctx context.Context, key, value string) error {
	if uc == nil || uc.repo == nil {
		return nil
	}
	var err error
	ctx, err = uc.prepareWrite(ctx, key)
	if err != nil {
		return err
	}
	return uc.repo.Set(ctx, key, value)
}
