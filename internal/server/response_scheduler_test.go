package server

import (
	"context"
	"errors"
	"testing"
	"time"

	identityv1 "micro-one-api/api/identity/v1"
	relaybiz "micro-one-api/internal/biz"

	"google.golang.org/grpc"
)

type schedulerPlannerStub struct {
	plan  *relaybiz.RelayPlan
	err   error
	calls int
}

type wsStickySubscriptionClient struct {
	account *relaybiz.SubscriptionAccount
}

func (*wsStickySubscriptionClient) SelectChannel(context.Context, string, string, bool) (*relaybiz.Channel, error) {
	return nil, errors.New("no ordinary channel")
}

func (*wsStickySubscriptionClient) SelectChannelExcluding(context.Context, string, string, map[int64]bool) (*relaybiz.Channel, error) {
	return nil, errors.New("no ordinary channel")
}

func (*wsStickySubscriptionClient) RecordChannelHealth(context.Context, int64, bool, string, int64) error {
	return nil
}

func (*wsStickySubscriptionClient) RecordSubscriptionAccountHealth(context.Context, int64, bool) error {
	return nil
}

func (c *wsStickySubscriptionClient) SelectSubscriptionAccount(context.Context, string, string, string, bool) (*relaybiz.SubscriptionAccount, error) {
	return c.account, nil
}

func (c *wsStickySubscriptionClient) GetSubscriptionAccountByID(_ context.Context, accountID int64) (*relaybiz.SubscriptionAccount, error) {
	if c.account != nil && c.account.ID == accountID {
		return c.account, nil
	}
	return nil, nil
}

func (s *schedulerPlannerStub) Plan(ctx context.Context, req relaybiz.RelayRequest) (*relaybiz.RelayPlan, error) {
	s.calls++
	return s.plan, s.err
}

type rawIdentityClientWithAllowedModels struct {
	rawIdentityClient
	allowedModels []string
}

func (c rawIdentityClientWithAllowedModels) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	reply, err := c.rawIdentityClient.GetAuthSnapshot(ctx, req, opts...)
	if err != nil {
		return nil, err
	}
	reply.AllowedModels = c.allowedModels
	return reply, nil
}

func TestOpenAIWSRoutingSchedulerResolveStoredRoute(t *testing.T) {
	srv := &HTTPServer{
		identityClient: rawIdentityClient{},
		responseRoutes: map[string]responseRouteEntry{
			"resp_123": {route: responseRoute{Model: "gpt-5"}, expiresAt: time.Now().Add(time.Hour)},
		},
	}
	sched := NewOpenAIWSRoutingScheduler(srv)

	plan, ok := sched.ResolveStoredRoute(context.Background(), "token", "gpt-5", "resp_123")
	if !ok || plan == nil {
		t.Fatal("expected stored route")
	}
	if plan.ResolvedModel != "gpt-5" {
		t.Fatalf("resolved model = %q, want gpt-5", plan.ResolvedModel)
	}
}

