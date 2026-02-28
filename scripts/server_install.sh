#!/bin/bash
# Основной скрипт установки VPNTurbo + Xray-core (VLESS-Reality)
# Запускается автоматически из bootstrap install.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(dirname "$SCRIPT_DIR")"
OPT_DIR="/opt/myvpn"
CONFIG_FILE="$OPT_DIR/client_info.txt"
XRAY_CONFIG="/usr/local/etc/xray/config.json"

echo "================================================================"
echo "    🚀 Installing Xray-Core (VLESS-Reality) + MyVPN Server      "
echo "================================================================"

# 1. Update packages
echo "[1/5] Updating system packages..."
apt-get update -q && apt-get upgrade -y -q
apt-get install -y curl wget git xxd qrencode -q

# 2. Install Xray-core
echo "[2/5] Installing Xray-core..."
bash -c "$(curl -L https://github.com/XTLS/Xray-install/raw/main/install-release.sh)" @ install

# Генерируем ключи ТОЛЬКО если конфигурации ещё не было
if [ ! -f "$CONFIG_FILE" ] || ! grep -q "Xray UUID" "$CONFIG_FILE"; then
    echo "Generating XTLS-Reality keys (first run)..."
    
    # ВАЖНО: вызываем xray x25519 ОДИН раз и парсим ОБА ключа из одного вывода!
    KEY_OUTPUT=$(xray x25519)
    PRIVATE_KEY=$(echo "$KEY_OUTPUT" | grep "Private key:" | awk '{print $NF}')
    PUBLIC_KEY=$(echo "$KEY_OUTPUT" | grep "Public key:" | awk '{print $NF}')
    UUID=$(xray uuid)
    
    echo "  Private Key: ${PRIVATE_KEY:0:10}..."
    echo "  Public Key:  ${PUBLIC_KEY:0:10}..."
    echo "  UUID:        $UUID"

    echo "Configuring Xray-core..."
    cat > "$XRAY_CONFIG" <<EOF
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
                    "dest": "www.microsoft.com:443",
                    "xver": 0,
                    "serverNames": ["www.microsoft.com", "microsoft.com"],
                    "privateKey": "$PRIVATE_KEY",
                    "shortIds": ["12345678"]
                }
            }
        }
    ],
    "outbounds": [ { "protocol": "freedom", "tag": "direct" } ]
}
EOF
    KEYS_GENERATED=true
else
    echo "Existing Xray config found — reusing keys."
    UUID=$(grep "Xray UUID" "$CONFIG_FILE" | awk '{print $NF}')
    PUBLIC_KEY=$(grep "Xray Public Key" "$CONFIG_FILE" | awk '{print $NF}')
    KEYS_GENERATED=false
fi

systemctl restart xray
systemctl enable xray

# Открываем порт 443 в файрволе
ufw allow 443/tcp 2>/dev/null || iptables -I INPUT -p tcp --dport 443 -j ACCEPT 2>/dev/null || true

# 3. Install Go and compile MyVPN
echo "[3/5] Installing Go and compiling MyVPN..."

if ! /usr/local/go/bin/go version &>/dev/null; then
    echo "Downloading Go 1.22.1..."
    wget -q https://go.dev/dl/go1.22.1.linux-amd64.tar.gz
    rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.1.linux-amd64.tar.gz
    rm go1.22.1.linux-amd64.tar.gz
fi

echo "Compiling MyVPN server from $REPO_DIR..."
mkdir -p "$OPT_DIR"
cd "$REPO_DIR"
/usr/local/go/bin/go build -o "$OPT_DIR/myvpn-server" ./cmd/server
/usr/local/go/bin/go build -o "$OPT_DIR/myvpn-client" ./cmd/client

# 4. Configure MyVPN service
echo "[4/5] Configuring MyVPN Service..."

# Генерируем VPN ключ только при первой установке
if [ ! -f "$OPT_DIR/vpn.key" ]; then
    head -c 32 /dev/urandom | xxd -p -c 32 > "$OPT_DIR/vpn.key"
fi

VPN_KEY=$(cat "$OPT_DIR/vpn.key")

cat > /etc/systemd/system/myvpn.service <<EOF
[Unit]
Description=MyVPN Server (over Xray-core)
After=network.target xray.service

[Service]
ExecStart=$OPT_DIR/myvpn-server -key $OPT_DIR/vpn.key -addr 127.0.0.1:8080
Restart=always
User=root

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable myvpn
systemctl restart myvpn

# 5. Output
SERVER_IP=$(curl -s https://ifconfig.me)
VLESS_LINK="vless://${UUID}@${SERVER_IP}:443?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.microsoft.com&fp=chrome&pbk=${PUBLIC_KEY}&sid=12345678&type=tcp#VPNTurbo"

cat > "$CONFIG_FILE" <<EOF
=============== VPNTurbo Client Configuration ===============
Server IP:        $SERVER_IP
Xray UUID:        $UUID
Xray Public Key:  $PUBLIC_KEY
VPN Master Key:   $VPN_KEY
VLESS Link:       $VLESS_LINK
=============================================================
EOF

echo ""
echo "[5/5] Setup Finished Successfully!"
echo "=================================================================="
echo "    🎉 SERVER IS RUNNING AND READY TO ACCEPT CONNECTIONS 🎉     "
echo "=================================================================="
echo ""
echo "📋 Данные для подключения:"
echo "  IP сервера:      $SERVER_IP"
echo "  Xray UUID:       $UUID"
echo "  Xray Public Key: $PUBLIC_KEY"
echo "  VPN Master Key:  $VPN_KEY"
echo ""
echo "=================================================================="
echo "📱 VLESS ССЫЛКА ДЛЯ v2rayNG / v2rayN:"
echo "=================================================================="
echo ""
echo "$VLESS_LINK"
echo ""
echo "=================================================================="
echo ""

# QR код
echo "📲 QR-код для v2rayNG:"
qrencode -t ANSIUTF8 "$VLESS_LINK"
echo ""

echo "=================================================================="
echo "📖 КАК ПОДКЛЮЧИТЬСЯ:"
echo "  Android: Откройте v2rayNG → + → Вставить из буфера обмена"
echo "  Windows: Откройте v2rayN  → + → Вставить из буфера обмена"
echo ""
echo "  Затем запустите myvpn-client (нужен root/TUN доступ):"
echo "  ./myvpn-client -server $SERVER_IP:8080 -key $VPN_KEY -socks5 127.0.0.1:10808"
echo "=================================================================="
echo ""
echo "⚙️  Конфигурация сохранена в: $CONFIG_FILE"
echo "    systemctl status xray   — статус Xray"
echo "    systemctl status myvpn  — статус MyVPN"
echo "=================================================================="
