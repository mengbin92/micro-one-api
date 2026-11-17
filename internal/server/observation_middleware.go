package server

import (
	"net/http"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	relaybiz "micro-one-api/internal/biz"
)

// ObservationContextMiddleware establishes two request-scoped facts before any
// handler runs:
//   - a holder for the verified user id (so auth can bind the real owner even
//     when request timing is disabled);
//   - the observation expectation flag used by the transport seam to detect a
//     detached sink.
//
// It is intentionally independent of the observation session, so a path that
// forgets to attach the sink still shows up as expected-but-not-observed.
func ObservationContextMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := requesttrace.WithVerifiedUserHolder(r.Context())
			if relaybiz.ObservationEnabled() {
				ctx = evidence.WithObservationExpected(ctx)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