func TestOpenAIWSRoutingSchedulerRouteModels(t *testing.T) {
	mapper := relaybiz.NewModelMapperForTest(map[string]*relaybiz.ModelEntry{
		"client-model": {ActualName: "global-model"},
		"*":            {ActualName: "catch-all"},
	})
	sched := &OpenAIWSRoutingScheduler{server: &HTTPServer{
		relayUsecase: relaybiz.NewRelayUsecase(nil, nil, mapper, nil),
	}}

	t.Run("stored metadata wins", func(t *testing.T) {
		global, resolved := sched.routeModels(responseRoute{
			GlobalModel:   "stored-global",
			ResolvedModel: "stored-upstream",
		}, "client-model")
		if global != "stored-global" || resolved != "stored-upstream" {
			t.Fatalf("route models = %q/%q, want stored-global/stored-upstream", global, resolved)
		}
	})

	t.Run("stored metadata drops extended context suffix", func(t *testing.T) {
		global, resolved := sched.routeModels(responseRoute{
			GlobalModel:   "deepseek-v4-pro-0813[1M]",
			ResolvedModel: "DeepSeek-V4-Pro-0813[1m]",
		}, "deepseek-v4-pro-0813[1M]")
		if global != "deepseek-v4-pro-0813" || resolved != "DeepSeek-V4-Pro-0813" {
			t.Fatalf("route models = %q/%q, want suffix-free models", global, resolved)
		}
	})

	t.Run("legacy route rebuilds both mapping stages", func(t *testing.T) {
		global, resolved := sched.routeModels(responseRoute{
			Model:   "client-model",
			Channel: relaybiz.Channel{ModelMapping: `{"global-model":"channel-model"}`},
		}, "")
		if global != "global-model" || resolved != "channel-model" {
			t.Fatalf("route models = %q/%q, want global-model/channel-model", global, resolved)
		}
	})

	t.Run("missing model stays empty", func(t *testing.T) {
		global, resolved := sched.routeModels(responseRoute{
			Channel: relaybiz.Channel{ModelMapping: `{"*":"channel-catch-all"}`},
		}, "")
		if global != "" || resolved != "" {
			t.Fatalf("route models = %q/%q, want both empty", global, resolved)
		}
	})
}

func TestAuthAllowsModelIgnoresCaseAndExtendedContextSuffix(t *testing.T) {
	if !authAllowsModel([]string{"deepseek-v4-pro-0813"}, "DeepSeek-V4-Pro-0813[1M]") {
		t.Fatal("expected case-insensitive suffix-free permission match")
	}
}

func TestMaterializeWSStickySourcePreservesChannelModelSpelling(t *testing.T) {
	srv := &HTTPServer{channelClient: rawChannelClient{getModels: "DeepSeek-V4-Pro-0813"}}
	var route responseRoute
	ok := srv.materializeWSStickySource(context.Background(), &identityv1.GetAuthSnapshotReply{
		UserId: 42, Group: "default",
	}, "deepseek-v4-pro-0813[1M]", openAIWSStickySource{kind: relaybiz.UpstreamRouteChannel, id: 11}, &route)
	if !ok {
		t.Fatal("expected sticky source to materialize")
	}
	if got := relaybiz.ResolveChannelModel(&route.Channel, "deepseek-v4-pro-0813[1M]"); got != "DeepSeek-V4-Pro-0813" {
		t.Fatalf("resolved model = %q, want channel spelling", got)
	}
}

func TestOpenAIWSRoutingSchedulerRejectsSessionRouteWhenModelNotAllowed(t *testing.T) {
	ctx := context.Background()
	store := newOpenAIWSStickyStore(nil)
	store.BindSessionRoute(ctx, "default", "session-a", &relaybiz.Channel{ID: 99}, openAIWSStickyTTL)
	planner := &schedulerPlannerStub{plan: &relaybiz.RelayPlan{GlobalModel: "gpt-4o", ResolvedModel: "gpt-4o"}}
	sched := &OpenAIWSRoutingScheduler{
		server: &HTTPServer{
			identityClient: rawIdentityClientWithAllowedModels{allowedModels: []string{"gpt-4o"}},
			channelClient:  rawChannelClient{},
			wsSticky:       store,
		},
		planner: planner,
	}

	_, ok := sched.ResolveSessionRoute(ctx, "token", "gpt-5", "session-a")
	if ok {
		t.Fatal("expected session route to be rejected for disallowed model")
	}
}

func TestOpenAIWSRoutingSchedulerResolvePlanFallsBackToPlanner(t *testing.T) {
	want := &relaybiz.RelayPlan{GlobalModel: "gpt-4o", ResolvedModel: "gpt-4o"}
	planner := &schedulerPlannerStub{plan: want}
	sched := &OpenAIWSRoutingScheduler{
		server:  &HTTPServer{identityClient: rawIdentityClient{}},
		planner: planner,
	}

	plan, err := sched.ResolvePlan(context.Background(), "token", "gpt-4o", "", "")
	if err != nil {
		t.Fatalf("ResolvePlan error: %v", err)
	}
	if plan != want {
		t.Fatalf("plan = %#v, want %#v", plan, want)
	}
	if planner.calls != 1 {
		t.Fatalf("planner calls = %d, want 1", planner.calls)
	}
}

