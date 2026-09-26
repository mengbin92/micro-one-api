package credential

import (
	"context"
	"errors"
	"time"

	"micro-one-api/platform/metrics"
)

func (b *baseTokenProvider) RequiresAuthoritativeLookup() bool { return b.coordinator != nil }

// SetRefreshCoordinator must run before the provider or its sweep starts.
// Storage remains the shared credential authority; Redis holds no tokens.
func (b *baseTokenProvider) SetRefreshCoordinator(coordinator RefreshCoordinator) error {
	if coordinator == nil {
		return ErrNotConfigured
	}
	if _, ok := b.lookup.(RefreshClaimer); !ok {
		return ErrNotConfigured
	}
	b.coordinator = coordinator
	return nil
}

func (b *baseTokenProvider) resolveDistributed(ctx context.Context, id int64, force bool) (token string, err error) {
	defer func() {
		result := "success"
		switch {
		case errors.Is(err, context.Canceled):
			result = "canceled"
		case errors.Is(err, context.DeadlineExceeded):
			result = "timeout"
		case errors.Is(err, ErrRefreshBusy):
			result = "busy"
		case errors.Is(err, ErrRefreshUncertain):
			result = "uncertain"
		case errors.Is(err, ErrCredentialPersistencePending):
			result = "pending"
		case errors.Is(err, ErrCredentialConflict):
			result = "conflict"
		case err != nil:
			result = "unavailable"
		}
		metrics.CredentialCoordination.WithLabelValues(b.platform, result).Inc()
	}()
	if force {
		return b.resolveDistributedOnce(ctx, id, force)
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	for {
		token, err = b.resolveDistributedOnce(ctx, id, false)
		if !errors.Is(err, ErrRefreshBusy) {
			return token, err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *baseTokenProvider) resolveDistributedOnce(ctx context.Context, id int64, force bool) (string, error) {
	mu := b.lockFor(id)
	if err := mu.lockContext(ctx); err != nil {
		return "", err
	}
	defer mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	creds, err := b.lookup.Lookup(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrCoordinationUnavailable
	}
	if creds == nil {
		return "", ErrAccountNotFound
	}
	// Validate pending memory against the durable revision on every read, so
	// a manual authorization cannot be hidden by another replica's warm cache.
	if cached, ok := b.cache.getCreds(id); ok {
		if _, dirty := b.cache.pending()[id]; dirty {
			matchingClaim := creds.Revision == cached.Revision && creds.RefreshPending
			matchingReplay := creds.Revision == cached.Revision+1 && !creds.RefreshPending &&
				creds.AccessToken == cached.AccessToken && creds.RefreshToken == cached.RefreshToken
			if matchingClaim || matchingReplay {
				if err := b.persist(ctx, id); err != nil {
					if errors.Is(err, ErrCredentialConflict) {
						return "", err
					}
					// Only this process owns the rotated credentials. Do not rotate
					// again before they are durable, even for a forced refresh.
					if !force && !staleExpiry(cached.ExpiresAt) {
						return cached.AccessToken, nil
					}
					return "", ErrCredentialPersistencePending
				}
				creds, _ = b.cache.getCreds(id)
				if !staleExpiry(creds.ExpiresAt) {
					return creds.AccessToken, nil
				}
			} else {
				b.cache.discard(id)
				b.reportPending()
			}
		}
	}
	if !creds.RefreshPending && !force && creds.AccessToken != "" && !staleExpiry(creds.ExpiresAt) {
		return creds.AccessToken, nil
	}
	lease, err := b.coordinator.Acquire(ctx, id)
	if err != nil {
		return "", err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		lease.Release(releaseCtx)
	}()
	// Reload after admission. Another replica may have completed a rotation
	// between our initial lookup and acquiring the lease.
	current, err := b.lookup.Lookup(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrCoordinationUnavailable
	}
	if current == nil {
		return "", ErrAccountNotFound
	}
	if current.RefreshPending {
		return "", ErrRefreshUncertain
	}
	if (!force || current.Revision != creds.Revision) && current.AccessToken != "" && !staleExpiry(current.ExpiresAt) {
		return current.AccessToken, nil
	}
	if current.RefreshToken == "" {
		return "", ErrNoRefreshToken
	}
	if err := lease.Check(ctx); err != nil {
		return "", err
	}
	if err := b.lookup.(RefreshClaimer).ClaimRefresh(ctx, id, current); err != nil {
		if errors.Is(err, ErrCredentialConflict) {
			return "", err
		}
		return "", ErrCoordinationUnavailable
	}
	// If this check fails the durable claim deliberately remains uncertain.
	if err := lease.Check(ctx); err != nil {
		return "", err
	}
	refreshURL := current.RefreshURL
	if refreshURL == "" {
		refreshURL = b.defaultRefreshURL
	}
	rotated, err := b.refresher.refresh(ctx, refreshURL, current.RefreshToken)
	if err != nil {
		// Even a timeout may have consumed a one-time refresh token upstream.
		// Retain the claim and do not retry that OAuth attempt.
		return "", ErrRefreshUncertain
	}
	rotated.Revision = current.Revision
	rotated.AccountID = current.AccountID
	rotated.ClientID = current.ClientID
	rotated.RefreshURL = current.RefreshURL
	b.cache.markDirty(id, rotated)
	if err := b.persist(ctx, id); err != nil {
		if errors.Is(err, ErrCredentialConflict) {
			return "", err
		}
		if force {
			return "", ErrCredentialPersistencePending
		}
	}
	return rotated.AccessToken, nil
}
