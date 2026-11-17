package data

import (
	"bytes"
	"context"
	"fmt"
	dbtest "micro-one-api/platform/database/testutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
)

func observationTestRepo(t *testing.T) biz.UsageObservationRepo {
	t.Helper()
	if driver := os.Getenv("USAGE_OBSERVATION_TEST_DRIVER"); driver != "" {
		return NewUsageObservationRepo(&Repository{db: dbtest.RoutingContextDB(t, driver)})
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&usageObservationModel{}))
	return NewUsageObservationRepo(&Repository{db: db})
}

func logObservationRow(id string, userID int64, at int64) *biz.UsageObservationRow {
	return &biz.UsageObservationRow{
		EventID: id, SendID: "obs/" + id, ObservationID: "obs-" + id, Category: "terminal", Sequence: 1,
		PayloadSHA256: "sha-" + id, Payload: []byte(`{"version":1}`), UserID: userID, Protocol: "openai_chat",
		SourceKind: "channel", SourceID: 4, Purpose: "user", UpstreamModelID: "exact", Capture: "complete",
		Status: "verified", ParserRevision: "p", ContractRevision: "c", HTTPStatus: 200, ObservedAtMS: at, ReceivedAtMS: at,
	}
}

func TestUsageObservationRepoIsIdempotentAndRejectsPayloadConflict(t *testing.T) {
	repo := observationTestRepo(t)
	ctx := context.Background()
	row := logObservationRow("e1", 7, 1_000_000)
	duplicate, err := repo.Record(ctx, row)
	require.NoError(t, err)
	require.False(t, duplicate)

	duplicate, err = repo.Record(ctx, row)
	require.NoError(t, err)
	require.True(t, duplicate, "same id and digest must be idempotent")

	changed := logObservationRow("e1", 7, 1_000_000)
	changed.PayloadSHA256 = "different"
	changed.Payload = []byte(`{"version":1,"tampered":true}`)
	_, err = repo.Record(ctx, changed)
	require.ErrorIs(t, err, biz.ErrUsageObservationConflict)
}

func TestUsageObservationRepoQueriesByUserAndWindow(t *testing.T) {
	repo := observationTestRepo(t)
	ctx := context.Background()
	for _, row := range []*biz.UsageObservationRow{
		logObservationRow("a", 7, 1_000_000),
		logObservationRow("b", 7, 2_000_000),
		logObservationRow("c", 8, 1_500_000),
	} {
		_, err := repo.Record(ctx, row)
		require.NoError(t, err)
	}
	rows, next, err := repo.ListUsageObservations(ctx, biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 3000_000, UserID: 7})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Empty(t, next)
	require.Equal(t, "b", rows[0].EventID, "newest first")

	rows, next, err = repo.ListUsageObservations(ctx, biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 3000_000, UserID: 7, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotEmpty(t, next)
	rows, _, err = repo.ListUsageObservations(ctx, biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 3000_000, UserID: 7, PageSize: 1, PageToken: next})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "a", rows[0].EventID)

	rows, _, err = repo.ListUsageObservations(ctx, biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 3000_000, Capture: "complete", UserID: 7})
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

func TestUsageObservationRepoRetentionDeletesOnlyOldRows(t *testing.T) {
	repo := observationTestRepo(t)
	ctx := context.Background()
	old := logObservationRow("old", 7, 1_000_000)
	old.ReceivedAtMS = time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	recent := logObservationRow("new", 7, 2_000_000)
	recent.ReceivedAtMS = time.Now().UnixMilli()
	_, err := repo.Record(ctx, old)
	require.NoError(t, err)
	_, err = repo.Record(ctx, recent)
	require.NoError(t, err)
	deleted, err := repo.DeleteObservationsBefore(ctx, time.Now().Add(-7*24*time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	rows, _, err := repo.ListUsageObservations(ctx, biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 3000_000, UserID: 7})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "new", rows[0].EventID)
}

func scopedObservationContext(actor int64, allow, deny []authorization.Scope) context.Context {
	return authorization.WithQueryScope(context.Background(), "log.request.list", authorization.QueryScope{ActorID: actor, Allow: allow, Deny: deny})
}

