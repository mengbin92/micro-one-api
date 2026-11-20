# domain/subscription — Shared Subscription Domain Library

## Purpose

The legacy `SubscriptionGroup` name means a **subscription quota policy**
(订阅额度策略). Its numeric ID is independent of the string routing groups on
users, channels, and upstream accounts. Assigning a subscription does not change
a user's routing group. `RateMultiplier` scales subscription-window consumption;
it is separate from billing's routing-group price ratio (`GroupRatio`).
See [group concepts and implementation](../../docs/design/group-concepts-and-implementation.md)
for the current boundaries and migration plan.

`domain/subscription` is a **shared modular domain library**, not an
independently deployable microservice. It bundles the subscription business
model (DOs, usecases, repo interfaces) and its data implementation
(GORM-based repositories) so that the three services that need subscription
behavior — **admin**, **billing**, and **relay-gateway** — can embed it
directly without introducing an extra network hop.

This is a deliberate **modular-monolith** boundary: the three binaries share
the *same* subscription database tables and the *same* in-process biz/data
code, rather than communicating through a `api/subscription/v1` gRPC contract.

## Package layout

```
domain/subscription/
├── biz/          # Domain layer (pure Go, no proto, no storage tags)
│   ├── entity.go              # UserSubscription, SubscriptionGroup, SubscriptionPlan (DOs)
│   ├── repo.go                # SubscriptionRepository, GroupRepository, PlanRepository interfaces
│   ├── subscription_usecase.go
│   ├── group_usecase.go
│   ├── plan_usecase.go
│   ├── quota_checker.go
│   ├── expiry_checker.go
│   ├── subscription_change.go
│   ├── subscription_absorbable.go
│   └── plan_snapshot.go
├── data/          # Data layer (GORM repository implementations)
│   ├── data.go                # Repository struct, NewRepository, NewRepositoryFromEnv
│   ├── subscription_repo.go
│   ├── group_repo.go
│   ├── plan_repo.go
│   └── db_helpers.go
└── README.md       # This file
```

`domain/subscription/biz` declares the repo interfaces; `domain/subscription/data`
implements them. This follows the same repo-IF-in-biz convention used by
per-service `internal/biz` + `internal/data` layers.

## Consumers

| Service        | How it embeds the library                          |
|----------------|----------------------------------------------------|
| **admin**      | `newSubscriptionUsecases` in `admin_helpers.go`    |
| **billing**    | `newApp` in `wire.go` (billing)                    |
| **relay-gateway** | `newApp` in `wire.go` (relay-gateway)            |

Billing passes its DB and Redis clients directly to `subscriptiondata.NewRepository`.
Admin supplies its resolved DSN to `NewRepositoryFromEnv`. Relay uses the same
constructor with environment-derived settings when subscription or entitlement
support is enabled. Configure all participating processes to use the same
authoritative subscription database.

## Subscription-account platforms

The subscription-*account* layer (distinct from user subscriptions) lives in
`domain/upstream/credential` + `internal/identity` + `internal/adaptor`.
It supports the following platforms, each with its own credential shape:

| Platform | Credential shape              | Channel type | Upstream API           |
|----------|-------------------------------|--------------|------------------------|
| claude   | OAuth (refresh)              | 33           | Anthropic Messages     |
| codex    | OAuth (refresh)              | 32           | OpenAI Responses       |
| zhipu    | static Coding Plan key       | 34           | Anthropic-compatible   |
| minimax  | static Coding Plan key       | 35           | Anthropic-compatible   |
| kimi     | OAuth (refresh, Kimi CLI)    | 36           | Anthropic-compatible   |

GLM/MiniMax/Kimi upstreams all expose Anthropic-compatible Messages endpoints,
so they reuse the `ClaudeOAuthAdaptor` request construction; only the
`BaseURL` (set on the channel) and the token provider differ. See
`docs/design/cn-subscription-accounts-roadmap.md` for the full plan.

## Data ownership & migrations

### Schema owner

The repository root `migrations/` directory maintains the subscription schema.
[`ownership.yaml`](../../migrations/ownership.yaml) assigns these migrations to
Billing in deployments with per-service schemas. Examples include the following files.

- `039_create_user_subscriptions.sql`
- `040_create_subscription_groups.sql`
- `050_create_subscription_plans.sql`
- `042_add_subscription_group_pricing.sql`
- `048_increase_subscription_usage_precision.sql`
- `049_backfill_subscription_usage_from_ledgers.sql`
- `059_enforce_single_active_subscription.sql`

SQLite and PostgreSQL equivalents live under `migrations/sqlite/` and
`migrations/postgres/`. The deployment migration job applies the appropriate
migration set before service startup.

### Write ownership

| Data | Primary writers |
| ---- | --------------- |
| `user_subscriptions` lifecycle | Admin for manual management and legacy fulfillment. Billing for contract purchases and changes, payment fulfillment, refunds and expiry |
| `subscription_groups`, `subscription_plans` | Admin |
| Settled usage in `user_subscriptions` and `subscription_window_charges` | Billing settlement transactions |

Relay reads entitlements for admission. The optional legacy quota middleware
records precheck metrics and continues to the request handler. Billing applies
the final usage, using transaction-scoped locking and deduplication when settling
wallet and subscription charges. [Relay precheck](../../internal/server/subscription_middleware.go) · [Billing settlement](../../app/billing/internal/biz/billing.go) · [Frozen usage](data/frozen_usage.go)

### When to reconsider this boundary

If a future requirement demands that subscription data be owned by a single
service and accessed via gRPC by others, the migration path is:

1. Extract `domain/subscription` into a standalone `app/subscription` service
   with its own `api/subscription/v1` proto.
2. Replace the in-process `subscriptiondata.NewRepository` calls in admin,
   billing, and relay-gateway with gRPC clients against the new service.
3. Move the subscription migrations under the new service's ownership.

Until that requirement materializes, the shared-library approach avoids
premature distribution while keeping the biz/data split clean.
