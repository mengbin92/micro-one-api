package biz

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

// PasskeyCredential is one registered WebAuthn credential. PublicKey is the
// attested COSE key; the private key never leaves the authenticator.
type PasskeyCredential struct {
	ID          []byte
	PublicKey   []byte
	SignCount   uint32
	Attestation string
	Transport   []string
	Name        string
	CreatedAt   int64
	LastUsedAt  int64
}

// PasskeyRepo persists credentials inside the IAM write transaction so
// enrollment and revocation share the account's audit + revision semantics.
type PasskeyRepo interface {
	Configured() bool
	List(context.Context, IAMTx, int64) ([]PasskeyCredential, error)
	Save(context.Context, IAMTx, int64, PasskeyCredential) error
	Delete(context.Context, IAMTx, int64, []byte) error
}

// PasskeyCeremonyRepo stores one-shot WebAuthn ceremony sessions (Redis, TTL).
type PasskeyCeremonyRepo interface {
	PutCeremony(context.Context, string, []byte, time.Duration) error
	TakeCeremony(context.Context, string) ([]byte, error)
}

const (
	passkeyCeremonyKindLogin  = "login"
	passkeyCeremonyKindEnroll = "enroll"
	passkeyCeremonyTTL        = 5 * time.Minute
)

var ErrPasskeyUnavailable = ErrMFAUnavailable

type passkeyCeremony struct {
	Kind          string               `json:"kind"`
	UserID        int64                `json:"user_id"`
	SessionID     string               `json:"session_id"`
	PasswordEpoch int64                `json:"password_epoch"`
	Name          string               `json:"name"`
	Session       webauthn.SessionData `json:"session"`
}

// PasskeyUser adapts an account to the go-webauthn User contract.
type PasskeyUser struct {
	ID      int64
	Name    string
	Display string
	Creds   []PasskeyCredential
}

func (u PasskeyUser) WebAuthnID() []byte          { return []byte(strconv.FormatInt(u.ID, 10)) }
func (u PasskeyUser) WebAuthnName() string        { return u.Name }
func (u PasskeyUser) WebAuthnDisplayName() string { return u.Display }
func (u PasskeyUser) WebAuthnCredentials() []webauthn.Credential {
	creds := make([]webauthn.Credential, 0, len(u.Creds))
	for _, c := range u.Creds {
		transports := make([]protocol.AuthenticatorTransport, 0, len(c.Transport))
		for _, t := range c.Transport {
			transports = append(transports, protocol.AuthenticatorTransport(t))
		}
		creds = append(creds, webauthn.Credential{ID: c.ID, PublicKey: c.PublicKey, AttestationType: c.Attestation, Transport: transports, Authenticator: webauthn.Authenticator{SignCount: c.SignCount}})
	}
	return creds
}

func (uc *IdentityUsecase) SetPasskey(creds PasskeyRepo, ceremonies PasskeyCeremonyRepo) {
	uc.passkeys, uc.passkeyCeremonies = creds, ceremonies
}

// passkeyRP builds the relying-party config from env. The feature stays dark
// until an operator pins both the RP ID and the allowed origin(s).
func (uc *IdentityUsecase) passkeyRP() (*webauthn.WebAuthn, error) {
	rpID := strings.TrimSpace(os.Getenv("IDENTITY_PASSKEY_RP_ID"))
	origins := strings.TrimSpace(os.Getenv("IDENTITY_PASSKEY_ORIGIN"))
	if rpID == "" || origins == "" {
		return nil, ErrPasskeyUnavailable
	}
	display := strings.TrimSpace(os.Getenv("IDENTITY_PASSKEY_DISPLAY_NAME"))
	if display == "" {
		display = "Micro-One API"
	}
	return webauthn.New(&webauthn.Config{RPID: rpID, RPDisplayName: display, RPOrigins: splitPasskeyOrigins(origins)})
}

func (uc *IdentityUsecase) storePasskeyCeremony(ctx context.Context, kind string, userID int64, a authorization.Actor, name string, session webauthn.SessionData) (string, error) {
	if uc.passkeyCeremonies == nil {
		return "", ErrPasskeyUnavailable
	}
	raw, err := jsonx.Marshal(passkeyCeremony{Kind: kind, UserID: userID, SessionID: a.SessionID, PasswordEpoch: a.PasswordEpoch, Name: strings.TrimSpace(name), Session: session})
	if err != nil {
		return "", ErrPasskeyUnavailable
	}
	id := uc.generateToken()
	if err = uc.passkeyCeremonies.PutCeremony(ctx, id, raw, passkeyCeremonyTTL); err != nil {
		return "", err
	}
	return id, nil
}