func selfScope() []authorization.Scope {
	return []authorization.Scope{{Clauses: []authorization.Clause{{Self: true}}}}
}

func scopedObservationDetailContext(actor int64, allow, deny []authorization.Scope) context.Context {
	q := authorization.QueryScope{ActorID: actor, Allow: allow, Deny: deny}
	ctx := authorization.WithQueryScope(context.Background(), "log.request.read", q)
	return authorization.WithQueryScope(ctx, "log.request.content.read", q)
}

func TestUsageObservationQueryScopeIsAppliedInSQL(t *testing.T) {
	repo := observationTestRepo(t)
	ctx := context.Background()
	for _, row := range []*biz.UsageObservationRow{
		logObservationRow("mine", 7, 1_000_000),
		logObservationRow("theirs", 8, 1_100_000),
	} {
		_, err := repo.Record(ctx, row)
		require.NoError(t, err)
	}
	window := biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 2_000_000}

	selfRows, _, err := repo.ListUsageObservations(scopedObservationContext(7, selfScope(), nil), window)
	require.NoError(t, err)
	require.Len(t, selfRows, 1)
	require.Equal(t, "mine", selfRows[0].EventID)

	// allow users=[8] for actor 7 must read user 8, not fall back to self.
	userRows, _, err := repo.ListUsageObservations(scopedObservationContext(7, []authorization.Scope{{Clauses: []authorization.Clause{{UserIDs: []int64{8}}}}}, nil), window)
	require.NoError(t, err)
	require.Len(t, userRows, 1)
	require.Equal(t, "theirs", userRows[0].EventID)

	// allow all + deny self: the deny must be enforced in SQL, not ignored.
	denyRows, _, err := repo.ListUsageObservations(scopedObservationContext(7, []authorization.Scope{{Clauses: []authorization.Clause{{All: true}}}}, selfScope()), window)
	require.NoError(t, err)
	require.Len(t, denyRows, 1)
	require.Equal(t, "theirs", denyRows[0].EventID)

	// A resource-id scope cannot be expressed against this table: fail closed.
	_, _, err = repo.ListUsageObservations(scopedObservationContext(7, []authorization.Scope{{Clauses: []authorization.Clause{{ResourceIDs: []int64{1}}}}}, nil), window)
	require.Error(t, err)

	// Without a scope the repository applies no predicate; the biz layer owns
	// the legacy denial (an unscoped read is rejected there).
	_, _, err = repo.ListUsageObservations(ctx, window)
	require.NoError(t, err)
}

func TestUsageObservationGetRespectsScope(t *testing.T) {
	repo := observationTestRepo(t)
	ctx := context.Background()
	_, err := repo.Record(ctx, logObservationRow("mine", 7, 1_000_000))
	require.NoError(t, err)
	_, err = repo.Record(ctx, logObservationRow("theirs", 8, 1_100_000))
	require.NoError(t, err)

	self := scopedObservationDetailContext(7, selfScope(), nil)
	row, err := repo.GetUsageObservation(self, "mine")
	require.NoError(t, err)
	require.Equal(t, "mine", row.EventID)
	_, err = repo.GetUsageObservation(self, "theirs")
	require.Error(t, err)
}

func TestUsageObservationWindowIsExactToTheMillisecond(t *testing.T) {
	repo := observationTestRepo(t)
	ctx := context.Background()
	for _, at := range []int64{999_999, 1_000_000, 1_000_001, 1_999_999, 2_000_000} {
		row := logObservationRow(fmt.Sprintf("e%d", at), 7, at)
		_, err := repo.Record(ctx, row)
		require.NoError(t, err)
	}
	scope := scopedObservationContext(7, selfScope(), nil)
	rows, _, err := repo.ListUsageObservations(scope, biz.UsageObservationFilter{StartTimeMS: 1_000_000, EndTimeMS: 2_000_000})
	require.NoError(t, err)
	var got []int64
	for _, row := range rows {
		got = append(got, row.ObservedAtMS)
	}
	require.Equal(t, []int64{1_999_999, 1_000_001, 1_000_000}, got)
}

