package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	relaybiz "micro-one-api/internal/relay/biz"
)

var errUserRPMLimited = errors.New("user rpm limit exceeded")

func (s *HTTPServer) checkUserRPM(ctx context.Context, userID int64) error {
	if s == nil || userID <= 0 || s.userRPMLimit <= 0 {
		return nil
	}
	if s.userRPM == nil {
		s.userRPM = relaybiz.NewAccountRPMLimiter()
	}
	if s.userRPM.TryAcquire(ctx, userID, s.userRPMLimit) {
		return nil
	}
	return errUserRPMLimited
}

func (s *HTTPServer) writeUserRPMError(w http.ResponseWriter) {
	if s != nil && s.userRPMLimit > 0 {
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", s.userRPMLimit))
	}
	w.Header().Set("X-RateLimit-Remaining", "0")
	w.Header().Set("Retry-After", "60")
	s.writeError(w, http.StatusTooManyRequests, "user rpm limit exceeded")
}
