package errors

import (
	"errors"
	"fmt"
	"strings"
)

const (
	ReasonUnknown            = "UNKNOWN"
	ReasonUnauthorized       = "UNAUTHORIZED"
	ReasonChannelNotFound    = "CHANNEL_NOT_FOUND"
	ReasonQuotaNotEnough     = "QUOTA_NOT_ENOUGH"
	ReasonInvalidRequest     = "INVALID_REQUEST"
	ReasonModelForbidden     = "MODEL_FORBIDDEN"
	ReasonUserDisabled       = "USER_DISABLED"
	ReasonServiceUnavailable = "SERVICE_UNAVAILABLE"
	ReasonBadGateway         = "BAD_GATEWAY"
	ReasonTokenDisabled      = "TOKEN_DISABLED"
	ReasonTokenExpired       = "TOKEN_EXPIRED"
	ReasonTokenExhausted     = "TOKEN_EXHAUSTED"
	ReasonTokenNotFound      = "TOKEN_NOT_FOUND"
	ReasonUserNotFound       = "USER_NOT_FOUND"

	// Config domain
	ReasonConfigNotFound = "CONFIG_NOT_FOUND"
	ReasonConfigExists   = "CONFIG_ALREADY_EXISTS"
	ReasonInvalidKey     = "INVALID_CONFIG_KEY"

	// Log domain
	ReasonLogNotFound = "LOG_NOT_FOUND"

	// Monitor domain
	ReasonHealthCheckNotFound = "HEALTH_CHECK_NOT_FOUND"
	ReasonAlertRuleNotFound   = "ALERT_RULE_NOT_FOUND"
	ReasonInvalidAlertRule    = "INVALID_ALERT_RULE"

	// Notify domain
	ReasonNotificationNotFound = "NOTIFICATION_NOT_FOUND"
	ReasonInvalidNotification  = "INVALID_NOTIFICATION"

	// Billing domain
	ReasonAccountNotFound      = "ACCOUNT_NOT_FOUND"
	ReasonReservationNotFound  = "RESERVATION_NOT_FOUND"
	ReasonReservationExpired   = "RESERVATION_EXPIRED"
	ReasonReservationCommitted = "RESERVATION_ALREADY_COMMITTED"
	ReasonReservationReleased  = "RESERVATION_ALREADY_RELEASED"
	ReasonRedeemCodeNotFound   = "REDEEM_CODE_NOT_FOUND"
	ReasonRedeemCodeDisabled   = "REDEEM_CODE_DISABLED"
	ReasonRedeemCodeUsedUp     = "REDEEM_CODE_USED_UP"
	ReasonIdempotencyConflict  = "IDEMPOTENCY_CONFLICT"

	// Relay domain
	ReasonProviderError     = "PROVIDER_ERROR"
	ReasonStreamError       = "STREAM_ERROR"
	ReasonModelNotMapped    = "MODEL_NOT_MAPPED"
	ReasonUpstreamTimeout   = "UPSTREAM_TIMEOUT"
	ReasonUpstreamRateLimit = "UPSTREAM_RATE_LIMIT"

	// Model management domain (方案B independent model registry)
	ReasonModelNotFound        = "MODEL_NOT_FOUND"
	ReasonModelIDExists        = "MODEL_ID_ALREADY_EXISTS"
	ReasonModelAliasExists     = "MODEL_ALIAS_ALREADY_EXISTS"
	ReasonModelAliasNotFound   = "MODEL_ALIAS_NOT_FOUND"
	ReasonModelMappingNotFound = "MODEL_MAPPING_NOT_FOUND"
	ReasonInvalidBatchAction   = "INVALID_BATCH_ACTION"
)

// HTTPStatusCode defines the mapping from error reasons to HTTP status codes
var HTTPStatusCode = map[string]int{
	ReasonUnknown:              500,
	ReasonUnauthorized:         401,
	ReasonChannelNotFound:      503,
	ReasonQuotaNotEnough:       429,
	ReasonInvalidRequest:       400,
	ReasonModelForbidden:       403,
	ReasonUserDisabled:         403,
	ReasonServiceUnavailable:   503,
	ReasonBadGateway:           502,
	ReasonTokenDisabled:        401,
	ReasonTokenExpired:         401,
	ReasonTokenExhausted:       401,
	ReasonTokenNotFound:        401,
	ReasonUserNotFound:         404,
	ReasonConfigNotFound:       404,
	ReasonConfigExists:         409,
	ReasonInvalidKey:           400,
	ReasonLogNotFound:          404,
	ReasonHealthCheckNotFound:  404,
	ReasonAlertRuleNotFound:    404,
	ReasonInvalidAlertRule:     400,
	ReasonNotificationNotFound: 404,
	ReasonInvalidNotification:  400,

	// Billing domain
	ReasonAccountNotFound:      404,
	ReasonReservationNotFound:  404,
	ReasonReservationExpired:   410,
	ReasonReservationCommitted: 409,
	ReasonReservationReleased:  409,
	ReasonRedeemCodeNotFound:   404,
	ReasonRedeemCodeDisabled:   403,
	ReasonRedeemCodeUsedUp:     410,
	ReasonIdempotencyConflict:  409,

	// Relay domain
	ReasonProviderError:     502,
	ReasonStreamError:       502,
	ReasonModelNotMapped:    400,
	ReasonUpstreamTimeout:   504,
	ReasonUpstreamRateLimit: 429,

	// Model management
	ReasonModelNotFound:        404,
	ReasonModelIDExists:        409,
	ReasonModelAliasExists:     409,
	ReasonModelAliasNotFound:   404,
	ReasonModelMappingNotFound: 404,
	ReasonInvalidBatchAction:   400,
}

