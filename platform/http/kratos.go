package xhttp

import (
	"net/http"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
)

// SafeKratosServerOptions avoids the Kratos v3.0.0 fallback to
// http.DefaultServeMux for unmatched routes and unsupported methods.
func SafeKratosServerOptions(opts ...khttp.ServerOption) []khttp.ServerOption {
	safeOpts := []khttp.ServerOption{
		khttp.NotFoundHandler(http.NotFoundHandler()),
		khttp.MethodNotAllowedHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		})),
	}
	return append(append([]khttp.ServerOption{}, opts...), safeOpts...)
}
