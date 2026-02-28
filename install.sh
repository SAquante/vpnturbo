#!/bin/bash
# Bootstrap: клонирует актуальный репозиторий и запускает основной скрипт из него.
# Это гарантирует, что всегда выполняется ПОСЛЕДНЯЯ версия скрипта,
# даже если пользователь запустил старую версию через curl.

REPO="https://github.com/SAquante/vpnturbo.git"
INSTALL_DIR="/root/vpnturbo"

echo "================================================================"
echo "    🚀 VPNTurbo — Bootstrapping installer...                    "
echo "================================================================"

apt-get install -y git -q 2>/dev/null

if [ -d "$INSTALL_DIR/.git" ]; then
    echo "Updating repository..."
    git -C "$INSTALL_DIR" pull -q
else
    echo "Cloning repository..."
    git clone -q "$REPO" "$INSTALL_DIR"
fi

echo "Launching main installer..."
exec bash "$INSTALL_DIR/scripts/server_install.sh"
