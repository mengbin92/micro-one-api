package iamdto

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
)

// Validate timestamps before conversion; AsTime normalizes invalid protobuf
// values and must never silently normalize authorization validity intervals.
func validTimes(p proto.Message) error {
	var result error
	var walk func(protoreflect.Message)
	walk = func(msg protoreflect.Message) {
		if msg.Descriptor().FullName() == "google.protobuf.Timestamp" {
			if err := msg.Interface().(*timestamppb.Timestamp).CheckValid(); err != nil {
				result = err
			}
			return
		}
		msg.Range(func(f protoreflect.FieldDescriptor, val protoreflect.Value) bool {
			if f.Kind() != protoreflect.MessageKind {
				return true
			}
			if f.IsList() {
				list := val.List()
				for i := 0; i < list.Len(); i++ {
					walk(list.Get(i).Message())
				}
			} else {
				walk(val.Message())
			}
			return result == nil
		})
	}
	walk(p.ProtoReflect())
	return result
}
func ValidateRequest(p *v.IAMRequest) error {
	if p == nil {
		return fmt.Errorf("request required")
	}
	if err := validTimes(p); err != nil {
		return err
	}
	if p.Context == nil {
		p.Context = AuthorizationContextTo(authorization.Platform())
	}
	c := AuthorizationContextFrom(p.Context)
	if c.RequirePlatform() != nil {
		return fmt.Errorf("invalid or unsupported context")
	}
	if p.Id < 0 || p.UserId < 0 || p.SourceId < 0 || p.PageSize < 0 || p.PageSize > 200 || len(p.Assignments) > 200 || len(p.RoleIds) > 200 || len(p.Grants) > 1000 || len(p.Reason) > 4096 || len(p.RequestId) > 128 {
		return fmt.Errorf("invalid IAM request")
	}
	contexts := []*v.IAMRole{p.Role}
	for _, r := range contexts {
		if r != nil && r.Context != nil && AuthorizationContextFrom(r.Context) != c {
			return fmt.Errorf("role context mismatch")
		}
	}
	if p.Delegation != nil && p.Delegation.Context != nil && AuthorizationContextFrom(p.Delegation.Context) != c {
		return fmt.Errorf("delegation context mismatch")
	}
	if p.Constraint != nil && p.Constraint.Context != nil && AuthorizationContextFrom(p.Constraint.Context) != c {
		return fmt.Errorf("constraint context mismatch")
	}
	assignments := append([]*v.IAMAssignment{}, p.Assignments...)
	if p.Assignment != nil {
		assignments = append(assignments, p.Assignment)
	}
	for _, a := range assignments {
		if a == nil || a.Context != nil && AuthorizationContextFrom(a.Context) != c {
			return fmt.Errorf("assignment context mismatch")
		}
	}
	if p.Object != nil && p.Object.Context != nil && AuthorizationContextFrom(p.Object.Context) != c {
		return fmt.Errorf("object context mismatch")
	}
	if p.UpdateMask != nil && len(p.UpdateMask.Paths) > 0 {
		var target proto.Message
		switch {
		case p.Role != nil:
			target = p.Role
		case p.Permission != nil:
			target = p.Permission
		case p.Delegation != nil:
			target = p.Delegation
		case p.Menu != nil:
			target = p.Menu
		case p.Constraint != nil:
			target = p.Constraint
		}
		if target == nil || !p.UpdateMask.IsValid(target) {
			return fmt.Errorf("invalid update mask")
		}
	}
	if strings.TrimSpace(p.Operation) != p.Operation {
		return fmt.Errorf("invalid operation")
	}
	return nil
}