// GetHTTPStatusCode returns the HTTP status code for a given error reason
func GetHTTPStatusCode(reason string) int {
	if code, ok := HTTPStatusCode[reason]; ok {
		return code
	}
	return HTTPStatusCode[ReasonUnknown]
}

// New creates a new error with a reason
func New(reason string) error {
	return &Error{
		Reason: reason,
	}
}

// Newf creates a new formatted error with a reason
func Newf(reason string, format string, args ...interface{}) error {
	return &Error{
		Reason:  reason,
		Message: fmt.Sprintf(format, args...),
	}
}

// Error represents a structured error
type Error struct {
	Reason  string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Reason != "" {
		return e.Reason
	}
	return "unknown error"
}

func (e *Error) Unwrap() error {
	return e.Err
}

// MapIdentityError maps identity biz errors to structured errors
func MapIdentityError(err error) error {
	if err == nil {
		return nil
	}

	var target error
	errorMsg := err.Error()

	switch {
	case errorMsg == "invalid token":
		target = &Error{Reason: ReasonUnauthorized, Message: "invalid token"}
	case errorMsg == "token expired":
		target = &Error{Reason: ReasonTokenExpired, Message: "token expired"}
	case errorMsg == "token exhausted":
		target = &Error{Reason: ReasonTokenExhausted, Message: "token quota exhausted"}
	case errorMsg == "token disabled":
		target = &Error{Reason: ReasonTokenDisabled, Message: "token disabled"}
	case errorMsg == "token not found":
		target = &Error{Reason: ReasonTokenNotFound, Message: "token not found"}
	case errorMsg == "user disabled":
		target = &Error{Reason: ReasonUserDisabled, Message: "user disabled"}
	case errorMsg == "user not found":
		target = &Error{Reason: ReasonUserNotFound, Message: "user not found"}
	default:
		target = &Error{Reason: ReasonUnknown, Message: err.Error()}
	}

	return target
}

// MapChannelError maps channel biz errors to structured errors
func MapChannelError(err error) error {
	if err == nil {
		return nil
	}

	errorMsg := err.Error()
	switch {
	case errorMsg == "channel not found":
		return &Error{Reason: ReasonChannelNotFound, Message: "no available channel"}
	case errorMsg == "model not found":
		return &Error{Reason: ReasonModelNotFound, Message: "model not found"}
	case errorMsg == "model_id already exists":
		return &Error{Reason: ReasonModelIDExists, Message: "model_id already exists"}
	case errorMsg == "model alias already exists":
		return &Error{Reason: ReasonModelAliasExists, Message: "model alias already exists"}
	case errorMsg == "model alias not found":
		return &Error{Reason: ReasonModelAliasNotFound, Message: "model alias not found"}
	case errorMsg == "model mapping not found":
		return &Error{Reason: ReasonModelMappingNotFound, Message: "model mapping not found"}
	case errorMsg == "invalid batch action":
		return &Error{Reason: ReasonInvalidBatchAction, Message: "invalid batch action"}
	default:
		return &Error{Reason: ReasonUnknown, Message: err.Error()}
	}
}

// IsForbidden checks if an error represents a forbidden action
func IsForbidden(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Reason == ReasonModelForbidden || e.Reason == ReasonUserDisabled)
}

// IsUnauthorized checks if an error represents unauthorized access
func IsUnauthorized(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Reason == ReasonUnauthorized ||
		e.Reason == ReasonTokenDisabled ||
		e.Reason == ReasonTokenExpired ||
		e.Reason == ReasonTokenExhausted ||
		e.Reason == ReasonTokenNotFound
}

// IsServiceUnavailable checks if an error represents service unavailability
func IsServiceUnavailable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Reason == ReasonServiceUnavailable || e.Reason == ReasonChannelNotFound
}

