package billing

import (
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/shopspring/decimal"

	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
)

const AudioInputPriceOption = "AudioInputPrice"

type AudioInputPrice struct {
	Yuan       string `json:"yuan"`
	YuanPerUSD string `json:"yuan_per_usd"`
}

func ParseAudioInputPrices(raw string) (map[string]AudioInputPrice, error) {
	var prices map[string]AudioInputPrice
	decoder := jsonx.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&prices); err != nil || prices == nil {
		return nil, fmt.Errorf("invalid %s", AudioInputPriceOption)
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("trailing %s content", AudioInputPriceOption)
	}
	for model, rate := range prices {
		if routing.AudioInputProtocol(model) == "" {
			return nil, fmt.Errorf("unsupported audio input model")
		}
		if _, _, err := rate.decimals(); err != nil {
			return nil, err
		}
	}
	return prices, nil
}

func (p AudioInputPrice) decimals() (decimal.Decimal, decimal.Decimal, error) {
	price, priceErr := positiveAudioDecimal(p.Yuan)
	exchange, exchangeErr := positiveAudioDecimal(p.YuanPerUSD)
	if priceErr != nil || exchangeErr != nil {
		return decimal.Zero, decimal.Zero, fmt.Errorf("invalid metered audio tariff")
	}
	return price, exchange, nil
}

func positiveAudioDecimal(s string) (decimal.Decimal, error) {
	// Decimal text is bounded before parsing to keep exponent and ratio input finite.
	if len(s) == 0 || len(s) > 24 || strings.ContainsAny(s, "eE+-") {
		return decimal.Zero, fmt.Errorf("invalid decimal")
	}
	amount, err := decimal.NewFromString(s)
	if err != nil || amount.Sign() <= 0 {
		return decimal.Zero, fmt.Errorf("invalid decimal")
	}
	return amount, nil
}

func (p AudioInputPrice) Quota(bound routing.CostBound, ratio float64, quotaPerUSD int64) (int64, error) {
	price, exchange, err := p.decimals()
	unitsPerPrice := bound.AudioUnitsPerPrice()
	if err != nil || unitsPerPrice <= 0 || quotaPerUSD <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio <= 0 {
		return 0, fmt.Errorf("invalid metered audio cost")
	}
	numerator := decimal.NewFromInt(bound.AudioUnits).Mul(price).Mul(decimal.NewFromFloat(ratio)).Mul(decimal.NewFromInt(quotaPerUSD))
	denominator := decimal.NewFromInt(unitsPerPrice).Mul(exchange)
	quota := numerator.DivRound(denominator, 0)
	if quota.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, fmt.Errorf("metered audio cost overflow")
	}
	// Positive requests retain the minimum quota unit used by existing settlement.
	return max(1, quota.IntPart()), nil
}
