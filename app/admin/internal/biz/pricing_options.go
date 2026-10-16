package biz

import (
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
	v "micro-one-api/api/admin/v1"
	"micro-one-api/pkg/jsonx"
)

var ErrPricingOptionsInvalid = errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_BUSINESS_RULE_VIOLATION.String(), "three complete pricing maps with non-negative numeric prices are required")

func ValidatePricingOptions(values map[string]string) error {
	if len(values) != 3 {
		return ErrPricingOptionsInvalid
	}
	for _, key := range []string{"ModelPrice", "ModelRatio", "CompletionRatio"} {
		var rows map[string]jsonx.RawMessage
		if jsonx.Unmarshal([]byte(values[key]), &rows) != nil || rows == nil {
			return ErrPricingOptionsInvalid
		}
		for model, raw := range rows {
			if strings.TrimSpace(model) == "" {
				return ErrPricingOptionsInvalid
			}
			if key == "ModelPrice" {
				var prices map[string]*float64
				if jsonx.Unmarshal(raw, &prices) != nil || prices == nil {
					return ErrPricingOptionsInvalid
				}
				for _, price := range prices {
					if price == nil || *price < 0 {
						return ErrPricingOptionsInvalid
					}
				}
			} else {
				var ratio *float64
				if jsonx.Unmarshal(raw, &ratio) != nil || ratio == nil || *ratio < 0 {
					return ErrPricingOptionsInvalid
				}
			}
		}
	}
	return nil
}
