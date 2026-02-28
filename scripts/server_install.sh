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
    PRIVATE_KEY=$(echo "$KEY_OUTPUT" | grep "PrivateKey:" | awk '{print $NF}')
    PUBLIC_KEY=$(echo "$KEY_OUTPUT" | grep "Password:" | awk '{print $NF}')
    UUID=$(xray uuid)
    
    echo "  Private Key: ${PRIVATE_KEY:0:10}..."
    echo "  Public Key:  ${PUBLIC_KEY:0:10}..."
    echo "  UUID:        $UUID"

    echo "Configuring Xray-core..."
    cat > "$XRAY_CONFIG" <<EOF
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
                    "dest": "gateway.icloud.com:443",
                    "xver": 0,
                    "serverNames": ["gateway.icloud.com"],
                    "privateKey": "$PRIVATE_KEY",
                    "shortIds": ["12345678"]
                },
                "xhttpSettings": {
                    "path": "/$(head -c 4 /dev/urandom | xxd -p)"
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

# Открываем порт 8443 в файрволе
ufw allow 8443/tcp 2>/dev/null || iptables -I INPUT -p tcp --dport 8443 -j ACCEPT 2>/dev/null || true

# 3. Install Hysteria 2
echo "[3/5] Installing Hysteria 2..."
bash "$SCRIPT_DIR/install_hysteria.sh"

# 4. Install Go and compile MyVPN
echo "[4/5] Installing Go and compiling MyVPN..."

if ! /usr/local/go/bin/go version &>/dev/null; then
    echo "Downloading Go 1.22.1..."
    wget -q https://go.dev/dl/go1.22.1.linux-amd64.tar.gz
    rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.1.linux-amd64.tar.gz
    rm go1.22.1.linux-amd64.tar.gz
fi

echo "Compiling MyVPN server..."
mkdir -p "$OPT_DIR"
cd "$REPO_DIR"
/usr/local/go/bin/go build -o "$OPT_DIR/myvpn-server" ./cmd/server
/usr/local/go/bin/go build -o "$OPT_DIR/myvpn-client" ./cmd/client

# 5. Configure MyVPN service
echo "[5/5] Configuring MyVPN Service..."

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
# 6. Final Setup & Output
echo "[6/6] Finalizing setup and generating keys..."
bash "$SCRIPT_DIR/newkey.sh" "gateway.icloud.com" "VPNTurbo"

echo ""
echo "=================================================================="
echo "✅ УСТАНОВКА ЗАВЕРШЕНА!"
echo "Используйте QR-коды выше для быстрого подключения в v2rayNG."
echo "Hysteria 2 (вариант 1) — самый надежный способ обхода блокировок."
echo "=================================================================="
