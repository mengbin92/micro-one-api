package data

import (
	"context"
	"encoding/json"
	"fmt"

	"micro-one-api/internal/billing/biz"
)

type PricingConfigRepo struct {
	data *Data
}

func NewPricingConfigRepo(data *Data) *PricingConfigRepo {
	return &PricingConfigRepo{data: data}
}

func (r *PricingConfigRepo) GetPricingConfig(ctx context.Context) (biz.PricingConfig, error) {
	values := map[string]string{}
	rows, err := r.data.db.WithContext(ctx).
		Table("system_options").
		Select("option_key, option_value").
		Where("option_key IN ?", []string{"GroupRatio", "ModelRatio", "CompletionRatio"}).
		Rows()
	if err != nil {
		return biz.PricingConfig{}, fmt.Errorf("list pricing options: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return biz.PricingConfig{}, fmt.Errorf("scan pricing option: %w", err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return biz.PricingConfig{}, err
	}

	return biz.PricingConfig{
		GroupRatios:      parseRatioOption(values["GroupRatio"]),
		ModelRatios:      parseRatioOption(values["ModelRatio"]),
		CompletionRatios: parseRatioOption(values["CompletionRatio"]),
	}, nil
}

func parseRatioOption(raw string) map[string]float64 {
	if raw == "" {
		return nil
	}
	values := map[string]float64{}
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}
