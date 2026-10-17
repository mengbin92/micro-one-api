package service

import (
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/timingdto"
)

func convertRequestTiming(p *logv1.RequestTimingSummary) (*requesttrace.TimingSummary, error) {
	s, err := timingdto.FromProto(p)
	if err != nil {
		return nil, biz.ErrInvalidRequestTiming
	}
	return s, nil
}
func requestTimingToProto(s *requesttrace.TimingSummary) *logv1.RequestTimingSummary {
	return timingdto.ToProto(s)
}
