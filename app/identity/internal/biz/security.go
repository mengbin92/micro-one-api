package biz

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/totp"
)

const securityTTL = 5 * time.Minute

var (
	ErrMFAInvalid            = kerrors.BadRequest(v.SecurityErrorReason_MFA_INVALID.String(), "invalid, expired or already used verification")
	ErrMFAUnavailable        = kerrors.ServiceUnavailable(v.SecurityErrorReason_MFA_UNAVAILABLE.String(), "security verification unavailable")
	ErrMFALocked             = kerrors.New(429, v.SecurityErrorReason_MFA_LOCKED.String(), "too many attempts; retry in five minutes")
	ErrStepUpRequired        = kerrors.Forbidden(v.SecurityErrorReason_STEP_UP_REQUIRED.String(), "recent security verification required")
	ErrMFAEnrollmentRequired = kerrors.Forbidden(v.SecurityErrorReason_MFA_ENROLLMENT_REQUIRED.String(), "enable MFA in your security center first")
)

type MFAState struct {
	UserID                                int64
	Secret, PendingSecret, PendingSession string
	PendingEpoch, PendingUntil            int64
	RecoveryHashes                        []string
	Revision                              uint64
	LastStep                              int64
	Failures                              int
	LockedUntil                           int64
}

type SecurityRepo interface {
	Configured() bool
	State(context.Context, IAMTx, int64) (MFAState, error)
	Save(context.Context, IAMTx, MFAState) error
	CreateToken(context.Context, IAMTx, *Token) error
}

type StepUpProof struct {
	UserID, PasswordEpoch int64
	MFARevision           uint64
}

type StepUpRepo interface {
	PutProof(context.Context, string, StepUpProof, time.Duration) error
	Proof(context.Context, string) (StepUpProof, error)
}

type SecurityStatus struct {
	Enabled, Configured, StepUpRequired, AdminMFARequired, PasswordAvailable bool
	RecoveryCodesRemaining                                                   int
}

type MFARequired struct{ Challenge string }

func (*MFARequired) Error() string { return "MFA verification required" }

func (uc *IdentityUsecase) SetSecurity(repo SecurityRepo, challenges ChallengeRepo, proofs StepUpRepo) {
	uc.security, uc.securityChallenges, uc.stepUp = repo, challenges, proofs
}