func (uc *IdentityUsecase) loadPasskeyCeremony(ctx context.Context, id, kind string) (passkeyCeremony, error) {
	var env passkeyCeremony
	if uc.passkeyCeremonies == nil || id == "" {
		return env, ErrMFAInvalid
	}
	raw, err := uc.passkeyCeremonies.TakeCeremony(ctx, id)
	if err != nil {
		return env, err
	}
	if len(raw) > 65536 {
		return env, ErrMFAInvalid
	}
	if err = jsonx.Unmarshal(raw, &env); err != nil || env.Kind != kind {
		return env, ErrMFAInvalid
	}
	return env, nil
}

// BeginPasskeyLogin issues WebAuthn assertion options for a named account.
// Unknown, disabled, or passkey-less accounts get an indistinguishable decoy
// ceremony so the public endpoint cannot enumerate which accounts exist or
// which of them can sign in with a passkey.
func (uc *IdentityUsecase) BeginPasskeyLogin(ctx context.Context, username string) (options, ceremony string, err error) {
	web, err := uc.passkeyRP()
	if err != nil {
		return "", "", err
	}
	if uc.passkeys == nil || !uc.passkeys.Configured() {
		return "", "", ErrPasskeyUnavailable
	}
	u, err := uc.repo.FindUserByUsername(ctx, strings.TrimSpace(username))
	if err != nil || u == nil || u.Status != UserStatusEnabled {
		return uc.decoyPasskeyLoginBegin(ctx, username, web)
	}
	var creds []PasskeyCredential
	err = uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		creds, err = uc.passkeys.List(ctx, tx, u.ID)
		return err
	})
	if err != nil {
		return "", "", err
	}
	if len(creds) == 0 {
		return uc.decoyPasskeyLoginBegin(ctx, username, web)
	}
	assertion, session, err := web.BeginLogin(PasskeyUser{ID: u.ID, Name: u.Username, Creds: creds})
	if err != nil {
		return "", "", ErrMFAUnavailable
	}
	ceremony, err = uc.storePasskeyCeremony(ctx, passkeyCeremonyKindLogin, u.ID, authorization.Actor{UserID: u.ID}, "", *session)
	if err != nil {
		return "", "", err
	}
	raw, err := jsonx.Marshal(assertion.Response)
	if err != nil {
		return "", "", ErrMFAUnavailable
	}
	return string(raw), ceremony, nil
}

// decoyPasskeyLoginBegin returns options+ceremony shaped like the real path
// but bound to user 0, so probing the public begin endpoint learns nothing;
// the ceremony always fails at complete. The fake allowed credential is
// deterministic per username: repeat probes see a stable shape, and an empty
// allowCredentials list never marks an account as passkey-less.
func (uc *IdentityUsecase) decoyPasskeyLoginBegin(ctx context.Context, username string, web *webauthn.WebAuthn) (string, string, error) {
	digest := sha256.Sum256([]byte(strings.TrimSpace(username)))
	assertion, session, err := web.BeginDiscoverableLogin(webauthn.WithAllowedCredentials([]protocol.CredentialDescriptor{{
		Type:         protocol.PublicKeyCredentialType,
		CredentialID: digest[:],
	}}))
	if err != nil {
		return "", "", ErrMFAUnavailable
	}
	ceremony, err := uc.storePasskeyCeremony(ctx, passkeyCeremonyKindLogin, 0, authorization.Actor{}, "", *session)
	if err != nil {
		return "", "", err
	}
	raw, err := jsonx.Marshal(assertion.Response)
	if err != nil {
		return "", "", ErrMFAUnavailable
	}
	return string(raw), ceremony, nil
}

