package biz

import (
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"

	"github.com/go-kratos/kratos/v3/errors"
)

var (
	ErrIAMDependencyUnavailable = errors.ServiceUnavailable(identityv1.AuthorizationErrorReason_AUTHORIZATION_DEPENDENCY_UNAVAILABLE.String(), "authorization storage unavailable")
	ErrIAMInvalidRelation       = errors.BadRequest(identityv1.AuthorizationErrorReason_AUTHORIZATION_CONSTRAINT_VIOLATION.String(), "invalid authorization relation")
	ErrIAMNotFound              = errors.NotFound(identityv1.AuthorizationErrorReason_AUTHORIZATION_BUSINESS_RULE_VIOLATION.String(), "authorization relation not found")
	ErrIAMContextInvalid        = errors.BadRequest(identityv1.AuthorizationErrorReason_AUTHORIZATION_CONTEXT_INVALID.String(), "invalid authorization context")
	ErrIAMScopeInvalid          = errors.BadRequest(identityv1.AuthorizationErrorReason_AUTHORIZATION_SCOPE_INVALID.String(), "invalid authorization scope")
	ErrIAMRevisionConflict      = errors.Conflict(identityv1.AuthorizationErrorReason_AUTHORIZATION_REVISION_CONFLICT.String(), "authorization changed; reload and preview again")
	ErrIAMProtected             = errors.Forbidden(identityv1.AuthorizationErrorReason_AUTHORIZATION_PROTECTED_RESOURCE.String(), "protected authorization core")
	ErrIAMCutoverBlocked        = errors.Forbidden(identityv1.AuthorizationErrorReason_AUTHORIZATION_CUTOVER_BLOCKED.String(), "authorization writes blocked")
)

// IAMDecisionError never exposes storage errors or treats an unknown reason as
// success. Transport adapters map these typed errors to HTTP/gRPC status.
func IAMDecisionError(d authorization.Decision) error {
	if d.Allowed {
		return nil
	}
	reason := identityv1.AuthorizationErrorReason_AUTHORIZATION_DEPENDENCY_UNAVAILABLE
	if value, ok := identityv1.AuthorizationErrorReason_value["AUTHORIZATION_"+d.Reason]; ok {
		reason = identityv1.AuthorizationErrorReason(value)
	}
	if reason == identityv1.AuthorizationErrorReason_AUTHORIZATION_IDENTITY_INVALID || reason == identityv1.AuthorizationErrorReason_AUTHORIZATION_SESSION_INVALID {
		return errors.Unauthorized(reason.String(), "authorization identity/session invalid")
	}
	if reason == identityv1.AuthorizationErrorReason_AUTHORIZATION_REVISION_CONFLICT {
		return ErrIAMRevisionConflict
	}
	return errors.Forbidden(reason.String(), "authorization denied")
}
