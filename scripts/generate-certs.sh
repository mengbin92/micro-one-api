#!/bin/bash

# Certificate generation script for mTLS
# This script generates CA certificate, server certificates, and client certificates for mTLS

set -e

# Configuration
CERTS_DIR="./certs"
CA_VALIDITY_DAYS=3650
CERT_VALIDITY_DAYS=365
ORGANIZATION="Micro-One-API"
COUNTRY="US"
STATE="California"
LOCALITY="San Francisco"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}=== Micro-One-API Certificate Generator ===${NC}"

# Create certificates directory
echo -e "${YELLOW}Creating certificates directory...${NC}"
mkdir -p "$CERTS_DIR"

# Generate CA private key
echo -e "${YELLOW}Generating CA private key...${NC}"
openssl genrsa -out "$CERTS_DIR/ca.key" 4096

# Generate CA certificate
echo -e "${YELLOW}Generating CA certificate...${NC}"
openssl req -new -x509 -days $CA_VALIDITY_DAYS -key "$CERTS_DIR/ca.key" -out "$CERTS_DIR/ca.crt" \
    -subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY/O=$ORGANIZATION/OU=CA/CN=Micro-One-API-CA"

# Generate server private key
echo -e "${YELLOW}Generating server private key...${NC}"
openssl genrsa -out "$CERTS_DIR/server.key" 2048

# Generate server CSR
echo -e "${YELLOW}Generating server CSR...${NC}"
openssl req -new -key "$CERTS_DIR/server.key" -out "$CERTS_DIR/server.csr" \
    -subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY/O=$ORGANIZATION/OU=Server/CN=*.micro-one-api.local" \
    -addext "subjectAltName=DNS:*.micro-one-api.local,DNS:localhost,IP:127.0.0.1"

# Sign server certificate with CA
echo -e "${YELLOW}Signing server certificate...${NC}"
openssl x509 -req -in "$CERTS_DIR/server.csr" -CA "$CERTS_DIR/ca.crt" -CAkey "$CERTS_DIR/ca.key" \
    -CAcreateserial -out "$CERTS_DIR/server.crt" -days $CERT_VALIDITY_DAYS \
    -extfile <(cat <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, keyEncipherment
subjectAltName = DNS:*.micro-one-api.local, DNS:localhost, IP:127.0.0.1
EOF
)

# Generate client private key
echo -e "${YELLOW}Generating client private key...${NC}"
openssl genrsa -out "$CERTS_DIR/client.key" 2048

# Generate client CSR
echo -e "${YELLOW}Generating client CSR...${NC}"
openssl req -new -key "$CERTS_DIR/client.key" -out "$CERTS_DIR/client.csr" \
    -subj "/C=$COUNTRY/ST=$STATE/L=$LOCALITY/O=$ORGANIZATION/OU=Client/CN=relay-gateway-client"

# Sign client certificate with CA
echo -e "${YELLOW}Signing client certificate...${NC}"
openssl x509 -req -in "$CERTS_DIR/client.csr" -CA "$CERTS_DIR/ca.crt" -CAkey "$CERTS_DIR/ca.key" \
    -CAcreateserial -out "$CERTS_DIR/client.crt" -days $CERT_VALIDITY_DAYS \
    -extfile <(cat <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature
extendedKeyUsage = clientAuth
EOF
)

# Clean up CSR files
echo -e "${YELLOW}Cleaning up temporary files...${NC}"
rm "$CERTS_DIR"/*.csr

# Set proper permissions
echo -e "${YELLOW}Setting file permissions...${NC}"
chmod 600 "$CERTS_DIR"/*.key
chmod 644 "$CERTS_DIR"/*.crt

# Generate PKCS#12 format for client certificate (optional)
echo -e "${YELLOW}Generating PKCS#12 format for client certificate...${NC}"
PKCS12_PASS="${PKCS12_PASSWORD:-changeme}"
if [ "$PKCS12_PASS" = "changeme" ]; then
    echo -e "${YELLOW}Warning: Using default PKCS#12 password. Set PKCS12_PASSWORD env var for production.${NC}"
fi
openssl pkcs12 -export -out "$CERTS_DIR/client.p12" -inkey "$CERTS_DIR/client.key" \
    -in "$CERTS_DIR/client.crt" -certfile "$CERTS_DIR/ca.crt" -passout "pass:$PKCS12_PASS"

# Display certificate information
echo -e "\n${GREEN}=== Certificate Information ===${NC}"
echo -e "${YELLOW}CA Certificate:${NC}"
openssl x509 -in "$CERTS_DIR/ca.crt" -text -noout | grep -E "(Subject:|Issuer:|Not Before:|Not After:)"

echo -e "\n${YELLOW}Server Certificate:${NC}"
openssl x509 -in "$CERTS_DIR/server.crt" -text -noout | grep -E "(Subject:|Issuer:|Not Before:|Not After:|Subject Alternative Name:)"

echo -e "\n${YELLOW}Client Certificate:${NC}"
openssl x509 -in "$CERTS_DIR/client.crt" -text -noout | grep -E "(Subject:|Issuer:|Not Before:|Not After:|Extended Key Usage:)"

# Verify certificates
echo -e "\n${GREEN}=== Verifying Certificates ===${NC}"
openssl verify -CAfile "$CERTS_DIR/ca.crt" "$CERTS_DIR/server.crt"
openssl verify -CAfile "$CERTS_DIR/ca.crt" "$CERTS_DIR/client.crt"

# Generate environment configuration
echo -e "\n${GREEN}=== Environment Configuration ===${NC}"
cat <<EOF > "$CERTS_DIR/.env.certs"
# TLS Configuration for Development
TLS_ENABLED=true
TLS_CERT_FILE=$CERTS_DIR/server.crt
TLS_KEY_FILE=$CERTS_DIR/server.key
TLS_CA_FILE=$CERTS_DIR/ca.crt
TLS_SERVER_NAME=*.micro-one-api.local

# mTLS Client Configuration
MTLS_ENABLED=true
MTLS_CLIENT_CERT_FILE=$CERTS_DIR/client.crt
MTLS_CLIENT_KEY_FILE=$CERTS_DIR/client.key
MTLS_CA_FILE=$CERTS_DIR/ca.crt
EOF

echo -e "${GREEN}=== Certificate Generation Complete ===${NC}"
echo -e "${GREEN}Certificates generated in: $CERTS_DIR${NC}"
echo -e "${YELLOW}To use these certificates, source the environment file:${NC}"
echo -e "  source $CERTS_DIR/.env.certs"
echo -e "\n${YELLOW}IMPORTANT:${NC}"
echo -e "1. Keep the private keys secure (they have been set to 600 permissions)"
echo -e "2. Add $CERTS_DIR to .gitignore"
echo -e "3. Do not commit certificates to version control"
echo -e "4. Change the default PKCS#12 password in production"
