#!/bin/bash
# Генерация новых ключей VPNTurbo (VLESS + Hysteria 2)
# Использование: bash /root/vpnturbo/scripts/newkey.sh [sni] [name]

set -e

SNI="${1:-gateway.icloud.com}"
NAME="${2:-VPNTurbo}"
SHORT_ID=$(openssl rand -hex 3)
SERVER_IP=$(curl -s ifconfig.me)
XHTTP_PATH="/$(openssl rand -hex 4)"

echo "================================================================"
echo "    🔑 Generating New Keys for VPNTurbo Dual-Protocol           "
echo "================================================================"

# 1. Xray Keys & Config
KEY_OUTPUT=$(xray x25519)
PRIVATE_KEY=$(echo "$KEY_OUTPUT" | grep "PrivateKey:" | awk '{print $NF}')
PUBLIC_KEY=$(echo "$KEY_OUTPUT" | grep "Password:" | awk '{print $NF}')
UUID=$(xray uuid)

cat > /usr/local/etc/xray/config.json <<EOF
{
    "log": { "loglevel": "warning" },
    "policy": {
        "levels": {
            "0": { "handshake": 10, "connIdle": 300 }
        }
    },
    "inbounds": [
        {
            "port": 8443,
            "protocol": "vless",
            "settings": {
                "clients": [ { "id": "$UUID", "flow": "" } ],
                "decryption": "none"
            },
            "streamSettings": {
                "network": "xhttp",
                "security": "reality",
                "realitySettings": {
                    "show": false,
                    "dest": "$SNI:443",
                    "xver": 0,
                    "serverNames": ["$SNI"],
                    "privateKey": "$PRIVATE_KEY",
                    "shortIds": ["$SHORT_ID"]
                },
                "xhttpSettings": {
                    "path": "$XHTTP_PATH"
                }
            }
        }
    ],
    "outbounds": [ { "protocol": "freedom", "tag": "direct" } ]
}
EOF

# 2. Hysteria 2 Keys & Config
VPN_KEY=$(cat /opt/myvpn/vpn.key)

cat > /etc/hysteria/config.yaml <<EOF
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
    url: https://$SNI
    rewriteHost: true

ignoreClientBandwidth: true
EOF

# Restart Services
systemctl restart xray
systemctl restart hysteria-server

# Generate Links
VLESS_LINK="vless://${UUID}@${SERVER_IP}:8443?type=xhttp&security=reality&pbk=${PUBLIC_KEY}&fp=chrome&sni=${SNI}&sid=${SHORT_ID}&spx=%2F&path=$(echo $XHTTP_PATH | sed 's|/|%2F|g')#${NAME}_VLESS"
HYSTERIA_LINK="hysteria2://${VPN_KEY}@${SERVER_IP}:443?sni=${SNI}&insecure=1&obfs=none#${NAME}_Hysteria"

# Save Output
cat > /opt/myvpn/client_info.txt <<EOF
=============== VPNTurbo Configuration ===============
Server IP:        $SERVER_IP
Xray UUID:        $UUID
Xray Public Key:  $PUBLIC_KEY
VPN Master Key:   $VPN_KEY
SNI:              $SNI
XHTTP Path:       $XHTTP_PATH

VLESS Link:       $VLESS_LINK
Hysteria 2 Link:  $HYSTERIA_LINK
======================================================
EOF

echo "✅ Services restarted."
echo ""
echo "════════════════════════════════════════════════════════"
echo "🚀 ВАРИАНТ 1: HYSTERIA 2 (Самый быстрый и пробивной)"
echo "════════════════════════════════════════════════════════"
echo "$HYSTERIA_LINK"
echo ""
qrencode -t ANSIUTF8 "$HYSTERIA_LINK"
echo ""
echo "════════════════════════════════════════════════════════"
echo "🥷 ВАРИАНТ 2: VLESS REALITY (Максимальная скрытность)"
echo "════════════════════════════════════════════════════════"
echo "$VLESS_LINK"
echo ""
qrencode -t ANSIUTF8 "$VLESS_LINK"
echo ""
echo "════════════════════════════════════════════════════════"
echo "Инструкция: Скопируйте одну из ссылок и вставьте в v2rayNG (+ -> Import from clipboard)"
echo "Hysteria 2 обычно работает лучше при сильных блокировках."
echo "════════════════════════════════════════════════════════"
