package billing

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"

	"micro-one-api/pkg/jsonx"
)

// SettlementPayload is the version-1 normalized financial payload. Usage is
// the LedgerUsage JSON shape; clock/enqueue metadata never changes its digest.
type SettlementPayload struct {
	ReservationID         string           `json:"reservation_id"`
	ActualTokens          int64            `json:"actual_tokens"`
	Success               bool             `json:"success"`
	SubscriptionAccountID int64            `json:"subscription_account_id"`
	Usage                 jsonx.RawMessage `json:"usage"`
}

func (p SettlementPayload) Digest() (string, error) {
	var usage map[string]any
	decoder := jsonx.NewDecoder(bytes.NewReader(p.Usage))
	decoder.UseNumber()
	if err := decoder.Decode(&usage); err != nil {
		return "", err
	}
	if usage == nil {
		return "", fmt.Errorf("settlement usage must be an object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return "", fmt.Errorf("settlement usage must contain one object")
	}
	canonical, err := jsonx.Marshal(usage)
	if err != nil {
		return "", err
	}
	p.Usage = canonical
	raw, err := jsonx.Marshal(p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
