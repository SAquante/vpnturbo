#!/bin/bash
# Генерация нового ключа VPNTurbo + VLESS ссылка + QR
# Использование: bash /opt/myvpn/newkey.sh [sni] [name]
# Пример:       bash /opt/myvpn/newkey.sh max.ru MyVPN

set -e

SNI="${1:-max.ru}"
NAME="${2:-VPNTurbo}"
SHORT_ID=$(openssl rand -hex 3)
SERVER_IP=$(curl -s ifconfig.me)

# Генерируем ключи
KEY_OUTPUT=$(xray x25519)
PRIVATE_KEY=$(echo "$KEY_OUTPUT" | awk '/PrivateKey/ {print $2}')
PUBLIC_KEY=$(echo "$KEY_OUTPUT" | awk '/Password/ {print $2}')
UUID=$(xray uuid)

echo "🔑 Generating new keys..."
echo "  UUID:        $UUID"
echo "  Private Key: ${PRIVATE_KEY:0:12}..."
echo "  Public Key:  $PUBLIC_KEY"
echo "  Short ID:    $SHORT_ID"
echo "  SNI:         $SNI"
echo ""

# Обновляем конфиг Xray
cat > /usr/local/etc/xray/config.json <<EOF
{
    "log": { "loglevel": "warning" },
    "inbounds": [
        {
            "port": 443,
            "protocol": "vless",
            "settings": {
                "clients": [ { "id": "$UUID", "flow": "xtls-rprx-vision" } ],
                "decryption": "none"
            },
            "streamSettings": {
                "network": "tcp",
                "security": "reality",
                "realitySettings": {
                    "show": false,
                    "dest": "${SNI}:443",
                    "xver": 0,
                    "serverNames": ["${SNI}"],
                    "privateKey": "$PRIVATE_KEY",
                    "shortIds": ["$SHORT_ID"]
                }
            }
        }
    ],
    "outbounds": [ { "protocol": "freedom", "tag": "direct" } ]
}
EOF

# Перезапускаем Xray
systemctl restart xray

# Формируем VLESS ссылку
VLESS_LINK="vless://${UUID}@${SERVER_IP}:443?type=tcp&security=reality&pbk=${PUBLIC_KEY}&fp=chrome&sni=${SNI}&sid=${SHORT_ID}&spx=%2F&flow=xtls-rprx-vision#${NAME}"

# Сохраняем
VPN_KEY=$(cat /opt/myvpn/vpn.key 2>/dev/null || echo "not_set")
cat > /opt/myvpn/client_info.txt <<EOF
Server IP:        $SERVER_IP
Xray UUID:        $UUID
Xray Public Key:  $PUBLIC_KEY
Short ID:         $SHORT_ID
SNI:              $SNI
VPN Master Key:   $VPN_KEY
VLESS Link:       $VLESS_LINK
EOF

echo "✅ Xray restarted: $(systemctl is-active xray)"
echo ""
echo "══════════════════════════════════════════"
echo "📱 VLESS ССЫЛКА (скопируйте в v2rayNG):"
echo "══════════════════════════════════════════"
echo ""
echo "$VLESS_LINK"
echo ""
echo "══════════════════════════════════════════"
echo "📲 QR-код:"
echo "══════════════════════════════════════════"
qrencode -t ANSIUTF8 "$VLESS_LINK" 2>/dev/null || echo "(установите qrencode: apt install qrencode)"
echo ""
echo "Данные сохранены: /opt/myvpn/client_info.txt"
