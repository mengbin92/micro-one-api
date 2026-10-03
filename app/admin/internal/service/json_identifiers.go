package service

import (
	"micro-one-api/pkg/jsonx"
	"strconv"
)

// Existing numeric legacy requests and lossless decimal strings share one DTO.
func normalizeDecimalFields(data []byte, fields ...string) ([]byte, error) {
	var values map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	for _, key := range fields {
		v := values[key]
		if len(v) == 0 || v[0] != '"' {
			continue
		}
		var text string
		if err := jsonx.Unmarshal(v, &text); err != nil {
			return nil, err
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, err
		}
		values[key] = jsonx.RawMessage(strconv.FormatInt(n, 10))
	}
	return jsonx.Marshal(values)
}
func (r *RoutingGroupStateRequest) UnmarshalJSON(data []byte) error {
	type plain RoutingGroupStateRequest
	normalized, err := normalizeDecimalFields(data, "expected_revision")
	if err != nil {
		return err
	}
	return jsonx.Unmarshal(normalized, (*plain)(r))
}
func (r *RoutingUserPriceRequest) UnmarshalJSON(data []byte) error {
	type plain struct {
		PriceRatio      float64 `json:"price_ratio"`
		ExpectedVersion *int64  `json:"expected_version"`
		Reason          string  `json:"reason"`
	}
	normalized, err := normalizeDecimalFields(data, "expected_version")
	if err != nil {
		return err
	}
	var out plain
	if err = jsonx.Unmarshal(normalized, &out); err != nil {
		return err
	}
	r.PriceRatio, r.ExpectedVersion, r.Reason = out.PriceRatio, out.ExpectedVersion, out.Reason
	return nil
}
