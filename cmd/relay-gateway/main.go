package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	appauth "micro-one-api/internal/pkg/auth"
	apptls "micro-one-api/internal/pkg/tls"
	relayprovider "micro-one-api/internal/relay/provider"
	"micro-one-api/internal/relay/server"

	"github.com/go-kratos/kratos/v2"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

func main() {
	// Load environment variables
	identityEndpoint := getEnvWithDefault("IDENTITY_GRPC_ENDPOINT", "127.0.0.1:9001")
	channelEndpoint := getEnvWithDefault("CHANNEL_GRPC_ENDPOINT", "127.0.0.1:9002")
	httpAddr := getEnvWithDefault("RELAY_HTTP_ADDR", ":8080")
	enableTLS := os.Getenv("TLS_ENABLED") == "true"
	enableAuth := os.Getenv("ENABLE_AUTH") == "true"

	// Load TLS configuration
	tlsConfig := apptls.LoadTLSConfig()

	// Load service authentication
	var serviceAuth *appauth.ServiceAuthConfig
	var err error
	if enableAuth {
		serviceAuth, err = appauth.LoadServiceAuthConfig()
		if err != nil {
			fmt.Printf("Warning: Failed to load service auth config: %v\n", err)
			enableAuth = false
		}
	}

	// Create gRPC connections with TLS and authentication
	var identityConn, channelConn *grpc.ClientConn
	var identityClient identityv1.IdentityServiceClient
	var channelClient channelv1.ChannelServiceClient

	if enableTLS {
		// Create authenticated connections with TLS
		identityConn, err = createAuthenticatedClient(identityEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			panic(fmt.Sprintf("failed to create identity client with TLS: %v", err))
		}
		defer identityConn.Close()

		channelConn, err = createAuthenticatedClient(channelEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			panic(fmt.Sprintf("failed to create channel client with TLS: %v", err))
		}
		defer channelConn.Close()

		identityClient = identityv1.NewIdentityServiceClient(identityConn)
		channelClient = channelv1.NewChannelServiceClient(channelConn)
	} else {
		// Create insecure connections for development
		identityConn, err = grpc.Dial(identityEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			panic(fmt.Sprintf("failed to connect to identity service: %v", err))
		}
		defer identityConn.Close()

		channelConn, err = grpc.Dial(channelEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			panic(fmt.Sprintf("failed to connect to channel service: %v", err))
		}
		defer channelConn.Close()

		identityClient = identityv1.NewIdentityServiceClient(identityConn)
		channelClient = channelv1.NewChannelServiceClient(channelConn)
	}

	// Create provider factory with timeout
	providerTimeout := 30 * time.Second
	if timeoutStr := os.Getenv("RELAY_PROVIDER_TIMEOUT"); timeoutStr != "" {
		if duration, err := time.ParseDuration(timeoutStr); err == nil {
			providerTimeout = duration
		}
	}

	providerFactory := relayprovider.NewProviderFactory(providerTimeout)

	// Create HTTP server
	httpServer := server.NewHTTPServer(identityClient, channelClient, providerFactory)

	// Create Kratos HTTP server
	srv := khttp.NewServer(
		khttp.Address(httpAddr),
		khttp.Timeout(providerTimeout),
	)
	httpServer.RegisterRoutes(srv)

	// Create and run application
	app := kratos.New(
		kratos.Name("relay-gateway"),
		kratos.Server(srv),
	)

	fmt.Printf("Starting relay-gateway on %s (TLS: %v, Auth: %v)\n", httpAddr, enableTLS, enableAuth)

	if err := app.Run(); err != nil {
		panic(fmt.Sprintf("failed to start relay gateway: %v", err))
	}
}

func createAuthenticatedClient(
	endpoint string,
	tlsConfig *apptls.TLSConfig,
	serviceAuth *appauth.ServiceAuthConfig,
) (*grpc.ClientConn, error) {
	// Create TLS credentials
	creds, err := apptls.CreateClientCredentials(tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS credentials: %w", err)
	}

	// Create dial options
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
	}

	// Add JWT authentication if service auth is configured
	if serviceAuth != nil && serviceAuth.Token != "" {
		tokenCreds := &tokenAuth{token: serviceAuth.Token}
		opts = append(opts, grpc.WithPerRPCCredentials(tokenCreds))
	}

	// Connect to server
	conn, err := grpc.NewClient(endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", endpoint, err)
	}

	return conn, nil
}

// tokenAuth implements PerRPCCredentials for JWT authentication
type tokenAuth struct {
	token string
}

func (t *tokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return true
}

func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
