package biz

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/authorization"
	"strings"
)

var ErrManagedWriteRevisionConflict = errors.Conflict(channelv1.ManagedWriteErrorReason_MANAGED_WRITE_REVISION_CONFLICT.String(), "resource revision changed")
var ErrManagedWriteIntentRequired = errors.BadRequest(channelv1.ManagedWriteErrorReason_MANAGED_WRITE_INTENT_REQUIRED.String(), "expected_revision and reason required")

func requireManagedWriteIntent(ctx context.Context, operations ...string) error {
	for _, op := range operations {
		if _, iam := authorization.QueryScopeFromContext(ctx, op); iam {
			if _, present := authorization.ExpectedResourceRevision(ctx); !present || strings.TrimSpace(authorization.WriteReason(ctx)) == "" {
				return ErrManagedWriteIntentRequired
			}
		}
	}
	return nil
}
