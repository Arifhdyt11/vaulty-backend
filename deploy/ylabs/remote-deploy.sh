#!/usr/bin/env bash
# Dijalankan di server oleh remote-build.sh (atau deploy-ylabs.sh BUILD=local) setelah image siap:
#   remote-deploy.sh <tag>
# Simpan tag, redis, migrasi dengan image baru, jalankan semua service, tunggu siap, lalu pasang
# konfigurasi nginx bila berubah (dicek nginx -t; gagal = file lama dikembalikan).
set -euo pipefail
TAG="${1:?Pemakaian: remote-deploy.sh <tag>}"
cd "$(dirname "$0")"

[ -f .env.backend ] || { echo ".env.backend belum ada di $PWD (lihat .env.backend.example)"; exit 1; }
printf "# Versi image\nVAULTY_TAG=%s\n" "$TAG" > .env.tag
chmod 600 .env.tag
dc() { docker compose --env-file .env.tag "$@"; }

echo "==> redis"
dc up -d redis
echo "==> migrasi"
dc run --rm -T migrate </dev/null
echo "==> up"
dc up -d --remove-orphans

wait_ok() {
  local name=$1 url=$2
  for _ in $(seq 1 30); do
    docker exec "$name" wget -qO- "$url" >/dev/null 2>&1 && { echo "==> $name siap"; return 0; }
    sleep 2
  done
  echo "!! $name belum siap setelah 60 dtk"; return 1
}
wait_ok vaulty-api localhost:8080/readyz
wait_ok vaulty-web 127.0.0.1:3000/
dc ps

CONF=/home/ubuntu/infra/nginx/conf.d
changed=()
for f in nginx/*.conf; do
  dst="$CONF/$(basename "$f")"
  cmp -s "$f" "$dst" && continue
  [ -f "$dst" ] && cp "$dst" "$dst.bak"
  cp "$f" "$dst"
  changed+=("$dst")
done
if [ ${#changed[@]} -gt 0 ]; then
  if docker exec nginx nginx -t 2>&1 | tail -1; then
    docker exec nginx nginx -s reload
    echo "==> nginx diperbarui: ${changed[*]}"
    for d in "${changed[@]}"; do rm -f "$d.bak"; done
  else
    for d in "${changed[@]}"; do if [ -f "$d.bak" ]; then mv "$d.bak" "$d"; else rm -f "$d"; fi; done
    echo "!! nginx -t gagal; konfigurasi lama dikembalikan"; exit 1
  fi
fi

docker stats --no-stream --format "{{.Name}} {{.MemUsage}}" vaulty-api vaulty-worker vaulty-web vaulty-redis
# Buang image lama yang tidak dipakai (tag aktif tetap).
docker image prune -f >/dev/null 2>&1 || true
