package biz

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	"micro-one-api/domain/authorization"
	"sort"
	"strings"
)

func (uc *SystemOptionsUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }
func SystemOptionWriteOperation(key string) string {
	switch strings.ToLower(key) {
	case "notice":
		return "system.content.notice.update"
	case "about":
		return "system.content.about.update"
	case "homepagecontent", "home_page_content":
		return "system.content.home.update"
	case "systemname", "system_name", "site_title", "logo", "footer", "theme":
		return "system.option.update"
	}
	k := strings.ToLower(key)
	if strings.Contains(k, "alipay") || strings.Contains(k, "stripe") || strings.Contains(k, "payment") || strings.Contains(k, "epay") || strings.Contains(k, "topup") {
		return "system.option.payment.update"
	}
	if IsPricingOption(key) {
		return "system.option.pricing.update"
	}
	return "system.option.security.update"
}
func IsPricingOption(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "price") || strings.Contains(k, "ratio") || strings.Contains(k, "quota_per_unit") || k == "quotaperunit" || k == "amountperunit"
}
func SensitiveSystemOption(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "secret") || strings.Contains(k, "password") || strings.Contains(k, "token") || strings.Contains(k, "privatekey") || strings.Contains(k, "webhook") || k == "smtpaccount"
}
func (uc *SystemOptionsUsecase) prepareRead(ctx context.Context, key string) (context.Context, error) {
	if !authorization.External(ctx) {
		return ctx, nil
	}
	var err error
	ctx, err = authorization.Prepare(ctx, uc.authorization, "admin.system_options", "system.option.read")
	if err != nil {
		return ctx, err
	}
	if err = authorization.Require(ctx, "system.option.read", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return ctx, err
	}
	if key == "GroupRatio" {
		return authorization.Prepare(ctx, uc.authorization, "admin.routing_policy", "billing.routing_policy.read")
	}
	if key == "UpstreamModelPrice" {
		return authorization.PrepareOptional(ctx, uc.authorization, "admin.upstream_costs", "billing.upstream_cost.read")
	}
	if IsPricingOption(key) {
		return authorization.PrepareOptional(ctx, uc.authorization, "admin.pricing", "billing.pricing.read")
	}
	return ctx, nil
}
func (uc *SystemOptionsUsecase) prepareWrite(ctx context.Context, key string) (context.Context, error) {
	if !authorization.External(ctx) {
		return ctx, nil
	}
	op := SystemOptionWriteOperation(key)
	point := "admin.system_options"
	if strings.HasPrefix(op, "system.content.") {
		point = "admin.content"
	}
	var err error
	ctx, err = authorization.Prepare(ctx, uc.authorization, point, op)
	if err != nil {
		return ctx, err
	}
	if err = authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return ctx, err
	}
	if strings.HasPrefix(op, "system.option.") && op != "system.option.update" {
		ctx, err = authorization.Prepare(ctx, uc.authorization, "admin.system_options", "system.option.update")
		if err != nil {
			return ctx, err
		}
		if err = authorization.Require(ctx, "system.option.update", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
			return ctx, err
		}
	}
	if key == "GroupRatio" {
		return authorization.Prepare(ctx, uc.authorization, "admin.routing_policy", "billing.routing_policy.publish")
	}
	if key == "UpstreamModelPrice" {
		for _, action := range []string{"create", "update", "delete", "migrate"} {
			ctx, err = authorization.PrepareOptional(ctx, uc.authorization, "admin.upstream_costs", "billing.upstream_cost."+action)
			if err != nil {
				return ctx, err
			}
		}
	} else if IsPricingOption(key) {
		ctx, err = authorization.Prepare(ctx, uc.authorization, "admin.pricing", "billing.pricing.update")
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}
func (uc *SystemOptionsUsecase) PublicContent(ctx context.Context, key string) (string, error) {
	switch strings.ToLower(key) {
	case "notice", "about", "homepagecontent", "home_page_content":
		return uc.repo.Get(context.WithValue(ctx, publicOptionKey{}, true), key)
	default:
		return "", authorization.ErrDenied
	}
}

type publicOptionKey struct{}

func IsPublicOptionContext(ctx context.Context) bool {
	v, _ := ctx.Value(publicOptionKey{}).(bool)
	return v
}

// Mutation applies a pure callback to the locked authoritative value. It cannot
// replace hidden map entries or lose another writer's independent key update.
type SystemOptionsMutationRepo interface {
	Mutate(context.Context, string, func(string) (string, error)) error
}

