package biz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	identityv1 "micro-one-api/api/identity/v1"
	applogger "micro-one-api/platform/logging"
)

const (
	ChallengeVerification        = "verification"
	ChallengePasswordReset       = "password_reset"
	EmailChallengeTTL            = 10 * time.Minute
	EmailChallengeResendInterval = time.Minute
	EmailChallengeMaxAttempts    = 5
)

var (
	ErrChallengeInvalid     = kerrors.BadRequest(identityv1.EmailChallengeErrorReason_EMAIL_CHALLENGE_INVALID.String(), "invalid or expired email challenge")
	ErrChallengeRateLimited = kerrors.New(429, identityv1.EmailChallengeErrorReason_EMAIL_CHALLENGE_RATE_LIMITED.String(), "please wait before requesting another email")
	ErrChallengeUnavailable = kerrors.ServiceUnavailable(identityv1.EmailChallengeErrorReason_EMAIL_CHALLENGE_UNAVAILABLE.String(), "email challenge storage is unavailable")
	ErrMailUnavailable      = kerrors.ServiceUnavailable(identityv1.EmailChallengeErrorReason_EMAIL_DELIVERY_UNAVAILABLE.String(), "email delivery is unavailable")
	ErrEmailInvalid         = kerrors.BadRequest(identityv1.EmailChallengeErrorReason_EMAIL_ADDRESS_INVALID.String(), "invalid email address")
)

// CodeDeliverer must submit the secret out of band, never return it to callers.
type CodeDeliverer interface {
	DeliverVerificationCode(context.Context, string, string) error
	DeliverResetToken(context.Context, string, string) error
}

// EmailChallenge carries the immutable identity facts bound to a reset token.
// The secret itself is only stored as a hash in the repository.
type EmailChallenge struct {
	UserID        int64
	PasswordEpoch int64
}

type ChallengeRepo interface {
	Reserve(context.Context, string, string, string, EmailChallenge, time.Duration, time.Duration) error
	Activate(context.Context, string, string, string) error
	Cancel(context.Context, string, string, string) error
	Check(context.Context, string, string, string) (*EmailChallenge, error)
	Consume(context.Context, string, string, string, int) (*EmailChallenge, error)
}

type EmailChallengeUsecase struct {
	repo          ChallengeRepo
	deliverer     CodeDeliverer
	recoverySlots chan struct{}
}

func NewEmailChallengeUsecase(repo ChallengeRepo, deliverer CodeDeliverer) *EmailChallengeUsecase {
	return &EmailChallengeUsecase{repo: repo, deliverer: deliverer, recoverySlots: make(chan struct{}, 4)}
}

// RequestRecovery accepts a bounded asynchronous request before looking up any
// account. Both known and unknown addresses take this same path, so SMTP latency
// and account state never become an observable public recovery response.
// Capacity errors depend only on global workload. No unbounded queue or worker
// survives its request, and the work has a separate, bounded lifetime.
func (u *EmailChallengeUsecase) RequestRecovery(ctx context.Context, email string, prepare func(context.Context, string) (int64, int64, error)) error {
	if !u.Available() || prepare == nil {
		return ErrChallengeUnavailable
	}
	if _, err := NormalizeEmail(email); err != nil {
		return err
	}
	email = strings.TrimSpace(email)
	select {
	case u.recoverySlots <- struct{}{}:
	default:
		return ErrChallengeUnavailable
	}
	workCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	go func() {
		defer func() { <-u.recoverySlots }()
		defer cancel()
		userID, epoch, err := prepare(workCtx, email)
		if err != nil {
			return
		}
		if err := u.IssueReset(workCtx, email, userID, epoch); err != nil && !errors.Is(err, ErrChallengeRateLimited) {
			// No address, account state, token, SMTP configuration or driver
			// error is included in logs or the public acknowledgement.
			applogger.Log.Warn("email recovery delivery failed")
		}
	}()
	return nil
}

func (u *EmailChallengeUsecase) Available() bool {
	if u == nil || u.repo == nil || u.deliverer == nil {
		return false
	}
	for _, dependency := range []any{u.repo, u.deliverer} {
		if available, ok := dependency.(interface{ Available() bool }); ok && !available.Available() {
			return false
		}
	}
	return true
}

// NormalizeEmail is shared by challenge issuance and consumption, preventing
// case or whitespace variants from creating independent attempt budgets.
func NormalizeEmail(value string) (string, error) {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 || strings.ContainsAny(value, "\r\n") {
		return "", ErrEmailInvalid
	}
	return strings.ToLower(value), nil
}

func (u *EmailChallengeUsecase) IssueVerification(ctx context.Context, email string) error {
	return u.issue(ctx, ChallengeVerification, email, EmailChallenge{})
}

func (u *EmailChallengeUsecase) IssueReset(ctx context.Context, email string, userID, passwordEpoch int64) error {
	return u.issue(ctx, ChallengePasswordReset, email, EmailChallenge{UserID: userID, PasswordEpoch: passwordEpoch})
}

func (u *EmailChallengeUsecase) issue(ctx context.Context, purpose, email string, record EmailChallenge) error {
	if u == nil || u.repo == nil {
		return ErrChallengeUnavailable
	}
	if u.deliverer == nil {
		return ErrMailUnavailable
	}
	deliveryEmail := strings.TrimSpace(email)
	email, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return err
	}
	code := hex.EncodeToString(secret)
	hash := hashChallenge(code)
	if err := u.repo.Reserve(ctx, purpose, email, hash, record, EmailChallengeTTL, EmailChallengeResendInterval); err != nil {
		return err
	}
	if purpose == ChallengePasswordReset {
		err = u.deliverer.DeliverResetToken(ctx, deliveryEmail, code)
	} else {
		err = u.deliverer.DeliverVerificationCode(ctx, deliveryEmail, code)
	}
	if err == nil {
		err = u.repo.Activate(ctx, purpose, email, hash)
	}
	if err != nil {
		// The HTTP request may have been cancelled while SMTP was in flight.
		// Revoke only this issuance with a bounded independent cleanup context.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = u.repo.Cancel(cleanup, purpose, email, hash)
	}
	return err
}

func (u *EmailChallengeUsecase) Consume(ctx context.Context, purpose, email, code string) (*EmailChallenge, error) {
	if u == nil || u.repo == nil {
		return nil, ErrChallengeUnavailable
	}
	email, err := NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	if purpose != ChallengeVerification && purpose != ChallengePasswordReset {
		return nil, ErrChallengeInvalid
	}
	return u.repo.Consume(ctx, purpose, email, hashChallenge(code), EmailChallengeMaxAttempts)
}

func hashChallenge(code string) string {
	hash := sha256.Sum256([]byte(code))
	return hex.EncodeToString(hash[:])
}
