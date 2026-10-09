package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func TestUnverifiedPeerCannotSkipJWT(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}}}})
	_, mtls, err := extractTokenFromContext(ctx)
	require.Error(t, err)
	require.False(t, mtls)
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer signed-token"))
	token, mtls, err := extractTokenFromContext(ctx)
	require.NoError(t, err)
	require.Equal(t, "signed-token", token)
	require.False(t, mtls)
}