// Password and OAuth share this last step. No session is issued before MFA.
func (uc *IdentityUsecase) completePrimaryAuthentication(ctx context.Context, user *User) (string, error) {
	if uc.security == nil {
		return uc.generateSessionToken(user)
	}
	var state MFAState
	err := uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		current, err := uc.iam.User(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		if current.Status != UserStatusEnabled || current.PasswordChangedAt != user.PasswordChangedAt {
			return ErrSessionRevoked
		}
		state, err = uc.security.State(ctx, tx, user.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	if state.Secret == "" {
		return uc.generateSessionToken(user)
	}
	if uc.securityChallenges == nil {
		return "", ErrMFAUnavailable
	}
	now := uc.now()
	claims := UserSessionClaims{UserID: user.ID, TokenType: "mfa_login", PwdEpoch: user.PasswordChangedAt, RegisteredClaims: jwt.RegisteredClaims{ID: uc.generateToken(), Issuer: uc.sessionIssuer, Audience: []string{"micro-one-api-mfa"}, Subject: strconv.FormatInt(user.ID, 10), ExpiresAt: jwt.NewNumericDate(now.Add(securityTTL)), IssuedAt: jwt.NewNumericDate(now)}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(uc.sessionSecret)
	if err != nil {
		return "", err
	}
	hash := securityHash(raw)
	err = uc.securityChallenges.Reserve(ctx, "mfa_login", hash, hash, EmailChallenge{UserID: user.ID, PasswordEpoch: user.PasswordChangedAt}, securityTTL, 0)
	if err == nil {
		err = uc.securityChallenges.Activate(ctx, "mfa_login", hash, hash)
	}
	if err != nil {
		return "", err
	}
	return "", &MFARequired{Challenge: raw}
}

func securityHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func (uc *IdentityUsecase) CompleteMFALogin(ctx context.Context, challenge, code string) (*User, string, error) {
	if uc.security == nil || uc.securityChallenges == nil || len(challenge) > 4096 {
		return nil, "", ErrMFAUnavailable
	}
	claims := &UserSessionClaims{}
	parsed, err := jwt.ParseWithClaims(challenge, claims, func(*jwt.Token) (any, error) { return uc.sessionSecret, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(uc.sessionIssuer), jwt.WithAudience("micro-one-api-mfa"), jwt.WithExpirationRequired(), jwt.WithTimeFunc(uc.now))
	if err != nil || !parsed.Valid || claims.TokenType != "mfa_login" || claims.UserID <= 0 || claims.ID == "" || claims.Subject != strconv.FormatInt(claims.UserID, 10) {
		return nil, "", ErrMFAInvalid
	}
	var user User
	var token string
	var outcome error
	err = uc.runtimeWriteEvent(ctx, "credential.mfa.login", strconv.FormatInt(claims.UserID, 10), "MFA login", authorization.Actor{UserID: claims.UserID}, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		event.Result = "success"
		outcome = nil
		user, err = uc.iam.User(ctx, tx, claims.UserID)
		if err != nil {
			return err
		}
		if user.Status != UserStatusEnabled || user.PasswordChangedAt != claims.PwdEpoch {
			return ErrMFAInvalid
		}
		state, e := uc.security.State(ctx, tx, user.ID)
		if e != nil {
			return e
		}
		if state.Secret == "" {
			return ErrMFAInvalid
		}
		outcome = uc.consumeMFA(&state, code)
		hash := securityHash(challenge)
		if outcome != nil {
			event.Action += ".failed_attempt"
			event.Diff = `{ "verified": false }`
			if _, e = uc.securityChallenges.Consume(ctx, "mfa_login", hash, "invalid", 5); e != nil && !errors.Is(e, ErrChallengeInvalid) {
				return e
			}
			return uc.security.Save(ctx, tx, state)
		}
		record, e := uc.securityChallenges.Consume(ctx, "mfa_login", hash, hash, 5)
		if e != nil || record.UserID != user.ID || record.PasswordEpoch != user.PasswordChangedAt {
			return ErrMFAInvalid
		}
		if e = uc.security.Save(ctx, tx, state); e != nil {
			return e
		}
		token, e = uc.generateSessionToken(&user)
		return e
	})
	if err != nil {
		return nil, "", err
	}
	if outcome != nil {
		return nil, "", outcome
	}
	return &user, token, nil
}

// A failure is committed by callers, so retrying on another replica cannot reset it.
func (uc *IdentityUsecase) consumeMFA(s *MFAState, code string) error {
	if uc.now().UnixMilli() < s.LockedUntil {
		return ErrMFALocked
	}
	if s.LockedUntil > 0 {
		s.Failures = 0
		s.LockedUntil = 0
	}
	if step, ok := totp.Match(s.Secret, code, uc.now(), s.LastStep); ok {
		s.LastStep = step
		s.Failures = 0
		return nil
	}
	if len(code) == 32 {
		hash := securityHash(strings.ToLower(code))
		if i := slices.Index(s.RecoveryHashes, hash); i >= 0 {
			s.RecoveryHashes = slices.Delete(s.RecoveryHashes, i, i+1)
			s.Failures = 0
			return nil
		}
	}
	s.Failures++
	if s.Failures >= 5 {
		s.LockedUntil = uc.now().Add(securityTTL).UnixMilli()
		return ErrMFALocked
	}
	return ErrMFAInvalid
}

func (uc *IdentityUsecase) securityActor(ctx context.Context, raw string) (authorization.Actor, error) {
	if uc.security == nil || uc.iam == nil {
		return authorization.Actor{}, ErrMFAUnavailable
	}
	s, err := uc.GetSessionAuthorization(ctx, raw, authorization.Platform())
	if err != nil {
		return authorization.Actor{}, err
	}
	return s.Actor, nil
}

func (uc *IdentityUsecase) GetSecurityStatus(ctx context.Context, raw string) (SecurityStatus, error) {
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return SecurityStatus{}, err
	}
	var state MFAState
	var passwordAvailable bool
	err = uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		var e error
		u, e := uc.iam.User(ctx, tx, a.UserID)
		if e != nil {
			return e
		}
		passwordAvailable = u.PasswordHash != ""
		state, e = uc.security.State(ctx, tx, a.UserID)
		return e
	})
	return SecurityStatus{PasswordAvailable: passwordAvailable, Enabled: state.Secret != "", Configured: uc.security.Configured(), RecoveryCodesRemaining: len(state.RecoveryHashes), StepUpRequired: os.Getenv("IDENTITY_STEP_UP_ENABLED") == "true", AdminMFARequired: os.Getenv("IDENTITY_MFA_REQUIRE_ADMIN") == "true"}, err
}

