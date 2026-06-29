package grpc

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"net"
	"testing"
)

func TestValidateMTLSCertificate(t *testing.T) {
	cfg := &mTLSAuthConfig{
		Enabled:         true,
		AllowedSubjects: []string{"CN=test"},
		AllowedServices: []string{"test-service"},
	}

	tests := []struct {
		name    string
		cert    *x509.Certificate
		wantErr bool
	}{
		{
			name:    "nil certificate",
			cert:    nil,
			wantErr: true,
		},
		{
			name: "valid certificate with digital signature",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				Subject:     pkix.Name{CommonName: "test"},
				DNSNames:    []string{"test-service.example.com"},
			},
			wantErr: false,
		},
		{
			name: "missing digital signature usage",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageKeyEncipherment,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			},
			wantErr: true,
		},
		{
			name: "missing client auth extended usage",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMTLSCertificate(tt.cert, cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMTLSCertificate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExtractServiceIdentity(t *testing.T) {
	cert := &x509.Certificate{
		Subject:     pkix.Name{CommonName: "test-service"},
		DNSNames:    []string{"test-service.example.com", "test-service.internal"},
		IPAddresses: []net.IP{net.ParseIP("10.0.0.1")},
	}

	identity := extractServiceIdentity(cert)

	if identity.Name != "test-service" {
		t.Errorf("Name = %s, want test-service", identity.Name)
	}

	if len(identity.DNSNames) != 2 {
		t.Errorf("DNSNames length = %d, want 2", len(identity.DNSNames))
	}

	if len(identity.IPAddresses) != 1 {
		t.Errorf("IPAddresses length = %d, want 1", len(identity.IPAddresses))
	}
}

func TestServiceIdentityFromContext(t *testing.T) {
	ctx := context.Background()

	// Test empty context
	_, ok := ServiceIdentityFromContext(ctx)
	if ok {
		t.Error("Expected false for empty context")
	}

	// Add identity to context
	identity := ServiceIdentity{
		Name:        "test-service",
		Subject:     "CN=test-service",
		DNSNames:    []string{"test-service.example.com"},
		IPAddresses: []net.IP{},
	}
	ctx = contextWithServiceIdentity(ctx, identity)

	// Extract from context
	extracted, ok := ServiceIdentityFromContext(ctx)
	if !ok {
		t.Error("Expected true for context with identity")
	}

	if extracted.Name != identity.Name {
		t.Errorf("Name = %s, want %s", extracted.Name, identity.Name)
	}
}

func TestMTLSAuthConfig(t *testing.T) {
	cfg := DefaultMTLSAuthConfig()

	if cfg.Enabled {
		t.Error("Expected Enabled to be false by default")
	}

	if cfg.AllowedSubjects == nil {
		t.Error("Expected AllowedSubjects to be initialized")
	}

	if cfg.AllowedServices == nil {
		t.Error("Expected AllowedServices to be initialized")
	}
}

func TestMTLSClientDialOptions(t *testing.T) {
	// This test would require actual certificate files
	// For now, we just test that the function signature is correct
	_, err := MTLSClientDialOptions(
		"/path/to/cert.pem",
		"/path/to/key.pem",
		"/path/to/ca.pem",
		"example.com",
	)

	// Should error because files don't exist
	if err == nil {
		t.Error("Expected error for non-existent certificate files")
	}
}

func TestMTLSServerOptions(t *testing.T) {
	// This test would require actual certificate files
	// For now, we just test that the function signature is correct
	_, err := MTLSServerOptions(
		"/path/to/cert.pem",
		"/path/to/key.pem",
		"/path/to/ca.pem",
	)

	// Should error because files don't exist
	if err == nil {
		t.Error("Expected error for non-existent certificate files")
	}
}

// TestCertificateValidationWithAllowedSubjects tests subject filtering.
func TestCertificateValidationWithAllowedSubjects(t *testing.T) {
	cfg := &mTLSAuthConfig{
		Enabled:         true,
		AllowedSubjects: []string{"CN=allowed", "O=MyOrg"},
		AllowedServices: []string{},
	}

	tests := []struct {
		name    string
		cert    *x509.Certificate
		wantErr bool
	}{
		{
			name: "allowed subject CN",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				Subject:     pkix.Name{CommonName: "allowed"},
			},
			wantErr: false,
		},
		{
			name: "allowed subject O",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				Subject:     pkix.Name{Organization: []string{"MyOrg"}, OrganizationalUnit: []string{"Engineering"}},
			},
			wantErr: false,
		},
		{
			name: "not allowed subject",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				Subject:     pkix.Name{CommonName: "not-allowed"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMTLSCertificate(tt.cert, cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMTLSCertificate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestCertificateValidationWithAllowedServices tests SAN filtering.
func TestCertificateValidationWithAllowedServices(t *testing.T) {
	cfg := &mTLSAuthConfig{
		Enabled:         true,
		AllowedSubjects: []string{},
		AllowedServices: []string{"allowed-service", "trusted-service"},
	}

	tests := []struct {
		name    string
		cert    *x509.Certificate
		wantErr bool
	}{
		{
			name: "allowed service in SAN",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				DNSNames:    []string{"allowed-service.example.com"},
			},
			wantErr: false,
		},
		{
			name: "no matching service in SAN",
			cert: &x509.Certificate{
				KeyUsage:    x509.KeyUsageDigitalSignature,
				ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
				DNSNames:    []string{"other-service.example.com"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMTLSCertificate(tt.cert, cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMTLSCertificate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
