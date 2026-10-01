package authorization

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestScopeContainmentDoesNotSpliceCeilings(t *testing.T) {
	all := Scope{Clauses: []Clause{{All: true}}}
	for _, tt := range []struct {
		name   string
		a, b   Scope
		au, bu int64
		want   bool
	}{
		{"all contains symbolic self", Scope{Clauses: []Clause{{Self: true}}}, all, 0, 9, true},
		{"self is target, not manager", Scope{Clauses: []Clause{{Self: true}}}, Scope{Clauses: []Clause{{UserIDs: []int64{9}}}}, 7, 9, false},
		{"conjunction narrows", Scope{Clauses: []Clause{{ResourceIDs: []int64{1}, RoutingGroupIDs: []int64{2}}}}, Scope{Clauses: []Clause{{RoutingGroupIDs: []int64{2, 3}}}}, 7, 9, true},
		{"independent dimensions cannot splice", Scope{Clauses: []Clause{{ResourceIDs: []int64{1, 2}, RoutingGroupIDs: []int64{3, 4}}}}, Scope{Clauses: []Clause{{ResourceIDs: []int64{1}, RoutingGroupIDs: []int64{3}}, {ResourceIDs: []int64{2}, RoutingGroupIDs: []int64{4}}}}, 7, 9, false},
		{"empty is zero", Scope{Clauses: []Clause{}}, all, 7, 9, true},
		{"unknown is rejected", Scope{}, all, 7, 9, false},
	} {
		t.Run(tt.name, func(t *testing.T) { require.Equal(t, tt.want, ScopeContained(tt.a, tt.b, tt.au, tt.bu)) })
	}
}
