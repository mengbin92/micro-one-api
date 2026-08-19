package server

import (
	"errors"
	"io"
	"net/http"

	appmiddleware "micro-one-api/platform/middleware"
)

var errRequestBodyTooLarge = errors.New("request body too large")

const jsonRequestBodyLimit = appmiddleware.JSONRequestBodyLimit

// readRequestBody keeps direct handler tests safe even when the route
// middleware is not installed. The extra byte distinguishes an oversized
// body from a valid body that exactly fills the cap.
func readRequestBody(r *http.Request, maxSize int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSize+1))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return nil, errRequestBodyTooLarge
		}
		return nil, err
	}
	if int64(len(body)) > maxSize {
		return nil, errRequestBodyTooLarge
	}
	return body, nil
}

func readRouteRequestBody(r *http.Request) ([]byte, error) {
	return readRequestBody(r, appmiddleware.RequestBodyLimitForPath(r.URL.Path))
}

func (s *HTTPServer) writeRequestBodyError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errRequestBodyTooLarge) {
		appmiddleware.WriteRequestBodyTooLarge(w, r.URL.Path)
		return
	}
	s.writeError(w, http.StatusBadRequest, "failed to read request body")
}
