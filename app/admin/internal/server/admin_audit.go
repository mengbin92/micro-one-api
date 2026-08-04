package server

import (
	"net/http"
	"sync"

	"micro-one-api/platform/audit"
)

// adminAuditor is the admin-api audit sink. It is unconditionally enabled
// (events go to the structured application log); a config gate can be layered
// on later without changing call sites. Unlike relay-gateway's full HTTP
// middleware, admin uses it for explicit sensitive-operation records
// (refunds, balance-affecting actions), where actor + request_id matter most.
var (
	adminAuditorOnce sync.Once
	adminAuditorInst *audit.Auditor
)

func adminAuditor() *audit.Auditor {
	adminAuditorOnce.Do(func() {
		adminAuditorInst = audit.NewAuditor(true)
	})
	return adminAuditorInst
}

// adminActorFromRequest reads the actor stamped by the admin guard. It is
// never empty after newAdminGuard runs, so sensitive-operation audit records
// always carry the real operator.
func adminActorFromRequest(r *http.Request) audit.ActorInfo {
	if r == nil {
		return audit.ActorInfo{}
	}
	return audit.ActorFrom(r.Context())
}
