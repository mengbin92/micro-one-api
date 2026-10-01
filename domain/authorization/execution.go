package authorization

import "slices"

// ExecutionPoint declarations are code-owned resource entry points. A browser
// cannot publish one by sending an operation or fabricated object facts.
type ExecutionPoint struct {
	Owner      string
	Operations []string
}

var executionPoints = map[string]ExecutionPoint{
	"admin.console":           {"admin", []string{"admin.console.enter"}},
	"identity.routing_access": {"identity", []string{"identity.routing_access.read", "identity.routing_access.grant", "identity.routing_access.revoke", "identity.routing_access.default.update", "identity.routing_access.public_access.update"}},
	"identity.users.create":   {"identity", []string{"identity.user.create", "identity.routing_access.default.update", "identity.routing_access.grant"}},
	"identity.users.delete":   {"identity", []string{"identity.user.delete"}},
	"identity.users.list":     {"identity", []string{"identity.user.list", "identity.user.contact.read"}},
	"identity.users.read":     {"identity", []string{"identity.user.read", "identity.user.contact.read"}},
	"identity.users.update":   {"identity", []string{"identity.user.update", "identity.user.enable", "identity.user.disable", "identity.user.email_binding.update", "identity.user.credential.update", "identity.routing_access.default.update", "identity.routing_access.grant", "identity.routing_access.revoke"}},
}

func Execution(code string) (ExecutionPoint, bool) {
	e, ok := executionPoints[code]
	e.Operations = slices.Clone(e.Operations)
	return e, ok
}
func ResourceBound(operation string) bool {
	for _, e := range executionPoints {
		if slices.Contains(e.Operations, operation) {
			return true
		}
	}
	return false
}

type ResourceRequest struct {
	ExecutionPoint, Operation string
	Object                    *ObjectFacts
}
type ResourceAuthorization struct {
	Mode     string
	Query    QueryScope
	Decision *Decision
}
