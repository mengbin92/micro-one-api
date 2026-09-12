package service

import (
	"fmt"
	"testing"

	kerrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/status"
	"micro-one-api/app/identity/internal/biz"
)

func TestRoutingErrorReasonSurvivesGRPC(t *testing.T) {
	for _, domainErr := range []*kerrors.Error{biz.ErrRoutingFactsUnavailable, biz.ErrRoutingDefaultInvalid, biz.ErrRoutingAccessConflict} {
		mapped := mapIdentityErrorToGRPC(fmt.Errorf("routing: %w", domainErr))
		wire := status.Convert(mapped).Err()
		require.Equal(t, domainErr.Code, kerrors.FromError(wire).Code)
		require.Equal(t, domainErr.Reason, kerrors.FromError(wire).Reason)
	}
}
