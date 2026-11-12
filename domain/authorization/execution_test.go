package authorization

import (
	"slices"
	"testing"
)

// Execution points are compiled into every service that authorizes resource
// writes (admin, identity, channel, billing, ...). A point added here but not
// redeployed to the authorizing service fails at runtime with
// ErrIAMInvalidRelation, which surfaces as a generic authorization denial.
func TestBusinessAuditExecutionPointIsBound(t *testing.T) {
	e, ok := Execution("admin.business_audits")
	if !ok {
		t.Fatal("admin.business_audits must stay declared for the admin audit reader")
	}
	if e.Owner != "admin" {
		t.Errorf("admin.business_audits owner = %q, want admin", e.Owner)
	}
	if !slices.Contains(e.Operations, "iam.audit.read") {
		t.Fatal("admin.business_audits must bind iam.audit.read")
	}
	if !ResourceBound("iam.audit.read") {
		t.Fatal("iam.audit.read must be queryable by the audit execution point")
	}
}

func TestCompletedExecutionDeclarations(t *testing.T) {
	for point, entry := range executionPoints {
		if entry.Owner == "" {
			t.Errorf("%s has no owner", point)
		}
		seen := map[string]bool{}
		for _, code := range entry.Operations {
			if _, ok := Lookup(code); !ok {
				t.Errorf("%s declares unknown operation %s", point, code)
			}
			if seen[code] {
				t.Errorf("%s repeats operation %s", point, code)
			}
			seen[code] = true
		}
	}
	if !ResourceBound("billing.account.cost.read") {
		t.Fatal("delivered independent cost field permission must be queryable by billing")
	}
	for _, code := range []string{"channel.routing_group.members.read", "channel.routing_group.members.update", "channel.routing_group.resource_override.update", "channel.routing_group.enable"} {
		if !ResourceBound(code) {
			t.Errorf("delivered routing group operation %s unbound", code)
		}
	}
	for _, code := range []string{"channel.routing_group.archive", "channel.channel.batch_delete", "channel.channel.export", "log.request.purge", "notify.notification.test", "notify.notification.acknowledge", "billing.report.export", "channel.account.oauth.bind"} {
		if !ResourceBound(code) {
			t.Errorf("delivered B-stage operation %s must be bound", code)
		}
	}
	if ResourceBound("organization.member.invite") {
		t.Fatal("organization operations must remain unbound")
	}
}
