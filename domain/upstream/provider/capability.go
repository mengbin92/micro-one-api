package provider

import (
	"fmt"
	"strings"
)

// CapabilityError is terminal: retrying another protocol/provider would change
// the configured capability contract. It carries no URL, key or upstream body.
type CapabilityError struct{ Feature string }

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("%s is not implemented or is incompatible with the selected provider", e.Feature)
}

// ValidateEndpoint rejects known incompatibilities before any upstream call.
// Native provider conversion for supported Chat/Messages remains explicit.
func ValidateEndpoint(channelType int32, endpoint string) error {
	path := strings.TrimPrefix(endpoint, "/v1")
	if path == "responses" {
		path = "/responses"
	}
	if path == "/responses" || strings.HasPrefix(path, "/responses/") {
		switch channelType {
		case ChannelTypeAnthropic, ChannelTypeGemini, ChannelTypeAzure, ChannelTypeVoyageAI, ChannelTypeClaudeOAuth, ChannelTypeZhipuPlan, ChannelTypeMinimaxPlan, ChannelTypeKimiOAuth:
			return &CapabilityError{Feature: "responses"}
		}
	}
	return nil
}
