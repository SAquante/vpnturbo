#!/bin/bash
# Hysteria 2 Installation Script for VPNTurbo

set -e

OPT_DIR="/opt/myvpn"
HYSTERIA_BIN="/usr/local/bin/hysteria"
HYSTERIA_CONFIG="/etc/hysteria/config.yaml"
CONFIG_FILE="$OPT_DIR/client_info.txt"

echo "================================================================"
echo "    🚀 Installing Hysteria 2 (UDP Obfuscation)                  "
echo "================================================================"

# 1. Download Hysteria 2
if [ ! -f "$HYSTERIA_BIN" ]; then
    echo "Downloading Hysteria 2..."
    curl -Lo "$HYSTERIA_BIN" https://github.com/apernet/hysteria/releases/latest/download/hysteria-linux-amd64
    chmod +x "$HYSTERIA_BIN"
fi

# 2. Generate Certificate (Self-signed for Reality-like behavior or use Reality keys)
mkdir -p /etc/hysteria
if [ ! -f "/etc/hysteria/server.crt" ]; then
    echo "Generating self-signed certificate..."
    openssl req -x509 -nodes -newkey rsa:2048 -keyout /etc/hysteria/server.key -out /etc/hysteria/server.crt -days 3650 -subj "/CN=gateway.icloud.com"
fi

# 3. Configure Hysteria 2
# We use the same VPN_KEY as password for Hysteria
VPN_KEY=$(cat "$OPT_DIR/vpn.key")

cat > "$HYSTERIA_CONFIG" <<EOF
listen: :443

tls:
  cert: /etc/hysteria/server.crt
  key: /etc/hysteria/server.key

auth:
  type: password
  password: "$VPN_KEY"

masquerade:
  type: proxy
  proxy:
    url: https://gateway.icloud.com
    rewriteHost: true

ignoreClientBandwidth: true
EOF

# 4. Create Systemd Service
cat > /etc/systemd/system/hysteria-server.service <<EOF
[Unit]
Description=Hysteria 2 Server
After=network.target

[Service]
ExecStart=$HYSTERIA_BIN server --config $HYSTERIA_CONFIG
Restart=always
User=root

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable hysteria-server
systemctl restart hysteria-server

echo "✅ Hysteria 2 installed and running on port 443 (UDP)"
# Open UDP port 443
ufw allow 443/udp 2>/dev/null || iptables -I INPUT -p udp --dport 443 -j ACCEPT 2>/dev/null || true
