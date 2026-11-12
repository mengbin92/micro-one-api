package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"micro-one-api/pkg/jsonx"
)

type fakePasskeyRepo struct {
	configured bool
	creds      []PasskeyCredential
}

func (f fakePasskeyRepo) Configured() bool { return f.configured }
func (f fakePasskeyRepo) List(context.Context, IAMTx, int64) ([]PasskeyCredential, error) {
	return f.creds, nil
}
func (f fakePasskeyRepo) Save(context.Context, IAMTx, int64, PasskeyCredential) error { return nil }
func (f fakePasskeyRepo) Delete(context.Context, IAMTx, int64, []byte) error          { return nil }

type fakePasskeyCeremonyRepo struct {
	puts int
	raw  []byte
}

func (f *fakePasskeyCeremonyRepo) PutCeremony(context.Context, string, []byte, time.Duration) error {
	f.puts++
	return nil
}
func (f *fakePasskeyCeremonyRepo) TakeCeremony(context.Context, string) ([]byte, error) {
	if f.raw != nil {
		return f.raw, nil
	}
	return nil, errors.New("not found")
}

type fakeIdentityLookupRepo struct {
	IdentityRepo
	user *User
}

func (f fakeIdentityLookupRepo) FindUserByUsername(context.Context, string) (*User, error) {
	return f.user, nil
}

type fakeIAMTxRunner struct{ IAMTxRunner }

func (fakeIAMTxRunner) ReadIAMSnapshot(_ context.Context, fn func(context.Context, IAMTx) error) error {
	return fn(context.Background(), nil)
}

func TestSplitPasskeyOrigins(t *testing.T) {
	got := splitPasskeyOrigins(" https://a.example.com ,https://b.example.com, , ")
	if len(got) != 2 || got[0] != "https://a.example.com" || got[1] != "https://b.example.com" {
		t.Fatalf("origins not trimmed/deduped: %+v", got)
	}
	if splitPasskeyOrigins(" , ") != nil {
		t.Fatal("all-empty origins must yield nil")
	}
}

// The public begin endpoint must answer unknown, disabled, and passkey-less
// accounts with the same options+ceremony shape as a real account, so it
// cannot be used to enumerate which accounts exist or own passkeys.
func TestBeginPasskeyLoginDecoyForUnreachableAccounts(t *testing.T) {
	t.Setenv("IDENTITY_PASSKEY_RP_ID", "example.com")
	t.Setenv("IDENTITY_PASSKEY_ORIGIN", "https://example.com")

	cases := []struct {
		name  string
		user  *User
		creds []PasskeyCredential
	}{
		{"unknown user", nil, nil},
		{"disabled user", &User{ID: 1, Username: "neo", Status: UserStatusDisabled}, nil},
		{"enabled without passkeys", &User{ID: 2, Username: "trinity", Status: UserStatusEnabled}, nil},
		{"enabled with passkeys", &User{ID: 3, Username: "morpheus", Status: UserStatusEnabled}, []PasskeyCredential{{ID: []byte{1, 2, 3}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ceremonies := &fakePasskeyCeremonyRepo{}
			uc := &IdentityUsecase{
				passkeys:          fakePasskeyRepo{configured: true, creds: tc.creds},
				passkeyCeremonies: ceremonies,
				repo:              fakeIdentityLookupRepo{user: tc.user},
				iamRunner:         fakeIAMTxRunner{},
			}
			options, ceremony, err := uc.BeginPasskeyLogin(context.Background(), " whoever ")
			if err != nil {
				t.Fatalf("begin returned error: %v", err)
			}
			if options == "" || ceremony == "" {
				t.Fatalf("begin must return options+ceremony, got options=%q ceremony=%q", options, ceremony)
			}
			if ceremonies.puts != 1 {
				t.Fatalf("expected one stored ceremony, got %d", ceremonies.puts)
			}
		})
	}
}

// Without the RP env the whole feature must stay dark, including the decoy.
func TestBeginPasskeyLoginUnavailableWithoutRPConfig(t *testing.T) {
	uc := &IdentityUsecase{passkeys: fakePasskeyRepo{configured: true}}
	if _, _, err := uc.BeginPasskeyLogin(context.Background(), "neo"); !errors.Is(err, ErrPasskeyUnavailable) {
		t.Fatalf("expected ErrPasskeyUnavailable, got %v", err)
	}
}

// A decoy ceremony is bound to user 0 and must fail with the same sentinel as
// a real account's invalid assertion, without reaching the IAM store (which
// would return a distinguishable "user not found" error). This keeps the
// anti-enumeration decoy effective at the complete endpoint too.
func TestCompletePasskeyLoginRejectsDecoyCeremony(t *testing.T) {
	t.Setenv("IDENTITY_PASSKEY_RP_ID", "example.com")
	t.Setenv("IDENTITY_PASSKEY_ORIGIN", "https://example.com")

	raw, err := jsonx.Marshal(passkeyCeremony{Kind: passkeyCeremonyKindLogin, UserID: 0})
	if err != nil {
		t.Fatal(err)
	}
	uc := &IdentityUsecase{
		passkeys:          fakePasskeyRepo{configured: true},
		passkeyCeremonies: &fakePasskeyCeremonyRepo{raw: raw},
	}
	_, _, _, err = uc.CompletePasskeyLogin(context.Background(), "decoy-ceremony", "{}")
	if !errors.Is(err, ErrMFAInvalid) {
		t.Fatalf("decoy ceremony error = %v, want ErrMFAInvalid", err)
	}
}
