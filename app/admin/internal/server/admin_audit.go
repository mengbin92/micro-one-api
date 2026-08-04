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
//
// The auditor is injected via SetAdminAuditor (called from NewHTTPServer with
// a Wire-provided *audit.Auditor) rather than being a lazy singleton, so the
// enabled flag is configurable and tests can inject a fake.
var (
	adminAuditorOnce sync.Once
	adminAuditorInst *audit.Auditor
)

// SetAdminAuditor injects the audit sink. It is intended to be called once
// during server construction (NewHTTPServer). If never called, a default
// enabled auditor is lazily created on first use for backwards compatibility.
func SetAdminAuditor(a *audit.Auditor) {
	adminAuditorOnce.Do(func() {
		adminAuditorInst = a
	})
}

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
	return audit.ActorFrom(r.Context())
}
