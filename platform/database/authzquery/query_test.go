package authzquery

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/domain/authorization"
)

func TestSQLMatchesPureScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE objects (id INTEGER PRIMARY KEY, user_id INTEGER)").Error)
	require.NoError(t, db.Exec("CREATE TABLE memberships (object_id INTEGER, group_id INTEGER)").Error)
	objects := []authorization.ObjectFacts{{Context: authorization.Platform(), ResourceID: 1, OwnerUserID: 10, RoutingGroupIDs: []int64{1, 2}}, {Context: authorization.Platform(), ResourceID: 2, OwnerUserID: 20, RoutingGroupIDs: []int64{2}}, {Context: authorization.Platform(), ResourceID: 3, OwnerUserID: 10}, {Context: authorization.Platform(), ResourceID: 4, OwnerUserID: 30, RoutingGroupIDs: []int64{3}}}
	for _, o := range objects {
		require.NoError(t, db.Exec("INSERT INTO objects VALUES (?,?)", o.ResourceID, o.OwnerUserID).Error)
		for _, g := range o.RoutingGroupIDs {
			require.NoError(t, db.Exec("INSERT INTO memberships VALUES (?,?)", o.ResourceID, g).Error)
		}
	}
	scopes := []authorization.Scope{{Clauses: []authorization.Clause{{All: true}}}, {Clauses: []authorization.Clause{{Self: true}}}, {Clauses: []authorization.Clause{{UserIDs: []int64{10}, RoutingGroupIDs: []int64{2}}}}, {Clauses: []authorization.Clause{{ResourceIDs: []int64{1, 4}}}}, {Clauses: []authorization.Clause{{RoutingGroupIDs: []int64{2}}}}, {Clauses: []authorization.Clause{{UserIDs: []int64{10}}, {RoutingGroupIDs: []int64{3}}}}, {}}
	cols := Columns{Resource: "objects.id", User: "objects.user_id", Groups: "SELECT 1 FROM memberships WHERE memberships.object_id = objects.id AND memberships.group_id IN ?"}
	for _, allow := range scopes {
		for _, deny := range scopes {
			q := authorization.QueryScope{ActorID: 10, Allow: []authorization.Scope{allow}, Deny: []authorization.Scope{deny}}
			query, err := Apply(db.Table("objects"), q, cols)
			require.NoError(t, err)
			var got []int64
			require.NoError(t, query.Order("objects.id").Pluck("objects.id", &got).Error)
			want := []int64{}
			for _, o := range objects {
				if q.Matches(o, false) {
					want = append(want, o.ResourceID)
				}
			}
			require.Equal(t, want, got)
		}
	}
	_, err = Apply(db.Table("objects"), authorization.QueryScope{ActorID: 10, Allow: []authorization.Scope{{Clauses: []authorization.Clause{{RoutingGroupIDs: []int64{1}}}}}}, Columns{Resource: "id"})
	require.Error(t, err)
}
