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

// passkeyModel stores one attested WebAuthn credential per row. The COSE
// public key is AES-GCM ciphertext just like MFA secrets; the attestation
// statement and transports stay plaintext metadata.
type passkeyModel struct {
	CredentialID []byte `gorm:"primaryKey;column:credential_id"`
	UserID       int64  `gorm:"index"`
	PublicKey    string
	Attestation  string
	Transport    string
	Name         string
	SignCount    uint32
	CreatedAt    int64
	LastUsedAt   int64
}

func (passkeyModel) TableName() string { return "identity_passkey" }

type passkeyRepo struct {
	data *Data
	key  []byte
}

func NewPasskeyRepo(d *Data, key []byte) biz.PasskeyRepo {
	return &passkeyRepo{data: d, key: append([]byte(nil), key...)}
}

func (r *passkeyRepo) Configured() bool {
	return len(r.key) == 16 || len(r.key) == 24 || len(r.key) == 32
}

func (r *passkeyRepo) List(ctx context.Context, handle biz.IAMTx, userID int64) ([]biz.PasskeyCredential, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return nil, err
	}
	var rows []passkeyModel
	if err = tx.db.Where("user_id = ?", userID).Order("created_at").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := make([]biz.PasskeyCredential, 0, len(rows))
	for _, p := range rows {
		c, err := toBizPasskey(p, r.key)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *passkeyRepo) Save(ctx context.Context, handle biz.IAMTx, userID int64, c biz.PasskeyCredential) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	p, err := newPasskey(userID, c, r.key)
	if err != nil {
		return err
	}
	// credential_id is client-supplied at enrollment and there is no
	// attestation verification, so an upsert must never let one account
	// overwrite another account's credential row.
	var owner int64
	if err = tx.db.Model(&passkeyModel{}).Select("user_id").Where("credential_id = ?", p.CredentialID).Scan(&owner).Error; err != nil {
		return iamStorageError(tx, err)
	}
	if owner != 0 && owner != userID {
		return biz.ErrMFAInvalid
	}
	if err = tx.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "credential_id"}}, UpdateAll: true}).Create(&p).Error; err != nil {
		return iamStorageError(tx, err)
	}
	tx.mutated = true
	return nil
}

func (r *passkeyRepo) Delete(ctx context.Context, handle biz.IAMTx, userID int64, credentialID []byte) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	res := tx.db.Where("user_id = ? AND credential_id = ?", userID, credentialID).Delete(&passkeyModel{})
	if res.Error != nil {
		return iamStorageError(tx, res.Error)
	}
	if res.RowsAffected == 0 {
		return biz.ErrMFAInvalid
	}
	tx.mutated = true
	return nil
}

func toBizPasskey(p passkeyModel, key []byte) (biz.PasskeyCredential, error) {
	pub, err := appcrypto.Decrypt(p.PublicKey, key)
	if err != nil {
		return biz.PasskeyCredential{}, biz.ErrMFAUnavailable
	}
	c := biz.PasskeyCredential{ID: append([]byte(nil), p.CredentialID...), PublicKey: []byte(pub), Attestation: p.Attestation, Name: p.Name, SignCount: p.SignCount, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt}
	if err = jsonx.Unmarshal([]byte(p.Transport), &c.Transport); err != nil {
		return biz.PasskeyCredential{}, biz.ErrMFAUnavailable
	}
	return c, nil
}

func newPasskey(userID int64, c biz.PasskeyCredential, key []byte) (passkeyModel, error) {
	pub, err := appcrypto.Encrypt(string(c.PublicKey), key)
	if err != nil {
		return passkeyModel{}, biz.ErrMFAUnavailable
	}
	transport, err := jsonx.Marshal(c.Transport)
	if err != nil {
		return passkeyModel{}, biz.ErrMFAUnavailable
	}
	return passkeyModel{CredentialID: append([]byte(nil), c.ID...), UserID: userID, PublicKey: pub, Attestation: c.Attestation, Transport: string(transport), Name: c.Name, SignCount: c.SignCount, CreatedAt: c.CreatedAt, LastUsedAt: c.LastUsedAt}, nil
}
