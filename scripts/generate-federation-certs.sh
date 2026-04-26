#!/bin/bash
#
# generate-federation-certs.sh
# Generates self-signed CA and peer certificates for federation mTLS testing.
#
# Usage:
#   ./scripts/generate-federation-certs.sh [output_dir]
#
# Default output directory: certs/federation
#
set -euo pipefail

OUTPUT_DIR="${1:-certs/federation}"
VALIDITY_DAYS=365
CA_SUBJECT="/CN=AgentOS Federation CA/O=NexixAI/OU=Federation"
PEER_SUBJECT="/CN=federation-peer/O=NexixAI/OU=Federation"

echo "==> Generating federation certificates in ${OUTPUT_DIR}"

# Create output directory
mkdir -p "${OUTPUT_DIR}"

# Generate CA private key and certificate
echo "==> Generating CA certificate..."
openssl genrsa -out "${OUTPUT_DIR}/ca.key" 4096 2>/dev/null
openssl req -new -x509 -days ${VALIDITY_DAYS} \
    -key "${OUTPUT_DIR}/ca.key" \
    -out "${OUTPUT_DIR}/ca.crt" \
    -subj "${CA_SUBJECT}" 2>/dev/null

# Generate server certificate
echo "==> Generating server certificate..."
openssl genrsa -out "${OUTPUT_DIR}/server.key" 2048 2>/dev/null

# Create server CSR with SAN
cat > "${OUTPUT_DIR}/server.cnf" << EOF
[req]
default_bits = 2048
prompt = no
default_md = sha256
distinguished_name = dn
req_extensions = req_ext

[dn]
CN = federation-server
O = NexixAI
OU = Federation

[req_ext]
subjectAltName = @alt_names

[alt_names]
DNS.1 = localhost
DNS.2 = federation
DNS.3 = *.federation.local
IP.1 = 127.0.0.1
IP.2 = ::1
EOF

openssl req -new \
    -key "${OUTPUT_DIR}/server.key" \
    -out "${OUTPUT_DIR}/server.csr" \
    -config "${OUTPUT_DIR}/server.cnf" 2>/dev/null

# Sign server certificate with CA
cat > "${OUTPUT_DIR}/server_ext.cnf" << EOF
subjectAltName = DNS:localhost,DNS:federation,DNS:*.federation.local,IP:127.0.0.1,IP:::1
EOF

openssl x509 -req -days ${VALIDITY_DAYS} \
    -in "${OUTPUT_DIR}/server.csr" \
    -CA "${OUTPUT_DIR}/ca.crt" \
    -CAkey "${OUTPUT_DIR}/ca.key" \
    -CAcreateserial \
    -out "${OUTPUT_DIR}/server.crt" \
    -extfile "${OUTPUT_DIR}/server_ext.cnf" 2>/dev/null

# Generate client certificate for peer authentication
echo "==> Generating client certificate..."
openssl genrsa -out "${OUTPUT_DIR}/client.key" 2048 2>/dev/null

cat > "${OUTPUT_DIR}/client.cnf" << EOF
[req]
default_bits = 2048
prompt = no
default_md = sha256
distinguished_name = dn

[dn]
CN = federation-client
O = NexixAI
OU = Federation
EOF

openssl req -new \
    -key "${OUTPUT_DIR}/client.key" \
    -out "${OUTPUT_DIR}/client.csr" \
    -config "${OUTPUT_DIR}/client.cnf" 2>/dev/null

openssl x509 -req -days ${VALIDITY_DAYS} \
    -in "${OUTPUT_DIR}/client.csr" \
    -CA "${OUTPUT_DIR}/ca.crt" \
    -CAkey "${OUTPUT_DIR}/ca.key" \
    -CAcreateserial \
    -out "${OUTPUT_DIR}/client.crt" 2>/dev/null

# Generate JWT signing key pair (RS256)
echo "==> Generating JWT signing keys..."
openssl genrsa -out "${OUTPUT_DIR}/jwt-private.pem" 2048 2>/dev/null
openssl rsa -in "${OUTPUT_DIR}/jwt-private.pem" \
    -pubout -out "${OUTPUT_DIR}/jwt-public.pem" 2>/dev/null

# Clean up temporary files
rm -f "${OUTPUT_DIR}"/*.csr "${OUTPUT_DIR}"/*.cnf "${OUTPUT_DIR}"/*.srl

# Set restrictive permissions
chmod 600 "${OUTPUT_DIR}"/*.key "${OUTPUT_DIR}/jwt-private.pem"
chmod 644 "${OUTPUT_DIR}"/*.crt "${OUTPUT_DIR}/jwt-public.pem"

echo "==> Certificates generated successfully!"
echo ""
echo "Files created:"
echo "  CA:          ${OUTPUT_DIR}/ca.crt, ${OUTPUT_DIR}/ca.key"
echo "  Server:      ${OUTPUT_DIR}/server.crt, ${OUTPUT_DIR}/server.key"
echo "  Client:      ${OUTPUT_DIR}/client.crt, ${OUTPUT_DIR}/client.key"
echo "  JWT:         ${OUTPUT_DIR}/jwt-public.pem, ${OUTPUT_DIR}/jwt-private.pem"
echo ""
echo "Environment variables for .env file:"
echo ""
cat << EOF
# Federation mTLS (server)
AGENTOS_FED_REQUIRE_MTLS=true
AGENTOS_FED_SERVER_CERT=${OUTPUT_DIR}/server.crt
AGENTOS_FED_SERVER_KEY=${OUTPUT_DIR}/server.key
AGENTOS_FED_CA_CERT=${OUTPUT_DIR}/ca.crt

# Federation mTLS (client)
AGENTOS_FED_CLIENT_CERT=${OUTPUT_DIR}/client.crt
AGENTOS_FED_CLIENT_KEY=${OUTPUT_DIR}/client.key

# Federation JWT verification
AGENTOS_FED_JWT_PUBLIC_KEY=${OUTPUT_DIR}/jwt-public.pem
EOF
