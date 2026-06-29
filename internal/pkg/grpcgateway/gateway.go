package grpcgateway

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

// NewServeMux returns the common gateway mux used by internal HTTP transports.
func NewServeMux() *runtime.ServeMux {
	return runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions:   protojson.MarshalOptions{UseProtoNames: true},
			UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
		}),
		runtime.WithIncomingHeaderMatcher(incomingHeaderMatcher),
	)
}

func incomingHeaderMatcher(key string) (string, bool) {
	if strings.EqualFold(key, "Authorization") {
		return "authorization", true
	}
	return runtime.DefaultHeaderMatcher(key)
}

// DialOptions returns standard options for gateway-to-local-service gRPC calls.
func DialOptions() []grpc.DialOption {
	return []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
}

// EndpointForListenAddr converts a service listen address into a local dial target.
func EndpointForListenAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// MustRegister panics during app construction if a gateway handler cannot be registered.
func MustRegister(err error) {
	if err != nil {
		panic(err)
	}
}

func HealthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// BackgroundContext is isolated so callers do not bind gateway registration to
// request lifetimes.
func BackgroundContext() context.Context {
	return context.Background()
}
