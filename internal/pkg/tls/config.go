package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc/credentials"
	credentials_insecure "google.golang.org/grpc/credentials/insecure"
)

// TLSConfig holds TLS configuration
type TLSConfig struct {
	Enabled       bool
	CertFile      string
	KeyFile       string
	CAFile        string
	ServerName    string
	InsecureSkipVerify bool
}

// LoadTLSConfig loads TLS configuration from environment
func LoadTLSConfig() *TLSConfig {
	enabled := os.Getenv("TLS_ENABLED") == "true"

	config := &TLSConfig{
		Enabled:           enabled,
		CertFile:         os.Getenv("TLS_CERT_FILE"),
		KeyFile:          os.Getenv("TLS_KEY_FILE"),
		CAFile:           os.Getenv("TLS_CA_FILE"),
		ServerName:       os.Getenv("TLS_SERVER_NAME"),
		InsecureSkipVerify: os.Getenv("TLS_INSECURE_SKIP_VERIFY") == "true",
	}

	return config
}

// CreateClientCredentials creates gRPC client credentials with TLS
func CreateClientCredentials(config *TLSConfig) (credentials.TransportCredentials, error) {
	if !config.Enabled {
		return credentials_insecure.NewCredentials(), nil
	}

	// Load CA certificate
	caCert, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	// Load client certificate and key
	cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Create TLS config
	tlsConfig := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:          caCertPool,
		InsecureSkipVerify: config.InsecureSkipVerify,
		MinVersion:        tls.VersionTLS12,
		MaxVersion:        tls.VersionTLS13,
	}

	if config.ServerName != "" {
		tlsConfig.ServerName = config.ServerName
	}

	return credentials.NewTLS(tlsConfig), nil
}

// CreateServerCredentials creates gRPC server credentials with TLS
func CreateServerCredentials(config *TLSConfig) (credentials.TransportCredentials, error) {
	if !config.Enabled {
		return credentials_insecure.NewCredentials(), nil
	}

	// Load CA certificate
	caCert, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	// Load server certificate and key
	cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	// Create TLS config with mTLS
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    caCertPool,
		ClientAuth:   tls.VerifyClientCertIfGiven, // Require client cert if provided
		MinVersion:   tls.VersionTLS12,
		MaxVersion:   tls.VersionTLS13,
	}

	return credentials.NewTLS(tlsConfig), nil
}

// CreateHTTPClientConfig creates HTTP client TLS config
func CreateHTTPClientConfig(config *TLSConfig) (*tls.Config, error) {
	if !config.Enabled {
		return &tls.Config{
			InsecureSkipVerify: true, // For development only
		}, nil
	}

	// Load CA certificate
	caCert, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	// Load client certificate and key
	cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	return &tls.Config{
		Certificates:       []tls.Certificate{cert},
		RootCAs:          caCertPool,
		InsecureSkipVerify: config.InsecureSkipVerify,
		MinVersion:        tls.VersionTLS12,
		MaxVersion:        tls.VersionTLS13,
	}, nil
}

// ValidateClientCert validates a client certificate
func ValidateClientCert(cert *x509.Certificate, config *TLSConfig) error {
	if !config.Enabled {
		return nil
	}

	// Check certificate validity
	now := time.Now()
	if now.Before(cert.NotBefore) {
		return fmt.Errorf("certificate not yet valid")
	}
	if now.After(cert.NotAfter) {
		return fmt.Errorf("certificate expired")
	}

	// Check certificate usage
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return fmt.Errorf("certificate missing digital signature usage")
	}

	// Check extended key usage
	for _, extKeyUsage := range cert.ExtKeyUsage {
		if extKeyUsage == x509.ExtKeyUsageClientAuth {
			return nil
		}
	}

	return fmt.Errorf("certificate missing client auth extended key usage")
}
