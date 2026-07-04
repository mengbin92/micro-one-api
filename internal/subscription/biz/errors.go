package biz

import "errors"

var (
	ErrSubscriptionNotFound        = errors.New("subscription not found")
	ErrSubscriptionGroupNotFound   = errors.New("subscription group not found")
	ErrSubscriptionGroupNameTaken  = errors.New("subscription group name already exists")
	ErrSubscriptionPlanNotFound    = errors.New("subscription plan not found")
	ErrSubscriptionPlanNotSaleable = errors.New("subscription plan is not available for sale")
	ErrSubscriptionAlreadyAssigned = errors.New("subscription already assigned")
	ErrSubscriptionNotActive       = errors.New("subscription not active")
	ErrSubscriptionRevoked         = errors.New("subscription revoked")
	ErrSubscriptionGroupDisabled   = errors.New("subscription group disabled")
	ErrQuotaExceeded               = errors.New("quota exceeded")
	ErrInvalidQuotaScope           = errors.New("invalid quota scope")
)
