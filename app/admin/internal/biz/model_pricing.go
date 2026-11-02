package biz

import (
	"context"
	"math"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/shopspring/decimal"
	v "micro-one-api/api/admin/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

// ModelPricing is the model editor's price command, in USD per million tokens.
type ModelPricing struct {
	ModelID        string
	InputPrice     float64
	OutputPrice    float64
	CacheReadPrice float64
	// ForcePublish marks an explicit price command whose previous fields were
	// hidden, so redacted zeros cannot suppress an authorized free-price edit.
	ForcePublish bool
}

// ModelPricingRepo mutates only one model's authoritative sale price, under
// the same option lock and revision used by full pricing-map writers.
type ModelPricingRepo interface {
	MutateModelPrice(context.Context, string, func(string) (string, error)) error
}

var ErrModelPricingInvalid = errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_BUSINESS_RULE_VIOLATION.String(), "model pricing requires a model ID and finite non-negative prices")

func ValidateModelPricing(price ModelPricing) error {
	if strings.TrimSpace(price.ModelID) == "" {
		return ErrModelPricingInvalid
	}
	for _, value := range []float64{price.InputPrice, price.OutputPrice, price.CacheReadPrice} {
		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			return ErrModelPricingInvalid
		}
	}
	return nil
}

// PublishModelPricing publishes explicitly edited registry prices. Unchanged
// registry defaults only backfill an absent sale price, so metadata-only edits
// cannot overwrite prices managed independently on the pricing page.
func (uc *SystemOptionsUsecase) PublishModelPricing(ctx context.Context, before *ModelPricing, after ModelPricing) error {
	if err := ValidateModelPricing(after); err != nil {
		return err
	}
	unchanged := !after.ForcePublish && before != nil && before.InputPrice == after.InputPrice && before.OutputPrice == after.OutputPrice && before.CacheReadPrice == after.CacheReadPrice
	if !after.ForcePublish && after.InputPrice == 0 && after.OutputPrice == 0 && after.CacheReadPrice == 0 && (before == nil || unchanged) {
		return nil
	}
	if uc == nil || uc.repo == nil {
		return authorization.ErrWriteStorageUnavailable
	}
	repo, ok := uc.repo.(ModelPricingRepo)
	if !ok {
		return authorization.ErrWriteStorageUnavailable
	}
	if authorization.External(ctx) {
		var err error
		ctx, err = authorization.Prepare(ctx, uc.authorization, "admin.pricing", "billing.pricing.update")
		if err != nil {
			return err
		}
	}
	modelID := strings.ToLower(strings.TrimSpace(after.ModelID))
	return repo.MutateModelPrice(ctx, modelID, func(raw string) (string, error) {
		if unchanged && raw != "" && raw != "null" {
			return raw, nil
		}
		fields := map[string]jsonx.RawMessage{}
		if strings.TrimSpace(raw) != "" {
			if err := jsonx.Unmarshal([]byte(raw), &fields); err != nil {
				return "", ErrPricingOptionsInvalid
			}
			if fields == nil {
				fields = map[string]jsonx.RawMessage{}
			}
		}
		perToken := func(value float64) jsonx.RawMessage {
			price, _ := decimal.NewFromFloat(value).Shift(-6).Float64()
			encoded, _ := jsonx.Marshal(price)
			return encoded
		}
		fields["input_price"], fields["output_price"] = perToken(after.InputPrice), perToken(after.OutputPrice)
		if after.InputPrice == 0 && after.OutputPrice == 0 && after.CacheReadPrice == 0 {
			// Billing drops empty all-zero tariffs. An explicit free cache
			// price keeps this free model authoritative over legacy ratios.
			fields["cache_read_price"] = perToken(0)
		} else if after.CacheReadPrice == 0 {
			delete(fields, "cache_read_price") // Registry zero means not configured.
		} else {
			fields["cache_read_price"] = perToken(after.CacheReadPrice)
		}
		value, err := jsonx.Marshal(fields)
		return string(value), err
	})
}
