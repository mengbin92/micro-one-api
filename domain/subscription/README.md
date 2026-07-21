# domain/subscription — Shared Subscription Domain Library

## Purpose

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

Each consumer independently constructs its own
`subscriptiondata.NewRepository(...)` (or `NewRepositoryFromEnv`) using its
own config's `Data.Database` settings. The `Repository` value is then wired
into `biz.NewSubscriptionUsecase`, `NewGroupUsecase`, `NewPlanUsecase`, etc.

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

The subscription schema is **owned by the shared migration set** in the
repository root `migrations/` directory, not by any single service. The
relevant migrations are:

- `039_create_user_subscriptions.sql`
- `040_create_subscription_groups.sql`
- `050_create_subscription_plans.sql`
- `042_add_subscription_group_pricing.sql`
- `048_increase_subscription_usage_precision.sql`
- `049_backfill_subscription_usage_from_ledgers.sql`
- `059_enforce_single_active_subscription.sql`

SQLite equivalents live under `migrations/sqlite/`.

There is **no single service** that exclusively owns subscription schema
migrations. Migrations are run once per database, typically by a
centralized deploy/migrate job (see the root `migrations/` directory and
`Makefile` `migrate` target), not by any individual service at startup.

### Write ownership

| Table                        | Primary writer(s)                 |
|------------------------------|-----------------------------------|
| `user_subscriptions`         | admin (create/revoke), billing (renewal) |
| `subscription_groups`        | admin                             |
| `subscription_plans`         | admin                             |
| `subscription_usage_windows` | relay-gateway (quota middleware), billing (commit pipeline) |

Because the library is shared in-process, all three services operate against
the same database. Row-level locking (`AddUsageByIDInTx`,
`GetByIDInTx`) is used where concurrent writes can occur (e.g.,
relay-gateway's quota middleware and billing's dual-track commit pipeline
both write usage to the same subscription row). This is safe under the
modular-monolith model where all three services point at the same DB
instance.

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
