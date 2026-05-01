package validation

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

	relayprovider "micro-one-api/internal/relay/provider"
)

const (
	// MaxModelNameLength is the maximum allowed length for model names
	MaxModelNameLength = 100

	// MaxMessageCount is the maximum number of messages allowed
	MaxMessageCount = 100

	// MaxMessageContentLength is the maximum length of message content
	MaxMessageContentLength = 10000

	// MaxTemperature is the maximum allowed temperature value
	MaxTemperature = 2.0

	// MinTemperature is the minimum allowed temperature value
	MinTemperature = 0.0

	// MaxMaxTokens is the maximum allowed max_tokens value
	MaxMaxTokens = 128000

	// MinMaxTokens is the minimum allowed max_tokens value
	MinMaxTokens = 1
)

var (
	// modelPattern validates model name format (alphanumeric, hyphens, underscores, dots)
	modelPattern = regexp.MustCompile(`^[a-zA-Z0-9\-_\.]+$`)

	// validRoles contains allowed message roles
	validRoles = map[string]bool{
		"system":    true,
		"user":      true,
		"assistant": true,
		"function":  true,
	}
)

// ValidationError represents a validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error for field '%s': %s", e.Field, e.Message)
}

// NewValidationError creates a new validation error
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

// ValidateChatCompletionsRequest validates a chat completions request
func ValidateChatCompletionsRequest(req *relayprovider.ChatCompletionsRequest) error {
	// Validate model name
	if err := ValidateModelName(req.Model); err != nil {
		return err
	}

	// Validate messages
	if err := ValidateMessages(req.Messages); err != nil {
		return err
	}

	// Validate temperature
	if err := ValidateTemperature(req.Temperature); err != nil {
		return err
	}

	// Validate max_tokens
	if err := ValidateMaxTokens(req.MaxTokens); err != nil {
		return err
	}

	return nil
}

// ValidateModelName validates the model name
func ValidateModelName(model string) error {
	if model == "" {
		return NewValidationError("model", "model is required")
	}

	if utf8.RuneCountInString(model) > MaxModelNameLength {
		return NewValidationError("model",
			fmt.Sprintf("model name too long (max %d characters)", MaxModelNameLength))
	}

	if !modelPattern.MatchString(model) {
		return NewValidationError("model",
			"invalid model name format (only alphanumeric, hyphens, underscores, and dots allowed)")
	}

	return nil
}

// ValidateMessages validates the messages array
func ValidateMessages(messages []relayprovider.Message) error {
	if len(messages) == 0 {
		return NewValidationError("messages", "at least one message is required")
	}

	if len(messages) > MaxMessageCount {
		return NewValidationError("messages",
			fmt.Sprintf("too many messages (max %d)", MaxMessageCount))
	}

	for i, msg := range messages {
		if err := ValidateMessage(msg, i); err != nil {
			return err
		}
	}

	return nil
}

// ValidateMessage validates a single message
func ValidateMessage(msg relayprovider.Message, index int) error {
	field := fmt.Sprintf("messages[%d]", index)

	if msg.Role == "" {
		return NewValidationError(field+".role", "role is required")
	}

	if !validRoles[msg.Role] {
		return NewValidationError(field+".role",
			fmt.Sprintf("invalid role '%s' (must be one of: system, user, assistant, function)", msg.Role))
	}

	if msg.Content == "" {
		return NewValidationError(field+".content", "content is required")
	}

	if utf8.RuneCountInString(msg.Content) > MaxMessageContentLength {
		return NewValidationError(field+".content",
			fmt.Sprintf("content too long (max %d characters)", MaxMessageContentLength))
	}

	return nil
}

// ValidateTemperature validates the temperature parameter
func ValidateTemperature(temperature *float64) error {
	if temperature == nil {
		return nil // Temperature is optional
	}

	if *temperature < MinTemperature || *temperature > MaxTemperature {
		return NewValidationError("temperature",
			fmt.Sprintf("temperature must be between %.1f and %.1f", MinTemperature, MaxTemperature))
	}

	return nil
}

// ValidateMaxTokens validates the max_tokens parameter
func ValidateMaxTokens(maxTokens *int) error {
	if maxTokens == nil {
		return nil // Max tokens is optional
	}

	if *maxTokens < MinMaxTokens || *maxTokens > MaxMaxTokens {
		return NewValidationError("max_tokens",
			fmt.Sprintf("max_tokens must be between %d and %d", MinMaxTokens, MaxMaxTokens))
	}

	return nil
}

// ValidateString validates a string field
func ValidateString(value, fieldName string, required bool, minLength, maxLength int) error {
	if required && value == "" {
		return NewValidationError(fieldName, fmt.Sprintf("%s is required", fieldName))
	}

	if value != "" {
		length := utf8.RuneCountInString(value)
		if minLength > 0 && length < minLength {
			return NewValidationError(fieldName,
				fmt.Sprintf("%s must be at least %d characters", fieldName, minLength))
		}
		if maxLength > 0 && length > maxLength {
			return NewValidationError(fieldName,
				fmt.Sprintf("%s must not exceed %d characters", fieldName, maxLength))
		}
	}

	return nil
}

// ValidateEmail validates an email address
func ValidateEmail(email string) error {
	if email == "" {
		return NewValidationError("email", "email is required")
	}

	emailPattern := regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	if !emailPattern.MatchString(email) {
		return NewValidationError("email", "invalid email format")
	}

	return nil
}

// ValidateURL validates a URL
func ValidateURL(url string) error {
	if url == "" {
		return NewValidationError("url", "url is required")
	}

	urlPattern := regexp.MustCompile(`^https?://[a-zA-Z0-9\-._~:/?#\[\]@!$&'()*+,;=]+$`)
	if !urlPattern.MatchString(url) {
		return NewValidationError("url", "invalid URL format")
	}

	return nil
}

// SanitizeString removes potentially dangerous characters from a string
func SanitizeString(input string) string {
	// Remove null bytes and other control characters
	sanitized := regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`).ReplaceAllString(input, "")

	// Limit length
	if utf8.RuneCountInString(sanitized) > 1000 {
		sanitized = string([]rune(sanitized)[:1000])
	}

	return sanitized
}

// IsValidationError checks if an error is a validation error
func IsValidationError(err error) bool {
	var validationErr *ValidationError
	return errors.As(err, &validationErr)
}
