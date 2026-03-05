#!/bin/bash
set -e

echo "=== NodePass Deploy Script ==="

# 构建
echo "Building..."
make build-all

# 部署到服务器
SERVERS="${SERVERS:-ingress-1 egress-1 relay-1}"

for server in $SERVERS; do
    echo "Deploying to $server..."

    scp bin/nodepass-linux-amd64 "$server":/usr/local/bin/nodepass
    scp "configs/${server}.yaml" "$server":/etc/nodepass/config.yaml

    if [ -d certs ]; then
        ssh "$server" "mkdir -p /etc/nodepass/certs"
        scp -r certs/* "$server":/etc/nodepass/certs/
    fi

    ssh "$server" "systemctl restart nodepass"
    echo "  $server deployed."
done

echo "=== Deployment complete ==="
