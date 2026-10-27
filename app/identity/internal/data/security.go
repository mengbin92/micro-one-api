package data

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/pkg/jsonx"
	appcrypto "micro-one-api/platform/security/crypto"
)

type securityRepo struct {
	data *Data
	key  []byte
}

func NewSecurityRepo(d *Data, key []byte) biz.SecurityRepo {
	return &securityRepo{data: d, key: append([]byte(nil), key...)}
}
func (r *securityRepo) Configured() bool {
	return len(r.key) == 16 || len(r.key) == 24 || len(r.key) == 32
}

func (r *securityRepo) CreateToken(ctx context.Context, handle biz.IAMTx, token *biz.Token) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	bound := NewRepository(&Data{db: tx.db, redis: r.data.redis})
	if err = bound.CreateToken(ctx, token); err != nil {
		return iamStorageError(tx, err)
	}
	tx.mutated = true
	return nil
}

type mfaModel struct {
	UserID                                int64 `gorm:"primaryKey"`
	Secret, PendingSecret, PendingSession string
	PendingEpoch, PendingUntil            int64
	RecoveryHashes                        string
	Revision                              uint64
	LastStep                              int64
	Failures                              int
	LockedUntil                           int64
}

func (mfaModel) TableName() string { return "identity_mfa" }

func (r *securityRepo) State(ctx context.Context, handle biz.IAMTx, id int64) (biz.MFAState, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return biz.MFAState{}, err
	}
	var p mfaModel
	err = tx.db.Where("user_id = ?", id).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return biz.MFAState{UserID: id, Revision: 1, LastStep: -1}, nil
	}
	if err != nil {
		return biz.MFAState{}, iamStorageError(tx, err)
	}
	return toBizMFA(p, r.key)
}

func (r *securityRepo) Save(ctx context.Context, handle biz.IAMTx, s biz.MFAState) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	p, err := newMFA(s, r.key)
	if err != nil {
		return err
	}
	if err = tx.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, UpdateAll: true}).Create(&p).Error; err != nil {
		return iamStorageError(tx, err)
	}
	tx.mutated = true
	return nil
}

// Proofs attest an existing JTI; they cannot authenticate a caller themselves.
func (r *challengeRepo) PutProof(ctx context.Context, jti string, p biz.StepUpProof, ttl time.Duration) error {
	if r.client() == nil || ttl <= 0 {
		return biz.ErrMFAUnavailable
	}
	b, err := jsonx.Marshal(p)
	if err != nil {
		return biz.ErrMFAUnavailable
	}
	if err = r.client().Set(ctx, challengeKey("step_up", jti), b, ttl).Err(); err != nil {
		return biz.ErrMFAUnavailable
	}
	return nil
}
func (r *challengeRepo) Proof(ctx context.Context, jti string) (biz.StepUpProof, error) {
	if r.client() == nil {
		return biz.StepUpProof{}, biz.ErrMFAUnavailable
	}
	b, err := r.client().Get(ctx, challengeKey("step_up", jti)).Bytes()
	if errors.Is(err, redis.Nil) {
		return biz.StepUpProof{}, biz.ErrStepUpRequired
	}
	if err != nil {
		return biz.StepUpProof{}, biz.ErrMFAUnavailable
	}
	var p biz.StepUpProof
	if jsonx.Unmarshal(b, &p) != nil {
		return p, biz.ErrMFAUnavailable
	}
	return p, nil
}

func toBizMFA(p mfaModel, key []byte) (biz.MFAState, error) {
	var err error
	s := biz.MFAState{UserID: p.UserID, PendingSession: p.PendingSession, PendingEpoch: p.PendingEpoch, PendingUntil: p.PendingUntil, Revision: p.Revision, LastStep: p.LastStep, Failures: p.Failures, LockedUntil: p.LockedUntil}
	if p.Secret != "" {
		s.Secret, err = appcrypto.Decrypt(p.Secret, key)
		if err != nil {
			return s, biz.ErrMFAUnavailable
		}
	}
	if p.PendingSecret != "" {
		s.PendingSecret, err = appcrypto.Decrypt(p.PendingSecret, key)
		if err != nil {
			return s, biz.ErrMFAUnavailable
		}
	}
	if err = jsonx.Unmarshal([]byte(p.RecoveryHashes), &s.RecoveryHashes); err != nil {
		return s, biz.ErrMFAUnavailable
	}
	return s, nil
}

func newMFA(s biz.MFAState, key []byte) (mfaModel, error) {
	var err error
	p := mfaModel{UserID: s.UserID, PendingSession: s.PendingSession, PendingEpoch: s.PendingEpoch, PendingUntil: s.PendingUntil, Revision: s.Revision, LastStep: s.LastStep, Failures: s.Failures, LockedUntil: s.LockedUntil}
	if s.Secret != "" {
		p.Secret, err = appcrypto.Encrypt(s.Secret, key)
		if err != nil {
			return mfaModel{}, biz.ErrMFAUnavailable
		}
	}
	if s.PendingSecret != "" {
		p.PendingSecret, err = appcrypto.Encrypt(s.PendingSecret, key)
		if err != nil {
			return mfaModel{}, biz.ErrMFAUnavailable
		}
	}
	b, err := jsonx.Marshal(s.RecoveryHashes)
	if err != nil {
		return mfaModel{}, biz.ErrMFAUnavailable
	}
	p.RecoveryHashes = string(b)
	return p, nil
}
