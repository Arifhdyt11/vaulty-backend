#!/usr/bin/env bash
# Deploy backend ke sipantas: build image amd64 di Mac, kirim lewat ssh, migrasi, lalu
# jalankan ulang api + worker. Server tidak build Go (RAM 2 GB).
#   scripts/deploy.sh            # build + kirim + migrate + up
#   HOST=sipantas scripts/deploy.sh
# Prasyarat di server: ~/apps/vaulty/app.env (lihat deploy/app.env.example).
set -euo pipefail
cd "$(dirname "$0")/.."

HOST="${HOST:-sipantas}"
IMAGE="vaulty-backend:v2"
REMOTE_DIR="apps/vaulty"

echo "==> build $IMAGE (linux/amd64)"
docker buildx build --platform linux/amd64 -t "$IMAGE" --load .

echo "==> kirim image ke $HOST"
docker save "$IMAGE" | gzip | ssh "$HOST" 'gunzip | docker load'

echo "==> salin compose"
scp -q deploy/docker-compose.yml "$HOST:$REMOTE_DIR/docker-compose.yml"

ssh "$HOST" bash -s <<EOF
set -euo pipefail
cd "$REMOTE_DIR"
test -f app.env || { echo "app.env belum ada di ~/$REMOTE_DIR"; exit 1; }
echo "==> redis"
docker compose up -d redis
echo "==> migrate"
docker compose run --rm -T migrate </dev/null
echo "==> api + worker"
docker compose up -d api worker
for _ in \$(seq 1 30); do
  docker exec vaulty-api wget -qO- localhost:8080/readyz >/dev/null 2>&1 && break
  sleep 2
done
docker compose ps
docker exec vaulty-api wget -qO- localhost:8080/readyz && echo " <- readyz"
docker image prune -f >/dev/null
EOF
