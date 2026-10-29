package data

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"micro-one-api/app/identity/internal/biz"

	"github.com/redis/go-redis/v9"
)

type challengeRepo struct{ data *Data }

// NewChallengeRepo reuses the shared Redis client. It intentionally fails closed
// without Redis, including Lite: replicas must never use independent secrets.
func NewChallengeRepo(d *Data) biz.ChallengeRepo { return &challengeRepo{data: d} }

func (r *challengeRepo) Available() bool { return r.client() != nil }

func challengeKey(purpose, email string) string {
	// Redis key addressing only — the pre-image is a purpose label plus an identifier,
	// never a password or secret, so SHA-256 here is collision resistance, not password hashing.
	hash := sha256.Sum256([]byte(purpose + ":" + email))
	return "identity:email-challenge:v1:" + hex.EncodeToString(hash[:])
}

func (r *challengeRepo) client() *redis.Client {
	if r == nil || r.data == nil {
		return nil
	}
	return r.data.redis
}

func (r *challengeRepo) Reserve(ctx context.Context, purpose, email, hash string, record biz.EmailChallenge, ttl, cooldown time.Duration) error {
	client := r.client()
	if client == nil {
		return biz.ErrChallengeUnavailable
	}
	// Redis TIME supplies the common replica clock. The pending state makes an
	// undelivered token unusable, and the compare-and-set transitions below keep
	// late SMTP completions from activating or cancelling a newer issuance.
	result, err := client.Eval(ctx, `
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
local last = tonumber(redis.call('HGET', KEYS[1], 'sent_at'))
if last and now - last < tonumber(ARGV[5]) then return 0 end
redis.call('DEL', KEYS[1])
redis.call('HSET', KEYS[1], 'hash', ARGV[1], 'user_id', ARGV[2], 'epoch', ARGV[3], 'attempts', 0, 'ready', 0, 'sent_at', now)
redis.call('PEXPIRE', KEYS[1], ARGV[4])
return 1`, []string{challengeKey(purpose, email)}, hash, record.UserID, record.PasswordEpoch, ttl.Milliseconds(), cooldown.Milliseconds()).Int()
	if err != nil {
		return biz.ErrChallengeUnavailable
	}
	if result == 0 {
		return biz.ErrChallengeRateLimited
	}
	return nil
}

func (r *challengeRepo) Activate(ctx context.Context, purpose, email, hash string) error {
	client := r.client()
	if client == nil {
		return biz.ErrChallengeUnavailable
	}
	result, err := client.Eval(ctx, `
if redis.call('HGET', KEYS[1], 'hash') ~= ARGV[1] then return 0 end
redis.call('HSET', KEYS[1], 'ready', 1)
return 1`, []string{challengeKey(purpose, email)}, hash).Int()
	if err != nil {
		return biz.ErrChallengeUnavailable
	}
	if result == 0 {
		return biz.ErrChallengeInvalid
	}
	return nil
}

func (r *challengeRepo) Cancel(ctx context.Context, purpose, email, hash string) error {
	client := r.client()
	if client == nil {
		return biz.ErrChallengeUnavailable
	}
	err := client.Eval(ctx, `
if redis.call('HGET', KEYS[1], 'hash') == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0`, []string{challengeKey(purpose, email)}, hash).Err()
	if err != nil {
		return biz.ErrChallengeUnavailable
	}
	return nil
}

func (r *challengeRepo) Check(ctx context.Context, purpose, email, hash string) (*biz.EmailChallenge, error) {
	client := r.client()
	if client == nil {
		return nil, biz.ErrChallengeUnavailable
	}
	result, err := client.Eval(ctx, `
if redis.call('HGET', KEYS[1], 'ready') ~= '1' or redis.call('HGET', KEYS[1], 'hash') ~= ARGV[1] then return {} end
return {redis.call('HGET', KEYS[1], 'user_id'), redis.call('HGET', KEYS[1], 'epoch')}
`, []string{challengeKey(purpose, email)}, hash).StringSlice()
	return challengeRecord(result, err)
}

func (r *challengeRepo) Consume(ctx context.Context, purpose, email, hash string, attempts int) (*biz.EmailChallenge, error) {
	client := r.client()
	if client == nil {
		return nil, biz.ErrChallengeUnavailable
	}
	result, err := client.Eval(ctx, `
if redis.call('HGET', KEYS[1], 'ready') ~= '1' then return {} end
if redis.call('HGET', KEYS[1], 'hash') ~= ARGV[1] then
  local attempts = redis.call('HINCRBY', KEYS[1], 'attempts', 1)
  if attempts >= tonumber(ARGV[2]) then redis.call('HSET', KEYS[1], 'ready', 0, 'hash', '') end
  return {}
end
local result = {redis.call('HGET', KEYS[1], 'user_id'), redis.call('HGET', KEYS[1], 'epoch')}
redis.call('HSET', KEYS[1], 'ready', 0, 'hash', '')
return result`, []string{challengeKey(purpose, email)}, hash, attempts).StringSlice()
	return challengeRecord(result, err)
}

func challengeRecord(result []string, err error) (*biz.EmailChallenge, error) {
	if err != nil {
		return nil, biz.ErrChallengeUnavailable
	}
	if len(result) != 2 {
		return nil, biz.ErrChallengeInvalid
	}
	userID, e1 := strconv.ParseInt(result[0], 10, 64)
	epoch, e2 := strconv.ParseInt(result[1], 10, 64)
	if e1 != nil || e2 != nil {
		return nil, biz.ErrChallengeInvalid
	}
	return &biz.EmailChallenge{UserID: userID, PasswordEpoch: epoch}, nil
}