func TestOpenAIWSRoutingSchedulerResolvePlanPropagatesPlannerError(t *testing.T) {
	sched := &OpenAIWSRoutingScheduler{
		server:  &HTTPServer{identityClient: rawIdentityClient{}},
		planner: &schedulerPlannerStub{err: errors.New("boom")},
	}

	_, err := sched.ResolvePlan(context.Background(), "token", "gpt-4o", "", "")
	if err == nil {
		t.Fatal("expected planner error")
	}
}

func TestOpenAIWSRoutingSchedulerResolvePlanUsesSessionRouteBeforePlanner(t *testing.T) {
	ctx := context.Background()
	store := newOpenAIWSStickyStore(nil)
	store.BindSessionRoute(ctx, "default", "session-a", &relaybiz.Channel{ID: 99}, openAIWSStickyTTL)
	planner := &schedulerPlannerStub{plan: &relaybiz.RelayPlan{GlobalModel: "gpt-4o", ResolvedModel: "gpt-4o"}}
	sched := &OpenAIWSRoutingScheduler{
		server: &HTTPServer{
			identityClient: rawIdentityClient{},
			channelClient:  rawChannelClient{},
			wsSticky:       store,
		},
		planner: planner,
	}

	plan, err := sched.ResolvePlan(ctx, "token", "gpt-4o", "", "session-a")
	if err != nil {
		t.Fatalf("ResolvePlan error: %v", err)
	}
	if plan == nil || plan.Channel == nil || plan.Channel.ID != 99 {
		t.Fatalf("expected sticky channel 99, got %#v", plan)
	}
	if planner.calls != 0 {
		t.Fatalf("planner calls = %d, want 0", planner.calls)
	}
}

func TestOpenAIWSRoutingSchedulerMaterializesSubscriptionStickyNamespace(t *testing.T) {
	ctx := context.Background()
	store := newOpenAIWSStickyStore(nil)
	store.BindSessionRoute(ctx, "default", "session-sub", &relaybiz.Channel{ID: 7, SubscriptionAccountID: 7}, openAIWSStickyTTL)
	sourceClient := &wsStickySubscriptionClient{account: &relaybiz.SubscriptionAccount{
		ID:          7,
		Name:        "subscription-7",
		Platform:    "codex",
		Status:      1,
		Group:       "default",
		Models:      []string{"gpt-4o"},
		AccessToken: "secret",
	}}
	sched := &OpenAIWSRoutingScheduler{server: &HTTPServer{
		identityClient: rawIdentityClient{},
		relayUsecase:   relaybiz.NewRelayUsecase(nil, sourceClient, nil, nil),
		wsSticky:       store,
	}}

	plan, ok := sched.ResolveSessionRoute(ctx, "token", "gpt-4o", "session-sub")
	if !ok {
		t.Fatal("expected subscription sticky route to resolve")
	}
	if plan.Account == nil || plan.Account.ID != 7 {
		t.Fatalf("resolved account = %+v, want subscription account 7", plan.Account)
	}
	if plan.Channel == nil || plan.Channel.SubscriptionAccountID != 7 {
		t.Fatalf("resolved channel = %+v, want subscription projection 7", plan.Channel)
	}
}

func TestOpenAIWSRoutingSchedulerBindSession(t *testing.T) {
	ctx := context.Background()
	store := newOpenAIWSStickyStore(nil)
	sched := &OpenAIWSRoutingScheduler{
		server: &HTTPServer{wsSticky: store},
	}
	sched.BindSession(ctx, &relaybiz.RelayPlan{
		Auth:    &relaybiz.AuthSnapshot{Group: "default"},
		Channel: &relaybiz.Channel{ID: 77},
	}, "session-a")

	got := store.LookupSessionRoute(ctx, "default", "session-a")
	if got.kind != relaybiz.UpstreamRouteChannel || got.id != 77 {
		t.Fatalf("session source = %s:%d, want channel:77", got.kind.String(), got.id)
	}
}
