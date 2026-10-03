package authorization

import (
	"context"
	"fmt"
	"github.com/go-kratos/kratos/v3/errors"
	"strings"
)

var ErrWriteConflict = errors.Conflict("RESOURCE_VERSION_CONFLICT", "resource revision changed")
var ErrWritePrecondition = errors.BadRequest("RESOURCE_WRITE_PRECONDITION", "reason and expected_revision are required")

type expectedRevisionKey struct {
	resource string
	id       int64
}

func WithExpectedRevision(ctx context.Context, resource string, id, revision int64) context.Context {
	return context.WithValue(ctx, expectedRevisionKey{resource, id}, revision)
}
func WithExpectedRevisions(ctx context.Context, resource string, revisions map[int64]int64) context.Context {
	for id, revision := range revisions {
		ctx = WithExpectedRevision(ctx, resource, id, revision)
	}
	return ctx
}
func ExpectedRevision(ctx context.Context, resource string, id int64) int64 {
	revision, _ := ctx.Value(expectedRevisionKey{resource, id}).(int64)
	return revision
}
func CheckWriteRevision(ctx context.Context, resource string, id, revision int64) error {
	expected := ExpectedRevision(ctx, resource, id)
	if strings.TrimSpace(WriteReason(ctx)) == "" || expected <= 0 {
		return fmt.Errorf("%w: %s/%d", ErrWritePrecondition, resource, id)
	}
	if expected != revision {
		return ErrWriteConflict
	}
	return nil
}

func HasExpectedRevision(ctx context.Context, resource string, id int64) bool {
	_, ok := ctx.Value(expectedRevisionKey{resource, id}).(int64)
	return ok
}
