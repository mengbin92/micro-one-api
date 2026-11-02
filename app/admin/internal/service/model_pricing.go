package service

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Retain the cause for server-side inspection while exposing only an actionable
// message through the HTTP error sanitizer and the gRPC transport.
type modelPricingPublishError struct{ cause error }

func (e *modelPricingPublishError) Error() string {
	return "model saved, but publishing its billing price failed; retry in pricing management"
}

func (e *modelPricingPublishError) Unwrap() error { return e.cause }

func (e *modelPricingPublishError) GRPCStatus() *status.Status {
	return status.New(codes.FailedPrecondition, e.Error())
}
