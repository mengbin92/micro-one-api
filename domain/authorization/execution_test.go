package authorization

import "testing"

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
	for _, code := range []string{"channel.routing_group.archive", "channel.channel.batch_delete", "log.request.purge", "notify.notification.test"} {
		if ResourceBound(code) {
			t.Errorf("unfinished %s must remain unbound", code)
		}
	}
}
