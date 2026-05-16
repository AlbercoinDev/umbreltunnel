#!/usr/bin/env bash
set -euo pipefail

REPO="AlbercoinDev/umbreltunnel"
BRANCH="main"
DIR="umbreltunnel-vps"

if ! command -v docker &>/dev/null; then
  echo "Installing Docker..."
  curl -fsSL https://get.docker.com | sh
  sudo usermod -aG docker "$USER"
  echo "Docker installed. You may need to log out and back in for group changes to take effect."
fi

if ! command -v docker compose &>/dev/null; then
  echo "Installing Docker Compose..."
  sudo apt-get update && sudo apt-get install -y docker-compose-plugin
fi

echo "Downloading Umbrel Tunnel VPS server..."

rm -rf "$DIR"
curl -sL "https://github.com/$REPO/archive/refs/heads/$BRANCH.tar.gz" | tar xz
mv "umbreltunnel-$BRANCH" "$DIR"
cd "$DIR/vps-server"
rm -f install.sh .gitignore

if [ ! -f .env ]; then
  while [ -z "${domain:-}" ]; do
    read -rp "Enter your domain (e.g., tunnel.example.com): " domain </dev/tty
    if [ -z "$domain" ]; then
      echo "Domain cannot be empty."
    fi
  done
  api_key=$(openssl rand -hex 32 2>/dev/null || echo "CHANGE_ME")
  cat > .env <<EOF
VPS_DOMAIN=$domain
API_KEY=$api_key
EOF
  chmod 600 .env
  echo ".env file created"
fi

domain=$(grep ^VPS_DOMAIN .env | cut -d= -f2-)
api_key=$(grep ^API_KEY .env | cut -d= -f2-)

if [ -z "$domain" ]; then
  echo "ERROR: VPS_DOMAIN is not set in .env"
  exit 1
fi

export VPS_DOMAIN="$domain"
export API_KEY="$api_key"

echo "Starting services..."
sudo -E docker compose up -d

echo ""
echo "=== Umbrel Tunnel VPS installed ==="
echo "Domain: $domain"
echo ""
echo "Next steps:"
echo "  1. Add a DNS A record for $domain pointing to this server's IP"
echo "  2. In your Umbrel, open Umbrel Tunnel app and register using: https://$domain"
echo ""
echo "WireGuard peer configs are generated inside the container."
echo "View the first one with:"
echo "  sudo docker compose exec wireguard cat /config/peer1/peer1.conf"
