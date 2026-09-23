package adaptor

import (
	"bytes"
	"fmt"

	"micro-one-api/domain/upstream/provider"
	"micro-one-api/pkg/jsonx"
)

// ValidateResponsesConversion rejects server-owned state that a Chat/Messages
// conversion cannot reproduce. Native Responses requests bypass this check.
func ValidateResponsesConversion(body []byte) error {
	var request struct {
		PreviousResponseID string           `json:"previous_response_id"`
		Conversation       jsonx.RawMessage `json:"conversation"`
		Background         bool             `json:"background"`
		Store              bool             `json:"store"`
	}
	if err := jsonx.Unmarshal(body, &request); err != nil {
		return fmt.Errorf("parse responses conversion request: %w", err)
	}
	feature := ""
	switch {
	case request.PreviousResponseID != "":
		feature = "previous_response_id"
	case len(request.Conversation) != 0 && !bytes.Equal(bytes.TrimSpace(request.Conversation), []byte("null")):
		feature = "conversation"
	case request.Background:
		feature = "background"
	case request.Store:
		feature = "store"
	}
	if feature != "" {
		return &provider.CapabilityError{Feature: "responses " + feature + " requires a native Responses upstream"}
	}
	return nil
}
