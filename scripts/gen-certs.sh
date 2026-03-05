#!/bin/bash
set -e

echo "=== Generating TLS Certificates ==="

CERT_DIR="${CERT_DIR:-certs}"
mkdir -p "$CERT_DIR"

# 生成 CA
openssl ecparam -genkey -name prime256v1 -out "$CERT_DIR/ca.key"
openssl req -new -x509 -key "$CERT_DIR/ca.key" -out "$CERT_DIR/ca.crt" \
    -days 3650 -subj "/CN=NodePass CA"

# 生成节点证书
for node in controller ingress egress relay; do
    echo "Generating cert for $node..."

    openssl ecparam -genkey -name prime256v1 -out "$CERT_DIR/${node}.key"
    openssl req -new -key "$CERT_DIR/${node}.key" -out "$CERT_DIR/${node}.csr" \
        -subj "/CN=${node}"

    cat > "$CERT_DIR/${node}.ext" <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
subjectAltName=DNS:${node},DNS:localhost,IP:127.0.0.1
EOF

    openssl x509 -req -in "$CERT_DIR/${node}.csr" \
        -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
        -out "$CERT_DIR/${node}.crt" -days 365 \
        -extfile "$CERT_DIR/${node}.ext"

    rm -f "$CERT_DIR/${node}.csr" "$CERT_DIR/${node}.ext"
done

rm -f "$CERT_DIR/ca.srl"

echo "=== Certificates generated in $CERT_DIR ==="
