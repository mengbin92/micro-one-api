package biz

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ReadVendorCSV accepts one exact account/window scope per file. Export adapters
// must normalize their original report with independent unit/inclusion evidence.
// Unknown columns are rejected instead of silently dropping billable dimensions.
func ReadVendorCSV(input io.Reader, scope VendorScope) ([]VendorBillRow, error) {
	r := csv.NewReader(input)
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	columns := []string{"provider", "account", "project", "upstream_model_id", "currency", "unit", "request_id", "amount", "uncached_input_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "output_tokens"}
	if len(header) != len(columns) {
		return nil, fmt.Errorf("vendor CSV must have exactly the documented scope, amount and five-bucket columns")
	}
	index := map[string]int{}
	for i, h := range header {
		if _, ok := index[h]; ok {
			return nil, fmt.Errorf("duplicate CSV header")
		}
		index[h] = i
	}
	for _, h := range columns {
		if _, ok := index[h]; !ok {
			return nil, fmt.Errorf("missing CSV column %s", h)
		}
	}
	var rows []VendorBillRow
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rows) >= 100000 {
			return nil, fmt.Errorf("vendor CSV row limit exceeded")
		}
		get := func(s string) string { return rec[index[s]] }
		if get("provider") != scope.Provider || get("account") != scope.Account || get("project") != scope.Project || get("upstream_model_id") != scope.Model || get("currency") != scope.Currency || get("unit") != scope.Unit {
			return nil, fmt.Errorf("CSV row %d does not match exact report scope", len(rows)+2)
		}
		row := VendorBillRow{RequestID: get("request_id")}
		if len(row.RequestID) > 256 || strings.HasPrefix(row.RequestID, "unidentified:") {
			return nil, fmt.Errorf("invalid supplier request ID")
		}
		if amount := get("amount"); amount != "" {
			if _, err := vendorNumber(amount); err != nil {
				return nil, err
			}
			row.Amount = &amount
		}
		var buckets [5]int64
		present := 0
		for i, h := range columns[8:] {
			s := get(h)
			if s == "" {
				continue
			}
			present++
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("invalid token count at row %d column %s", len(rows)+2, h)
			}
			buckets[i] = n
		}
		if present != 0 && present != 5 {
			return nil, fmt.Errorf("vendor CSV requires all five buckets or all absent")
		}
		if present == 5 {
			row.Buckets = &buckets
		}
		rows = append(rows, row)
	}
	return rows, nil
}
