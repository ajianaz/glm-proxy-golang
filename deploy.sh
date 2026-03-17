#!/bin/bash
# deploy.sh - Deploy GLM Proxy ke server
#
# Cara pakai:
#   1. Upload file ini + docker-compose.prod.yml + .env ke server
#   2. chmod +x deploy.sh
#   3. ./deploy.sh
#
# Atau jalankan langsung dari lokal (butuh ssh access):
#   ./deploy.sh user@server

set -euo pipefail

COMPOSE_FILE="docker-compose.prod.yml"

# Warna
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# Cek .env ada
if [ ! -f .env ]; then
    error "File .env tidak ditemukan. Buat dulu dari .env.example:"
    echo "  cp .env.example .env"
    echo "  nano .env"
fi

# Cek docker login GHCR
REGISTRY=$(grep -oP 'DOCKER_BASEURL=\K.*' .env 2>/dev/null || echo "ghcr.io")
if ! docker pull ${REGISTRY}/busybox --quiet 2>/dev/null; then
    # Cek apakah sudah login
    if ! cat ~/.docker/config.json 2>/dev/null | grep -q "${REGISTRY}"; then
        warn "Belum login ke ${REGISTRY}"
        echo ""
        echo "  Login dulu:"
        echo "    echo \$GITHUB_PAT | docker login ${REGISTRY} -u USERNAME --password-stdin"
        echo ""
        echo "  GitHub PAT dibuat di: GitHub > Settings > Developer settings > Personal access tokens"
        echo "  Scope: write:packages, read:packages"
        echo ""
        read -p "Tekan Enter setelah login... " -r
    fi
fi

# Pull image terbaru
info "Pull image terbaru..."
DOCKER_IMAGE=$(grep -oP 'DOCKER_IMAGE=\K.*' .env 2>/dev/null || echo "ghcr.io/ajianaz/glm-proxy-go:main")
docker pull "${DOCKER_IMAGE}"

# Down lalu up
info "Restart container..."
docker compose -f ${COMPOSE_FILE} down --remove-orphans
docker compose -f ${COMPOSE_FILE} up -d

# Wait healthcheck
info "Menunggu healthcheck..."
sleep 5

# Verify
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:${PORT:-3000}/health 2>/dev/null || echo "000")

if [ "$HTTP_CODE" = "200" ]; then
    info "Deploy berhasil! Health check: 200 OK"
    info "Endpoint: http://localhost:${PORT:-3000}"
    echo ""
    docker compose -f ${COMPOSE_FILE} ps
else
    error "Health check gagal (HTTP ${HTTP_CODE}). Cek logs:"
    echo "  docker compose -f ${COMPOSE_FILE} logs -f"
fi
