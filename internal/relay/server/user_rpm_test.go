package server

import (
	"context"
	"errors"
	"testing"
)

func TestHTTPServerUserRPMDefaultLimitIsDisabledInUnitConstructor(t *testing.T) {
	srv := NewHTTPServer(nil, nil, nil, nil, nil)
	for i := 0; i < 10; i++ {
		if err := srv.checkUserRPM(context.Background(), 42); err != nil {
			t.Fatalf("checkUserRPM() with unset limit error = %v", err)
		}
	}
}

func TestHTTPServerUserRPMEnforcesConfiguredLimit(t *testing.T) {
	srv := NewHTTPServer(nil, nil, nil, nil, nil)
	srv.SetUserRPMLimit(3)

	for i := 0; i < 3; i++ {
		if err := srv.checkUserRPM(context.Background(), 42); err != nil {
			t.Fatalf("request %d should pass, got %v", i+1, err)
		}
	}
	if err := srv.checkUserRPM(context.Background(), 42); !errors.Is(err, errUserRPMLimited) {
		t.Fatalf("fourth request error = %v, want errUserRPMLimited", err)
	}
}
