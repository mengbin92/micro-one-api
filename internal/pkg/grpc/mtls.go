// Package grpc provides gRPC utilities including mTLS service authentication.
package grpc

import (
	"context"
	"crypto/x509"
	"fmt"
	"net"
	"slices"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
	apptls "micro-one-api/internal/pkg/tls"
)

// mTLSAuthConfig holds configuration for mTLS authentication.
type mTLSAuthConfig struct {
	// Enabled enables mTLS authentication.
	Enabled bool
	// CAFile is the path to the CA certificate for verifying client certificates.
	CAFile string
	// AllowedSubjects is a list of allowed certificate subjects.
	// If empty, all valid certificates are accepted.
	AllowedSubjects []string
	// AllowedServices is a list of allowed service names from certificate SANs.
	AllowedServices []string
}

// DefaultMTLSAuthConfig returns default mTLS authentication configuration.
func DefaultMTLSAuthConfig() *mTLSAuthConfig {
	return &mTLSAuthConfig{
		Enabled:         false,
		AllowedSubjects: []string{},
		AllowedServices: []string{},
	}
}

// MTLSAuthInterceptor creates a gRPC interceptor that enforces mTLS authentication.
//
// This interceptor:
// 1. Verifies client certificates are present and valid
// 2. Checks certificate subjects against allowed list
// 3. Validates service names from certificate SANs
// 4. Extracts service identity for authorization
func MTLSAuthInterceptor(cfg *mTLSAuthConfig) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !cfg.Enabled {
			return handler(ctx, req)
		}

		// Extract peer info
		p, ok := peer.FromContext(ctx)
		if !ok {
			applogger.Log.Warn("No peer info in context")
			return nil, status.Error(codes.Unauthenticated, "no peer info")
		}

		// Check for TLS info
		tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "not using TLS")
		}

		// Verify client certificate is present
		if len(tlsInfo.State.PeerCertificates) == 0 {
			return nil, status.Error(codes.Unauthenticated, "no client certificate provided")
		}

		cert := tlsInfo.State.PeerCertificates[0]

		// Validate certificate
		if err := validateMTLSCertificate(cert, cfg); err != nil {
			applogger.Log.Warn("Certificate validation failed",
				zap.String("subject", cert.Subject.String()),
				zap.Error(err),
				zap.String("method", info.FullMethod),
			)
			return nil, status.Error(codes.PermissionDenied, fmt.Sprintf("certificate validation failed: %v", err))
		}

		// Extract service identity from certificate
		serviceIdentity := extractServiceIdentity(cert)

		// Add service identity to context
		ctx = contextWithServiceIdentity(ctx, serviceIdentity)

		applogger.Log.Debug("mTLS authentication successful",
			zap.String("service", serviceIdentity.Name),
			zap.String("method", info.FullMethod),
			zap.Strings("sans", cert.DNSNames),
		)

		return handler(ctx, req)
	}
}

// validateMTLSCertificate validates a client certificate against mTLS requirements.
func validateMTLSCertificate(cert *x509.Certificate, cfg *mTLSAuthConfig) error {
	// Check basic validity
	if cert == nil {
		return fmt.Errorf("nil certificate")
	}

	// Check certificate usage
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return fmt.Errorf("certificate missing digital signature key usage")
	}

	// Check extended key usage for client auth
	if !slices.Contains(cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return fmt.Errorf("certificate missing client auth extended key usage")
	}

	// Check allowed subjects
	if len(cfg.AllowedSubjects) > 0 {
		subject := cert.Subject.String()
		allowed := false
		for _, allowedSubject := range cfg.AllowedSubjects {
			if strings.Contains(subject, allowedSubject) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("certificate subject not allowed: %s", subject)
		}
	}

	// Check allowed services from SANs
	if len(cfg.AllowedServices) > 0 {
		allowed := false
		for _, san := range cert.DNSNames {
			for _, allowedService := range cfg.AllowedServices {
				if strings.Contains(san, allowedService) {
					allowed = true
					break
				}
			}
			if allowed {
				break
			}
		}
		if !allowed {
			return fmt.Errorf("certificate does not contain allowed service SANs")
		}
	}

	return nil
}

// ServiceIdentity represents the identity of a service from its certificate.
type ServiceIdentity struct {
	// Name is the service name (from CN or SAN).
	Name string
	// Subject is the full certificate subject.
	Subject string
	// DNSNames are the SAN DNS names.
	DNSNames []string
	// IPAddresses are the SAN IP addresses.
	IPAddresses []net.IP
}

// extractServiceIdentity extracts service identity from a certificate.
func extractServiceIdentity(cert *x509.Certificate) ServiceIdentity {
	// Use common name as service name if available
	name := cert.Subject.CommonName
	if name == "" && len(cert.DNSNames) > 0 {
		name = cert.DNSNames[0]
	}

	return ServiceIdentity{
		Name:        name,
		Subject:     cert.Subject.String(),
		DNSNames:    cert.DNSNames,
		IPAddresses: cert.IPAddresses,
	}
}

// Context key for service identity.
type contextKey string

const serviceIdentityKey contextKey = "serviceIdentity"

// contextWithServiceIdentity adds service identity to context.
func contextWithServiceIdentity(ctx context.Context, identity ServiceIdentity) context.Context {
	return context.WithValue(ctx, serviceIdentityKey, identity)
}

// ServiceIdentityFromContext extracts service identity from context.
func ServiceIdentityFromContext(ctx context.Context) (ServiceIdentity, bool) {
	identity, ok := ctx.Value(serviceIdentityKey).(ServiceIdentity)
	return identity, ok
}

// MTLSClientDialOptions creates gRPC client dial options with mTLS.
func MTLSClientDialOptions(certFile, keyFile, caFile, serverName string) ([]grpc.DialOption, error) {
	tlsCfg := &apptls.TLSConfig{
		Enabled:    true,
		CertFile:   certFile,
		KeyFile:    keyFile,
		CAFile:     caFile,
		ServerName: serverName,
	}

	creds, err := apptls.CreateClientCredentials(tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create client credentials: %w", err)
	}

	return []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
	}, nil
}

// MTLSServerOptions creates gRPC server options with mTLS.
func MTLSServerOptions(certFile, keyFile, caFile string) ([]grpc.ServerOption, error) {
	tlsCfg := &apptls.TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
		CAFile:   caFile,
	}

	creds, err := apptls.CreateServerCredentials(tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create server credentials: %w", err)
	}

	return []grpc.ServerOption{
		grpc.Creds(creds),
	}, nil
}