// VerifyStepUp authenticates again without accepting a client-selected actor.
func (uc *IdentityUsecase) VerifyStepUp(ctx context.Context, raw, password, code string) error {
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return err
	}
	if uc.stepUp == nil {
		return ErrMFAUnavailable
	}
	var proof StepUpProof
	var outcome error
	err = uc.runtimeWriteEvent(ctx, "credential.step_up", strconv.FormatInt(a.UserID, 10), "session security verification", a, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		event.Result = "success"
		outcome = nil
		u, e := uc.iam.User(ctx, tx, a.UserID)
		if e != nil {
			return e
		}
		if e = uc.checkSecuritySession(ctx, tx, a, u); e != nil {
			return e
		}
		s, e := uc.security.State(ctx, tx, a.UserID)
		if e != nil {
			return e
		}
		if s.Secret != "" {
			outcome = uc.consumeMFA(&s, code)
		} else {
			if uc.now().UnixMilli() >= s.LockedUntil && s.LockedUntil > 0 {
				s.Failures = 0
				s.LockedUntil = 0
			}
			if uc.now().UnixMilli() < s.LockedUntil {
				outcome = ErrMFALocked
			} else if u.PasswordHash == "" || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
				s.Failures++
				outcome = ErrMFAInvalid
				if s.Failures >= 5 {
					s.LockedUntil = uc.now().Add(securityTTL).UnixMilli()
					outcome = ErrMFALocked
				}
			} else {
				s.Failures = 0
				s.LockedUntil = 0
			}
		}
		if outcome != nil {
			event.Action += ".failed_attempt"
			event.Diff = `{ "verified": false }`
		}
		proof = StepUpProof{UserID: a.UserID, PasswordEpoch: u.PasswordChangedAt, MFARevision: s.Revision}
		return uc.security.Save(ctx, tx, s)
	})
	if err != nil {
		return err
	}
	if outcome != nil {
		return outcome
	}
	ttl := securityTTL
	if remaining := a.ExpiresAt.Sub(uc.now()); remaining < ttl {
		ttl = remaining
	}
	return uc.stepUp.PutProof(ctx, a.SessionID, proof, ttl)
}

func (uc *IdentityUsecase) requireStepUpTx(ctx context.Context, tx IAMTx, a authorization.Actor, admin bool) error {
	if uc.security == nil {
		return nil
	}
	s, err := uc.security.State(ctx, tx, a.UserID)
	if err != nil {
		return err
	}
	if admin && os.Getenv("IDENTITY_MFA_REQUIRE_ADMIN") == "true" && s.Secret == "" {
		return ErrMFAEnrollmentRequired
	}
	if os.Getenv("IDENTITY_STEP_UP_ENABLED") != "true" && !(admin && os.Getenv("IDENTITY_MFA_REQUIRE_ADMIN") == "true") {
		return nil
	}
	if uc.stepUp == nil {
		return ErrMFAUnavailable
	}
	proof, err := uc.stepUp.Proof(ctx, a.SessionID)
	if err != nil {
		return err
	}
	if proof.UserID != a.UserID || proof.PasswordEpoch != a.PasswordEpoch || proof.MFARevision != s.Revision {
		return ErrStepUpRequired
	}
	return nil
}

func (uc *IdentityUsecase) RequireStepUp(ctx context.Context, raw string, admin bool) error {
	if uc.security == nil || (os.Getenv("IDENTITY_STEP_UP_ENABLED") != "true" && !(admin && os.Getenv("IDENTITY_MFA_REQUIRE_ADMIN") == "true")) {
		return nil
	}
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return err
	}
	return uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		u, e := uc.iam.User(ctx, tx, a.UserID)
		if e != nil {
			return e
		}
		if e = uc.checkSecuritySession(ctx, tx, a, u); e != nil {
			return e
		}
		return uc.requireStepUpTx(ctx, tx, a, admin)
	})
}

func newRecoveryCodes() ([]string, []string, error) {
	codes, hashes := make([]string, 10), make([]string, 10)
	for i := range codes {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return nil, nil, err
		}
		codes[i] = hex.EncodeToString(b)
		hashes[i] = securityHash(codes[i])
	}
	return codes, hashes, nil
}

