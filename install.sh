#!/bin/bash
# One-click installation script for Xray-Core + MyVPN on Ubuntu VPS

set -e

echo "================================================================"
echo "    🚀 Installing Xray-Core (VLESS-Reality) + MyVPN Server      "
echo "================================================================"

# 1. Update packages
echo "[1/5] Updating system packages..."
apt update && apt upgrade -y
apt install -y curl wget git xxd

# 2. Install Xray-core
echo "[2/5] Installing Xray-core..."
bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install

# Generate Reality Keys
echo "Generating XTLS-Reality keys..."
KEYS=$(xray x25519)
PRIVATE_KEY=$(echo "$KEYS" | grep "Private key" | awk '{print $3}')
PUBLIC_KEY=$(echo "$KEYS" | grep "Public key" | awk '{print $3}')
UUID=$(xray uuid)

# Configure Xray
echo "Configuring Xray-core..."
cat > /usr/local/etc/xray/config.json <<EOF
{
    "log": {
        "loglevel": "warning"
    },
    "inbounds": [
        {
            "port": 443,
            "protocol": "vless",
            "settings": {
                "clients": [
                    {
                        "id": "$UUID",
                        "flow": "xtls-rprx-vision"
                    }
                ],
                "decryption": "none"
            },
            "streamSettings": {
                "network": "tcp",
                "security": "reality",
                "realitySettings": {
                    "show": false,
                    "dest": "www.microsoft.com:443",
                    "xver": 0,
                    "serverNames": [
                        "www.microsoft.com",
                        "microsoft.com"
                    ],
                    "privateKey": "$PRIVATE_KEY",
                    "shortIds": [
                        "12345678"
                    ]
                }
            }
        }
    ],
    "outbounds": [
        {
            "protocol": "freedom",
            "tag": "direct"
        }
    ],
    "routing": {
        "rules": [
            {
                "type": "field",
                "inboundTag": ["inbound-443"],
                "outboundTag": "direct"
            }
        ]
    }
}
EOF

systemctl restart xray
systemctl enable xray

# 3. Download and build Go & MyVPN
echo "[3/5] Installing Go and MyVPN..."

if ! command -v go &> /dev/null
then
    echo "Go is not installed. Downloading Go 1.22.1..."
    wget -q https://go.dev/dl/go1.22.1.linux-amd64.tar.gz
    rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.1.linux-amd64.tar.gz
    rm go1.22.1.linux-amd64.tar.gz
    export PATH=$PATH:/usr/local/go/bin
fi

# Clone repo 
cd /root
if [ -d "vpnturbo" ]; then
    cd vpnturbo
    git pull
else
    git clone https://github.com/SAquante/vpnturbo.git
    cd vpnturbo
fi

# Build MyVPN server
echo "Compiling MyVPN server..."
mkdir -p /opt/myvpn
/usr/local/go/bin/go build -o /opt/myvpn/myvpn-server ./cmd/server

# 4. Configure MyVPN
echo "[4/5] Configuring MyVPN Service..."
VPN_KEY=$(head -c 32 /dev/urandom | xxd -p -c 32)
echo "$VPN_KEY" > /opt/myvpn/vpn.key

cat > /etc/systemd/system/myvpn.service <<EOF
[Unit]
Description=MyVPN Server (over Xray-core)
After=network.target xray.service

[Service]
ExecStart=/opt/myvpn/myvpn-server -key /opt/myvpn/vpn.key -addr 127.0.0.1:8080
Restart=always
User=root

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable myvpn
systemctl restart myvpn

SERVER_IP=$(curl -s ifconfig.me)

# Генерируем VLESS ссылку для v2rayNG
VLESS_LINK="vless://${UUID}@${SERVER_IP}:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=${PUBLIC_KEY}&sid=12345678&type=tcp#VPNTurbo"

# Сохраняем все данные в файл для последующего доступа
cat > /opt/myvpn/client_info.txt <<EOF
=============== VPNTurbo Client Configuration ===============
Server IP:          $SERVER_IP
Xray UUID:          $UUID
Xray Public Key:    $PUBLIC_KEY
VPN Master Key:     $VPN_KEY
VLESS Link:         $VLESS_LINK
=============================================================
EOF

echo "[5/5] Setup Finished Successfully!"
echo "=================================================================="
echo "    🎉 SERVER IS RUNNING AND READY TO ACCEPT CONNECTIONS 🎉     "
echo "=================================================================="
echo ""
echo "📋 Ваши данные для подключения:"
echo "  IP Адрес сервера:    $SERVER_IP"
echo "  Xray UUID:           $UUID"
echo "  Xray Public Key:     $PUBLIC_KEY"
echo "  VPN Master Key:      $VPN_KEY"
echo ""
echo "=================================================================="
echo "📱 ССЫЛКА ДЛЯ v2rayNG (скопируйте и вставьте в приложение):"
echo "=================================================================="
echo ""
echo "$VLESS_LINK"
echo ""
echo "=================================================================="

# Пробуем сгенерировать QR-код (если qrencode установлен)
if command -v qrencode &> /dev/null; then
    echo "📲 QR-код для v2rayNG (отсканируйте камерой в приложении):"
    echo ""
    qrencode -t ANSIUTF8 "$VLESS_LINK"
    echo ""
else
    echo "💡 Для генерации QR-кода установите: apt install qrencode"
    echo "   Затем выполните: qrencode -t ANSIUTF8 \"\$(cat /opt/myvpn/client_info.txt | grep 'VLESS Link' | cut -d' ' -f10-)\""
fi

echo ""
echo "=================================================================="
echo "📖 ИНСТРУКЦИЯ:"
echo "  1. Откройте v2rayNG на Android"
echo "  2. Нажмите + → Import from clipboard (вставьте VLESS ссылку)"
echo "     ИЛИ нажмите + → Scan QR code"
echo "  3. Подключитесь к серверу"
echo "  4. Запустите myvpn-client:"
echo "     ./myvpn-client -server $SERVER_IP:8080 -key $VPN_KEY -socks5 127.0.0.1:10808"
echo "=================================================================="
echo ""
echo "⚙️  Все данные сохранены в: /opt/myvpn/client_info.txt"
echo "    Статус Xray:  systemctl status xray"
echo "    Статус MyVPN: systemctl status myvpn"
echo "=================================================================="
