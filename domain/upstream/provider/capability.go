package provider

import (
	"fmt"
	"strings"
)

// CapabilityError is terminal when neither native forwarding nor a supported
// protocol conversion can serve the request. It carries no credentials.
type CapabilityError struct{ Feature string }

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("%s is not implemented or is incompatible with the selected provider", e.Feature)
}

// ValidateEndpoint rejects routes without native or adaptor support. Only
// Responses creation can be converted; stored-response operations remain native.
func ValidateEndpoint(channelType int32, endpoint string) error {
	path := strings.TrimPrefix(endpoint, "/v1")
	if path == "responses" {
		path = "/responses"
	}
	if path == "/responses" || strings.HasPrefix(path, "/responses/") {
		if path == "/responses" {
			switch channelType {
			case ChannelTypeAnthropic, ChannelTypeGemini, ChannelTypeAzure, ChannelTypeClaudeOAuth, ChannelTypeZhipuPlan, ChannelTypeMinimaxPlan, ChannelTypeKimiOAuth:
				return nil
			}
		}
		switch channelType {
		case ChannelTypeAnthropic, ChannelTypeGemini, ChannelTypeAzure, ChannelTypeVoyageAI, ChannelTypeClaudeOAuth, ChannelTypeZhipuPlan, ChannelTypeMinimaxPlan, ChannelTypeKimiOAuth:
			return &CapabilityError{Feature: "responses"}
		}
	}
	return nil
}
