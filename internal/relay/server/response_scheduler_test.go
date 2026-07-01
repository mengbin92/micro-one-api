package server

import (
	"context"
	"errors"
	"testing"

	identityv1 "micro-one-api/api/identity/v1"
	relaybiz "micro-one-api/internal/relay/biz"

	"google.golang.org/grpc"
)

type schedulerPlannerStub struct {
	plan  *relaybiz.RelayPlan
	err   error
	calls int
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
		responseRoutes: map[string]responseRoute{
			"resp_123": {Model: "gpt-5"},
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

func TestOpenAIWSRoutingSchedulerRejectsSessionRouteWhenModelNotAllowed(t *testing.T) {
	ctx := context.Background()
	store := newOpenAIWSStickyStore(nil)
	store.BindSessionChannel(ctx, "default", "session-a", 99, openAIWSStickyTTL)
	planner := &schedulerPlannerStub{plan: &relaybiz.RelayPlan{ResolvedModel: "gpt-4o"}}
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
	want := &relaybiz.RelayPlan{ResolvedModel: "gpt-4o"}
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
	store.BindSessionChannel(ctx, "default", "session-a", 99, openAIWSStickyTTL)
	planner := &schedulerPlannerStub{plan: &relaybiz.RelayPlan{ResolvedModel: "gpt-4o"}}
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

	if got := store.LookupSessionChannel(ctx, "default", "session-a"); got != 77 {
		t.Fatalf("session channel = %d, want 77", got)
	}
}