// MapConfigError maps config biz errors to structured errors
func MapConfigError(err error) error {
	if err == nil {
		return nil
	}
	switch err.Error() {
	case "config not found":
		return &Error{Reason: ReasonConfigNotFound, Message: "config not found"}
	case "config already exists":
		return &Error{Reason: ReasonConfigExists, Message: "config already exists"}
	case "invalid config key":
		return &Error{Reason: ReasonInvalidKey, Message: "invalid config key"}
	default:
		return &Error{Reason: ReasonUnknown, Message: err.Error()}
	}
}

// MapLogError maps log biz errors to structured errors
func MapLogError(err error) error {
	if err == nil {
		return nil
	}
	switch err.Error() {
	case "log entry not found":
		return &Error{Reason: ReasonLogNotFound, Message: "log entry not found"}
	default:
		return &Error{Reason: ReasonUnknown, Message: err.Error()}
	}
}

// MapMonitorError maps monitor biz errors to structured errors
func MapMonitorError(err error) error {
	if err == nil {
		return nil
	}
	switch err.Error() {
	case "health check not found":
		return &Error{Reason: ReasonHealthCheckNotFound, Message: "health check not found"}
	case "alert rule not found":
		return &Error{Reason: ReasonAlertRuleNotFound, Message: "alert rule not found"}
	case "invalid alert rule":
		return &Error{Reason: ReasonInvalidAlertRule, Message: "invalid alert rule"}
	default:
		return &Error{Reason: ReasonUnknown, Message: err.Error()}
	}
}

// MapNotifyError maps notify biz errors to structured errors
func MapNotifyError(err error) error {
	if err == nil {
		return nil
	}
	switch err.Error() {
	case "notification not found":
		return &Error{Reason: ReasonNotificationNotFound, Message: "notification not found"}
	case "invalid notification":
		return &Error{Reason: ReasonInvalidNotification, Message: "invalid notification"}
	default:
		return &Error{Reason: ReasonUnknown, Message: err.Error()}
	}
}

// MapBillingError maps billing biz errors to structured errors
func MapBillingError(err error) error {
	if err == nil {
		return nil
	}
	switch err.Error() {
	case "account not found":
		return &Error{Reason: ReasonAccountNotFound, Message: "account not found"}
	case "insufficient quota":
		return &Error{Reason: ReasonQuotaNotEnough, Message: "insufficient quota"}
	case "reservation not found":
		return &Error{Reason: ReasonReservationNotFound, Message: "reservation not found"}
	case "reservation expired":
		return &Error{Reason: ReasonReservationExpired, Message: "reservation expired"}
	case "reservation already committed":
		return &Error{Reason: ReasonReservationCommitted, Message: "reservation already committed"}
	case "reservation already released":
		return &Error{Reason: ReasonReservationReleased, Message: "reservation already released"}
	case "redeem code not found":
		return &Error{Reason: ReasonRedeemCodeNotFound, Message: "redeem code not found"}
	case "redeem code disabled":
		return &Error{Reason: ReasonRedeemCodeDisabled, Message: "redeem code disabled"}
	case "redeem code used up":
		return &Error{Reason: ReasonRedeemCodeUsedUp, Message: "redeem code used up"}
	default:
		return &Error{Reason: ReasonUnknown, Message: err.Error()}
	}
}

// MapRelayError maps relay biz errors to structured errors
func MapRelayError(err error) error {
	if err == nil {
		return nil
	}
	errorMsg := err.Error()
	switch {
	case strings.Contains(errorMsg, "model not mapped"):
		return &Error{Reason: ReasonModelNotMapped, Message: errorMsg}
	case strings.Contains(errorMsg, "upstream timeout"):
		return &Error{Reason: ReasonUpstreamTimeout, Message: errorMsg}
	case strings.Contains(errorMsg, "rate limit"):
		return &Error{Reason: ReasonUpstreamRateLimit, Message: errorMsg}
	case strings.Contains(errorMsg, "stream error"):
		return &Error{Reason: ReasonStreamError, Message: errorMsg}
	case strings.Contains(errorMsg, "upstream error") || strings.Contains(errorMsg, "provider error"):
		return &Error{Reason: ReasonProviderError, Message: errorMsg}
	default:
		return &Error{Reason: ReasonUnknown, Message: err.Error()}
	}
}

// IsRetryable checks if an error represents a retryable condition
func IsRetryable(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Reason == ReasonUpstreamRateLimit ||
		e.Reason == ReasonUpstreamTimeout ||
		e.Reason == ReasonProviderError ||
		e.Reason == ReasonBadGateway ||
		e.Reason == ReasonServiceUnavailable
}