// CompletePasskeyLogin verifies the assertion, then lands on the shared
// primary-authentication tail: accounts with TOTP still owe the second factor.
func (uc *IdentityUsecase) CompletePasskeyLogin(ctx context.Context, ceremony, rawResponse string) (userID int64, token, mfaChallenge string, err error) {
	web, err := uc.passkeyRP()
	if err != nil {
		return 0, "", "", err
	}
	if uc.passkeys == nil || !uc.passkeys.Configured() {
		return 0, "", "", ErrPasskeyUnavailable
	}
	env, err := uc.loadPasskeyCeremony(ctx, ceremony, passkeyCeremonyKindLogin)
	if err != nil {
		return 0, "", "", err
	}
	// Decoy ceremonies are bound to user 0. Reject them before any IAM lookup
	// or assertion parsing so their failure is indistinguishable from a real
	// account's invalid assertion; otherwise the "not found" branch for user 0
	// would leak which accounts own passkeys through a different error code.
	if env.UserID == 0 {
		return 0, "", "", ErrMFAInvalid
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(strings.NewReader(rawResponse))
	if err != nil {
		return 0, "", "", ErrMFAInvalid
	}
	session := env.Session
	var user *User
	err = uc.runtimeWriteEvent(ctx, "credential.passkey.login", strconv.FormatInt(env.UserID, 10), "passkey login", authorization.Actor{UserID: env.UserID}, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		u, err := uc.iam.User(ctx, tx, env.UserID)
		if err != nil {
			return err
		}
		user = &u
		if user.Status != UserStatusEnabled {
			event.Result = "failure"
			event.Diff = `{ "verified": false }`
			return ErrUserDisabled
		}
		creds, err := uc.passkeys.List(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		cred, err := web.ValidateLogin(PasskeyUser{ID: user.ID, Name: user.Username, Creds: creds}, session, parsed)
		if err != nil {
			event.Result = "failure"
			event.Diff = `{ "verified": false }`
			return ErrMFAInvalid
		}
		if cred.Authenticator.CloneWarning {
			// go-webauthn reports a non-advancing sign counter as a warning,
			// not an error; treat it as a cloned authenticator and refuse.
			event.Result = "failure"
			event.Diff = `{ "verified": false, "clone_warning": true }`
			return ErrMFAInvalid
		}
		for i := range creds {
			if subtle.ConstantTimeCompare(creds[i].ID, cred.ID) == 1 {
				creds[i].LastUsedAt = uc.now().UnixMilli()
				creds[i].SignCount = cred.Authenticator.SignCount
				if err = uc.passkeys.Save(ctx, tx, user.ID, creds[i]); err != nil {
					return err
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return 0, "", "", err
	}
	token, err = uc.completePrimaryAuthentication(ctx, user)
	var mfa *MFARequired
	if AsMFARequired(err, &mfa) {
		return user.ID, "", mfa.Challenge, nil
	}
	return user.ID, token, "", err
}

// BeginPasskeyEnrollment starts registering a new authenticator for the
// signed-in account. Like other sensitive changes it demands a fresh step-up.
func (uc *IdentityUsecase) BeginPasskeyEnrollment(ctx context.Context, raw, name string) (options, ceremony string, err error) {
	web, err := uc.passkeyRP()
	if err != nil {
		return "", "", err
	}
	if uc.passkeys == nil || !uc.passkeys.Configured() {
		return "", "", ErrPasskeyUnavailable
	}
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return "", "", err
	}
	var out optionsCeremony
	err = uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		u, err := uc.iam.User(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if err = uc.checkSecuritySession(ctx, tx, a, u); err != nil {
			return err
		}
		// Enrollment adds a persistent authentication factor, so it always
		// demands a fresh proof — like MFA setup — regardless of the
		// IDENTITY_STEP_UP_ENABLED rollout flag.
		if err = uc.requirePasskeyStepUp(ctx, tx, a, u); err != nil {
			return err
		}
		var creds []PasskeyCredential
		if creds, err = uc.passkeys.List(ctx, tx, a.UserID); err != nil {
			return err
		}
		creation, session, err := web.BeginRegistration(PasskeyUser{ID: u.ID, Name: u.Username, Creds: creds})
		if err != nil {
			return ErrMFAUnavailable
		}
		out.session = *session
		out.publicKey = creation.Response
		return nil
	})
	if err != nil {
		return "", "", err
	}
	ceremony, err = uc.storePasskeyCeremony(ctx, passkeyCeremonyKindEnroll, a.UserID, a, name, out.session)
	if err != nil {
		return "", "", err
	}
	rawOpts, err := jsonx.Marshal(out.publicKey)
	if err != nil {
		return "", "", ErrMFAUnavailable
	}
	return string(rawOpts), ceremony, nil
}

type optionsCeremony struct {
	session   webauthn.SessionData
	publicKey any
}

// CompletePasskeyEnrollment binds the attested credential to the account after
// the authenticator signs the challenge.
func (uc *IdentityUsecase) CompletePasskeyEnrollment(ctx context.Context, raw, ceremony, nameParam, rawResponse string) error {
	web, err := uc.passkeyRP()
	if err != nil {
		return err
	}
	if uc.passkeys == nil || !uc.passkeys.Configured() {
		return ErrPasskeyUnavailable
	}
	env, err := uc.loadPasskeyCeremony(ctx, ceremony, passkeyCeremonyKindEnroll)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(strings.NewReader(rawResponse))
	if err != nil {
		return ErrMFAInvalid
	}
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return err
	}
	if env.UserID != a.UserID || env.SessionID != a.SessionID || env.PasswordEpoch != a.PasswordEpoch {
		return ErrMFAInvalid
	}
	session := env.Session
	return uc.runtimeWriteEvent(ctx, "credential.passkey.enroll", strconv.FormatInt(a.UserID, 10), "self passkey enrollment", a, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		u, err := uc.iam.User(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if err = uc.checkSecuritySession(ctx, tx, a, u); err != nil {
			return err
		}
		cred, err := web.CreateCredential(PasskeyUser{ID: u.ID, Name: u.Username, Display: u.Username}, session, parsed)
		if err != nil {
			event.Result = "failure"
			event.Diff = `{ "verified": false }`
			return ErrMFAInvalid
		}
		name := env.Name
		if strings.TrimSpace(name) == "" {
			name = nameParam
		}
		record := PasskeyCredential{ID: cred.ID, PublicKey: cred.PublicKey, Attestation: cred.AttestationType, SignCount: cred.Authenticator.SignCount, CreatedAt: uc.now().UnixMilli(), Name: strings.TrimSpace(name)}
		for _, t := range cred.Transport {
			record.Transport = append(record.Transport, string(t))
		}
		return uc.passkeys.Save(ctx, tx, a.UserID, record)
	})
}

func (uc *IdentityUsecase) ListPasskeys(ctx context.Context, raw string) ([]PasskeyCredential, bool, error) {
	if uc.passkeys == nil {
		return nil, false, ErrPasskeyUnavailable
	}
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return nil, false, err
	}
	if !uc.passkeys.Configured() {
		// Without the encryption key stored credentials cannot be
		// decrypted; degrade to "disabled" instead of failing the request.
		return nil, false, nil
	}
	var creds []PasskeyCredential
	err = uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		creds, err = uc.passkeys.List(ctx, tx, a.UserID)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return creds, uc.passkeys != nil && uc.passkeys.Configured(), nil
}

// DeletePasskey revokes one authenticator. The session's step-up proof is
// re-validated so a stolen bearer token alone cannot strip factors.
func (uc *IdentityUsecase) DeletePasskey(ctx context.Context, raw, credentialID string) error {
	if uc.passkeys == nil || !uc.passkeys.Configured() {
		return ErrPasskeyUnavailable
	}
	a, err := uc.securityActor(ctx, raw)
	if err != nil {
		return err
	}
	id, err := decodeCredentialID(credentialID)
	if err != nil {
		return ErrMFAInvalid
	}
	return uc.runtimeWriteEvent(ctx, "credential.passkey.delete", strconv.FormatInt(a.UserID, 10), "self passkey revocation", a, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		u, err := uc.iam.User(ctx, tx, a.UserID)
		if err != nil {
			return err
		}
		if err = uc.checkSecuritySession(ctx, tx, a, u); err != nil {
			return err
		}
		// Revoking a login factor always demands a fresh proof.
		if err = uc.requirePasskeyStepUp(ctx, tx, a, u); err != nil {
			return err
		}
		return uc.passkeys.Delete(ctx, tx, a.UserID, id)
	})
}

