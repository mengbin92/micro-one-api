package data

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"micro-one-api/domain/upstream/credential"
)

const credentialRefreshLeaseTTL = time.Minute

// Redis admits one owner; the database claim fences uncertain OAuth attempts
// across lease expiry, Redis data loss and process restarts.
type redisCredentialCoordinator struct {
	client *redis.Client
}

func NewCredentialRefreshCoordinator(client *redis.Client) credential.RefreshCoordinator {
	if client == nil {
		return nil
	}
	return &redisCredentialCoordinator{client: client}
}

func (c *redisCredentialCoordinator) Acquire(ctx context.Context, id int64) (credential.RefreshLease, error) {
	key := "credential:refresh:" + strconv.FormatInt(id, 10)
	token := uuid.NewString()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ok, err := c.client.SetNX(ctx, key, token, credentialRefreshLeaseTTL).Result()
	if err != nil {
		return nil, credential.ErrCoordinationUnavailable
	}
	if !ok {
		return nil, credential.ErrRefreshBusy
	}
	return &redisCredentialLease{client: c.client, key: key, token: token}, nil
}

type redisCredentialLease struct {
	client     *redis.Client
	key, token string
}

func (l *redisCredentialLease) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	token, err := l.client.Get(ctx, l.key).Result()
	if err != nil || token != l.token {
		return credential.ErrCoordinationUnavailable
	}
	return nil
}

var releaseCredentialLease = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func (l *redisCredentialLease) Release(ctx context.Context) {
	// On failure the bounded lease expires; never delete a successor's lease.
	_ = releaseCredentialLease.Run(ctx, l.client, []string{l.key}, l.token).Err()
}