func TestUsageObservationInMemoryStorageRefusesAck(t *testing.T) {
	repo := NewUsageObservationRepo(&Repository{})
	_, err := repo.Record(context.Background(), logObservationRow("e1", 7, 1_000_000))
	require.ErrorIs(t, err, biz.ErrUsageObservationUnavailable)
}

func TestUsageObservationListSummariesAndGetPayload(t *testing.T) {
	repo := observationTestRepo(t)
	ctx := context.Background()
	row := logObservationRow("e1", 7, 1_000_000)
	row.Payload = bytes.Repeat([]byte("x"), 120<<10)
	_, err := repo.Record(ctx, row)
	require.NoError(t, err)
	scope := scopedObservationContext(7, selfScope(), nil)

	rows, _, err := repo.ListUsageObservations(scope, biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 2_000_000})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].Payload, "list must not load payload bytes")

	detail, err := repo.GetUsageObservation(scopedObservationDetailContext(7, selfScope(), nil), "e1")
	require.NoError(t, err)
	require.Len(t, detail.Payload, 120<<10)
}

func TestUsageObservationExpiredScopeIsDenied(t *testing.T) {
	repo := observationTestRepo(t)
	_, err := repo.Record(context.Background(), logObservationRow("mine", 7, 1_000_000))
	require.NoError(t, err)
	expired := authorization.WithQueryScope(context.Background(), "log.request.list", authorization.QueryScope{
		ActorID: 7, Allow: selfScope(), ValidUntil: time.Now().Add(-time.Minute),
	})
	_, _, err = repo.ListUsageObservations(expired, biz.UsageObservationFilter{StartTimeMS: 900_000, EndTimeMS: 2_000_000})
	require.Error(t, err, "an expired scope must fail closed")
}

func TestUsageObservationDetailRequiresReadAndContentScopes(t *testing.T) {
	repo := observationTestRepo(t)
	_, err := repo.Record(context.Background(), logObservationRow("mine", 7, 1_000_000))
	require.NoError(t, err)
	uc := biz.NewUsageObservationUsecase(repo)
	policy := &authztest.Resolver{ActorID: 7, Scopes: map[string]authorization.QueryScope{
		"log.request.list": authztest.All(),
	}}
	uc.SetAuthorization(policy)
	ctx := authztest.Context()

	row, err := uc.GetUsageObservation(ctx, "mine")
	require.Error(t, err, "a list grant must not expose the canonical evidence payload")
	require.Nil(t, row)

	policy.Scopes["log.request.read"] = authztest.Users(7)
	row, err = uc.GetUsageObservation(ctx, "mine")
	require.ErrorIs(t, err, biz.ErrUsageObservationNotFound, "payload access requires independent content authority")
	require.Nil(t, row)

	policy.Scopes["log.request.content.read"] = authztest.Users(8)
	row, err = uc.GetUsageObservation(ctx, "mine")
	require.ErrorIs(t, err, biz.ErrUsageObservationNotFound, "read and content scopes must intersect")
	require.Nil(t, row)

	policy.Scopes["log.request.content.read"] = authztest.Users(7)
	delete(policy.Scopes, "log.request.list")
	row, err = uc.GetUsageObservation(ctx, "mine")
	require.NoError(t, err, "detail authority does not depend on a list grant")
	require.NotEmpty(t, row.Payload)
}

func TestUsageObservationMalformedCursorIsRejected(t *testing.T) {
	repo := observationTestRepo(t)
	_, err := repo.Record(context.Background(), logObservationRow("mine", 7, 1_000_000))
	require.NoError(t, err)
	for _, token := range []string{"broken", "not-a-time:mine", "0:mine", "-1:mine", "1000000:", "1000000:" + string(make([]byte, 256))} {
		t.Run(token[:min(len(token), 20)], func(t *testing.T) {
			_, _, err := repo.ListUsageObservations(context.Background(), biz.UsageObservationFilter{
				StartTimeMS: 900_000, EndTimeMS: 2_000_000, PageToken: token,
			})
			require.ErrorIs(t, err, biz.ErrInvalidUsageObservation, "malformed cursors must not restart an export at page one")
		})
	}
}
