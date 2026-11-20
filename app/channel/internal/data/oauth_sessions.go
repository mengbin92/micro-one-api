package data

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	channeloauth "micro-one-api/app/channel/internal/biz/oauth"
	"micro-one-api/pkg/jsonx"
)

type oauthSessionRepo struct{ redis *redis.Client }

func NewOAuthSessionRepo(r *Repository) channeloauth.SessionRepository {
	if r.redis == nil && r.db == nil {
		return channeloauth.NewSessionStore(5 * time.Minute)
	}
	return &oauthSessionRepo{redis: r.redis}
}
func (*oauthSessionRepo) TTL() time.Duration { return 5 * time.Minute }
func (r *oauthSessionRepo) Set(s *channeloauth.Session) error {
	if r.redis == nil {
		return fmt.Errorf("oauth sessions require Redis with persistent storage")
	}
	ttl := time.Until(s.ExpiresAt(r.TTL()))
	if ttl <= 0 {
		return channeloauth.ErrInvalidSession
	}
	b, err := jsonx.Marshal(s)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.redis.Set(ctx, "channel:oauth:session:"+s.ID, b, ttl).Err()
}
func (r *oauthSessionRepo) load(id string, now time.Time, pop bool) (*channeloauth.Session, bool) {
	if r.redis == nil || id == "" {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var b []byte
	var err error
	if pop {
		b, err = r.redis.GetDel(ctx, "channel:oauth:session:"+id).Bytes()
	} else {
		b, err = r.redis.Get(ctx, "channel:oauth:session:"+id).Bytes()
	}
	if err != nil {
		return nil, false
	}
	var s channeloauth.Session
	if err := jsonx.Unmarshal(b, &s); err != nil {
		return nil, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !now.Before(s.ExpiresAt(r.TTL())) {
		return nil, false
	}
	return &s, true
}

var swapOAuthSession = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if ARGV[2] == '' then
  redis.call('DEL', KEYS[1])
else
  redis.call('SET', KEYS[1], ARGV[2], 'KEEPTTL')
end
return 1
`)

func (r *oauthSessionRepo) CompareAndSwap(current, next *channeloauth.Session, now time.Time) (bool, error) {
	if r.redis == nil {
		return false, fmt.Errorf("oauth sessions require Redis with persistent storage")
	}
	if !now.Before(current.ExpiresAt(r.TTL())) {
		return false, nil
	}
	previous, err := jsonx.Marshal(current)
	if err != nil {
		return false, err
	}
	var replacement []byte
	if next != nil {
		replacement, err = jsonx.Marshal(next)
		if err != nil {
			return false, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	changed, err := swapOAuthSession.Run(ctx, r.redis, []string{"channel:oauth:session:" + current.ID}, previous, string(replacement)).Int()
	return changed == 1, err
}
func (r *oauthSessionRepo) Get(id string, now time.Time) (*channeloauth.Session, bool) {
	return r.load(id, now, false)
}
func (r *oauthSessionRepo) Pop(id string, now time.Time) (*channeloauth.Session, bool) {
	return r.load(id, now, true)
}
func (r *oauthSessionRepo) Delete(id string) {
	if r.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = r.redis.Del(ctx, "channel:oauth:session:"+id).Err()
	}
}
func (*oauthSessionRepo) Cleanup(time.Time) {}