// ManageMFA has only enrollment/confirmation/disable/rotation transitions.
// Secrets never enter audit payloads; changes invalidate all existing sessions.
func (uc *IdentityUsecase) ManageMFA(ctx context.Context, raw, action, code string) (secret, uri string, codes []string, err error) {
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return "", "", nil, err
	}
	if !uc.security.Configured() {
		return "", "", nil, ErrMFAUnavailable
	}
	var outcome error
	err = uc.runtimeWriteEvent(ctx, "credential.mfa."+action, strconv.FormatInt(a.UserID, 10), "self MFA management", a, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		event.Result = "success"
		outcome = nil
		secret = ""
		uri = ""
		codes = nil
		u, e := uc.iam.User(ctx, tx, a.UserID)
		if e != nil {
			return e
		}
		if e = uc.checkSecuritySession(ctx, tx, a, u); e != nil {
			return e
		}
		s, e := uc.security.State(ctx, tx, a.UserID)
		if e != nil {
			return e
		}
		if action != "confirm" {
			// Enrollment is sensitive even when the rollout flag is off.
			if uc.stepUp == nil {
				return ErrMFAUnavailable
			}
			proof, e := uc.stepUp.Proof(ctx, a.SessionID)
			if e != nil {
				return e
			}
			if proof.UserID != a.UserID || proof.PasswordEpoch != a.PasswordEpoch || proof.MFARevision != s.Revision {
				return ErrStepUpRequired
			}
		}
		switch action {
		case "setup":
			if s.Secret != "" {
				return ErrMFAInvalid
			}
			secret, e = totp.NewSecret()
			if e != nil {
				return e
			}
			s.PendingSecret = secret
			s.PendingSession = a.SessionID
			s.PendingEpoch = a.PasswordEpoch
			s.PendingUntil = uc.now().Add(securityTTL).UnixMilli()
			uri = "otpauth://totp/" + url.PathEscape("Micro-One API:"+u.Username) + "?" + url.Values{"secret": {secret}, "issuer": {"Micro-One API"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}.Encode()
			return uc.security.Save(ctx, tx, s)
		case "confirm":
			if s.Secret != "" || s.PendingSecret == "" || s.PendingSession != a.SessionID || s.PendingEpoch != a.PasswordEpoch || s.PendingUntil <= uc.now().UnixMilli() {
				return ErrMFAInvalid
			}
			s.Secret = s.PendingSecret
			outcome = uc.consumeMFA(&s, code)
			if outcome != nil {
				event.Action += ".failed_attempt"
				event.Diff = `{ "verified": false }`
				s.Secret = ""
				return uc.security.Save(ctx, tx, s)
			}
			codes, s.RecoveryHashes, e = newRecoveryCodes()
			if e != nil {
				return e
			}
		case "disable":
			if s.Secret == "" {
				return ErrMFAInvalid
			}
			s.Secret = ""
			s.RecoveryHashes = nil
		case "recovery":
			if s.Secret == "" {
				return ErrMFAInvalid
			}
			codes, s.RecoveryHashes, e = newRecoveryCodes()
			if e != nil {
				return e
			}
		default:
			return ErrMFAInvalid
		}
		s.PendingSecret = ""
		s.PendingSession = ""
		s.PendingUntil = 0
		s.PendingEpoch = 0
		s.Revision++
		if e = uc.security.Save(ctx, tx, s); e != nil {
			return e
		}
		u.PasswordChangedAt = nextPasswordEpoch(u.PasswordChangedAt, uc.now())
		if e = uc.iam.UpdateAccount(ctx, tx, u, []string{"password_epoch"}); e != nil {
			return e
		}
		return uc.iam.RevokeUserSessions(ctx, tx, u.ID, uc.now())
	})
	if err == nil {
		err = outcome
	}
	if err != nil {
		return "", "", nil, err
	}
	return secret, uri, codes, nil
}

func (uc *IdentityUsecase) checkSecuritySession(ctx context.Context, tx IAMTx, a authorization.Actor, u User) error {
	if err := checkIAMIdentity(a, u, uc.now()); err != nil {
		return err
	}
	p, err := uc.iam.Policy(ctx, tx)
	if err != nil {
		return err
	}
	if p.Mode == "legacy" && p.Cutover == "idle" {
		return nil
	}
	if p.Mode != "iam" || p.Cutover != "complete" {
		return ErrIAMCutoverBlocked
	}
	session, err := uc.iam.Session(ctx, tx, a.SessionID, authorization.Platform())
	if err != nil {
		return err
	}
	return checkIAMSession(a, session, uc.now())
}

func (uc *IdentityUsecase) checkSelfSecurityChange(ctx context.Context, tx IAMTx, before, after User) error {
	if uc.security == nil || os.Getenv("IDENTITY_STEP_UP_ENABLED") != "true" || (before.PasswordHash == after.PasswordHash && before.Email == after.Email && before.Username == after.Username) {
		return nil
	}
	a, err := uc.authenticateSession(authorization.Credential(ctx))
	if err != nil {
		return err
	}
	if err = uc.checkSecuritySession(ctx, tx, a, before); err != nil {
		return err
	}
	return uc.requireStepUpTx(ctx, tx, a, false)
}

// OAuth-only accounts have no password to re-enter. A newly verified OAuth
// login may attest its own JTI for initial enrollment; old sessions cannot.
func (uc *IdentityUsecase) attestOAuthSession(ctx context.Context, user *User, raw string) error {
	if uc.security == nil || user.PasswordHash != "" {
		return nil
	}
	a, err := uc.authenticateSession(raw)
	if err != nil {
		return err
	}
	var state MFAState
	err = uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		var e error
		state, e = uc.security.State(ctx, tx, a.UserID)
		return e
	})
	if err != nil {
		return err
	}
	if uc.stepUp == nil || state.Secret != "" {
		return ErrMFAUnavailable
	}
	return uc.stepUp.PutProof(ctx, a.SessionID, StepUpProof{UserID: a.UserID, PasswordEpoch: a.PasswordEpoch, MFARevision: state.Revision}, securityTTL)
}