func (uc *SystemOptionsUsecase) Mutate(ctx context.Context, key string, fn func(string) (string, error)) error {
	var err error
	ctx, err = uc.prepareWrite(ctx, key)
	if err != nil {
		return err
	}
	if repo, ok := uc.repo.(SystemOptionsMutationRepo); ok {
		return repo.Mutate(ctx, key, fn)
	}
	if authorization.External(ctx) {
		for _, op := range []string{"system.option.update", SystemOptionWriteOperation(key)} {
			if _, iam := authorization.QueryScopeFromContext(ctx, op); iam {
				return authorization.ErrDenied
			}
		}
	}
	raw, err := uc.repo.Get(ctx, key)
	if err != nil {
		return err
	}
	value, err := fn(raw)
	if err != nil {
		return err
	}
	return uc.repo.Set(ctx, key, value)
}

type upstreamCostMigrationKey struct{}

func WithUpstreamCostMigration(ctx context.Context) context.Context {
	return context.WithValue(ctx, upstreamCostMigrationKey{}, true)
}
func IsUpstreamCostMigration(ctx context.Context) bool {
	v, _ := ctx.Value(upstreamCostMigrationKey{}).(bool)
	return v
}

// WithUpstreamCostMigrationBindings carries only the owner-resolved migration
// plan; clients never supply an authorization resource identifier.
type upstreamCostMigrationBindingsKey struct{}

func WithUpstreamCostMigrationBindings(ctx context.Context, bindings map[string]string) context.Context {
	copied := make(map[string]string, len(bindings))
	for target, source := range bindings {
		copied[target] = source
	}
	return context.WithValue(ctx, upstreamCostMigrationBindingsKey{}, copied)
}
func UpstreamCostMigrationSource(ctx context.Context, target string) (string, bool) {
	bindings, _ := ctx.Value(upstreamCostMigrationBindingsKey{}).(map[string]string)
	source, ok := bindings[target]
	return source, ok
}

func (uc *SystemOptionsUsecase) AuthorizeUpstreamCostMigration(ctx context.Context) (context.Context, error) {
	return authorization.Prepare(ctx, uc.authorization, "admin.upstream_costs", "billing.upstream_cost.migrate")
}

func IsPricingMapOption(key string) bool {
	switch key {
	case "ModelPrice", "ModelRatio", "CompletionRatio", "GroupRatio", "UpstreamModelPrice":
		return true
	}
	return false
}

type expectedOptionRevisionKey struct{}

func WithExpectedOptionRevision(ctx context.Context, revision int64) context.Context {
	return context.WithValue(ctx, expectedOptionRevisionKey{}, revision)
}
func ExpectedOptionRevision(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(expectedOptionRevisionKey{}).(int64)
	return v, ok
}

type SystemOptionRevisionReader interface {
	GetRevision(context.Context, string) (int64, error)
}

func (uc *SystemOptionsUsecase) GetRevision(ctx context.Context, key string) (int64, error) {
	var err error
	ctx, err = uc.prepareRead(ctx, key)
	if err != nil {
		return 0, err
	}
	if reader, ok := uc.repo.(SystemOptionRevisionReader); ok {
		return reader.GetRevision(ctx, key)
	}
	return 0, nil
}

var ErrSystemOptionConflict = errors.Conflict("SYSTEM_OPTION_REVISION_CONFLICT", "system option revision conflict")

type SystemOptionsBatchRepo interface {
	SetMany(context.Context, map[string]string, map[string]int64) error
}

func (uc *SystemOptionsUsecase) SetMany(ctx context.Context, values map[string]string, revisions map[string]int64) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var err error
	for _, key := range keys {
		ctx, err = uc.prepareWrite(ctx, key)
		if err != nil {
			return err
		}
	}
	if repo, ok := uc.repo.(SystemOptionsBatchRepo); ok {
		return repo.SetMany(ctx, values, revisions)
	}
	for _, key := range keys {
		if _, iam := authorization.QueryScopeFromContext(ctx, SystemOptionWriteOperation(key)); iam {
			return authorization.ErrWriteStorageUnavailable
		}
	}
	for _, key := range keys {
		if err = uc.repo.Set(ctx, key, values[key]); err != nil {
			return err
		}
	}
	return nil
}

type UpstreamCostResourceReader interface {
	CostResourceID(context.Context, string) (int64, error)
}

func (uc *SystemOptionsUsecase) CostResourceID(ctx context.Context, key string) (int64, error) {
	ctx, err := uc.prepareRead(ctx, "UpstreamModelPrice")
	if err != nil {
		return 0, err
	}
	if reader, ok := uc.repo.(UpstreamCostResourceReader); ok {
		return reader.CostResourceID(ctx, key)
	}
	return 0, nil
}