// AsMFARequired unwraps an MFARequired sentinel without importing errors in
// generated-adjacent call sites; the service layer maps it to mfa_required.
func AsMFARequired(err error, target **MFARequired) bool {
	for err != nil {
		if mfa, ok := err.(*MFARequired); ok {
			*target = mfa
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

func decodeCredentialID(encoded string) ([]byte, error) {
	// Credential IDs travel base64url in the WebAuthn browser contract; accept
	// raw bytes too so CLI callers are not forced through an encoder.
	if raw, err := base64.RawURLEncoding.DecodeString(encoded); err == nil && len(raw) > 0 {
		return raw, nil
	}
	if len(encoded) == 0 || len(encoded) > 1024 {
		return nil, ErrMFAInvalid
	}
	return []byte(encoded), nil
}

// requirePasskeyStepUp re-validates the session's step-up proof for passkey
// enrollment/revocation. Unlike requireStepUpTx it is not gated on the
// IDENTITY_STEP_UP_ENABLED rollout flag: adding or removing a persistent
// authentication factor always demands a fresh proof, mirroring ManageMFA.
func (uc *IdentityUsecase) requirePasskeyStepUp(ctx context.Context, tx IAMTx, a authorization.Actor, _ User) error {
	if uc.stepUp == nil {
		return ErrMFAUnavailable
	}
	s, err := uc.security.State(ctx, tx, a.UserID)
	if err != nil {
		return err
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

// splitPasskeyOrigins trims and drops empty ORIGIN entries so a common
// "https://a.example.com, https://b.example.com" config parses cleanly.
func splitPasskeyOrigins(origins string) []string {
	parts := strings.Split(origins, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
