#!/bin/bash
# Nyalakan semua kebutuhan dev Vaulty di Mac: Docker (Postgres+Redis), tunnel AI
# ke sipantas, API, dan worker. Log di /tmp/vaulty-*.log. Aman dijalankan ulang:
# yang sudah hidup tidak diganggu.
#   scripts/dev.sh          nyalakan
#   scripts/dev.sh stop     matikan api, worker, tunnel (Docker dibiarkan)
#   scripts/dev.sh status   cek status
set -u
cd "$(dirname "$0")/.."

status() {
  docker info >/dev/null 2>&1 && echo "docker  : hidup" || echo "docker  : MATI"
  lsof -nP -iTCP:8642 -sTCP:LISTEN >/dev/null 2>&1 && echo "tunnel  : hidup" || echo "tunnel  : MATI"
  pgrep -f 'vaulty api' >/dev/null && echo "api     : hidup" || echo "api     : MATI"
  pgrep -f 'vaulty worker' >/dev/null && echo "worker  : hidup" || echo "worker  : MATI"
  curl -s -m 3 "http://$(scutil --get LocalHostName).local:8081/readyz" >/dev/null && \
    echo "alamat  : http://$(scutil --get LocalHostName).local:8081 (siap)" || echo "alamat  : API belum siap"
}

if [ "${1:-}" = "status" ]; then status; exit 0; fi
if [ "${1:-}" = "stop" ]; then
  pkill -f 'vaulty api'; pkill -f 'vaulty worker'; pkill -f 'make tunnel'; pkill -f 'ssh -N .*8642'
  echo "api, worker, tunnel dimatikan"; exit 0
fi

if ! docker info >/dev/null 2>&1; then
  echo "menyalakan Docker Desktop…"; open -a Docker
  for _ in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 2; done
fi
docker compose up -d >/dev/null 2>&1
until docker exec vaulty2-postgres pg_isready -U vaulty >/dev/null 2>&1; do sleep 1; done

if ! lsof -nP -iTCP:8642 -sTCP:LISTEN >/dev/null 2>&1; then
  nohup make tunnel > /tmp/vaulty-tunnel.log 2>&1 &
  sleep 4
fi
pgrep -f 'vaulty api' >/dev/null || nohup go run ./cmd/vaulty api > /tmp/vaulty-api.log 2>&1 &
pgrep -f 'vaulty worker' >/dev/null || nohup go run ./cmd/vaulty worker > /tmp/vaulty-worker.log 2>&1 &
for _ in $(seq 1 30); do curl -s -m 2 localhost:8081/readyz >/dev/null && break; sleep 1; done
status
