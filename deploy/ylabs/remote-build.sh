#!/usr/bin/env bash
# Dijalankan di server oleh vaulty/scripts/deploy-ylabs.sh: build image dari source di ~/apps/vaulty/src
# (clone dari GitHub, checkout commit persis yang diminta), lalu serahkan ke remote-deploy.sh.
#   remote-build.sh <all|backend|web> <sha-backend> <sha-frontend>
# Build berurutan (backend lalu web) supaya puncak RAM tidak bertumpuk dengan app lain.
set -euo pipefail
WHAT="${1:?Pemakaian: remote-build.sh <all|backend|web> <sha-backend> <sha-frontend>}"
BE="${2:?sha backend}"
FE="${3:?sha frontend}"
cd "$(dirname "$0")"
SRC="$PWD/src"
TAG="$(date +%Y%m%d-%H%M)-${BE}-${FE}"

case "$WHAT" in all|backend|web) ;; *) echo "WHAT tidak dikenal: $WHAT"; exit 1 ;; esac
if [ "$WHAT" != all ] && ! grep -q '^VAULTY_TAG=' .env.tag 2>/dev/null; then
  echo ".env.tag belum ada: deploy pertama harus 'all'"; exit 1
fi
mkdir -p "$SRC"

checkout() {
  local repo=$1 sha=$2
  [ -d "$SRC/$repo/.git" ] || git clone -q "git@github.com:Arifhdyt11/$repo.git" "$SRC/$repo"
  git -C "$SRC/$repo" fetch -q origin
  git -C "$SRC/$repo" checkout -q --detach "$sha"
  echo "==> $repo @ $(git -C "$SRC/$repo" log --oneline -1)"
}

build() {
  local name=$1; shift
  echo "==> build $name:$TAG"
  local t0=$SECONDS
  docker build -q -t "$name:$TAG" "$@" >/dev/null
  echo "    selesai $((SECONDS - t0)) dtk; $(free -m | awk '/Mem:/{print "RAM tersedia " $7 " MB"} /Swap:/{print "swap " $3 " MB"}' | paste -sd, -)"
}

case "$WHAT" in all|backend)
  checkout vaulty-backend "$BE"
  build vaulty-backend "$SRC/vaulty-backend" ;;
esac
case "$WHAT" in all|web)
  checkout vaulty-frontend "$FE"
  build vaulty-frontend "$SRC/vaulty-frontend" ;;
esac

# Deploy sebagian: image sisi lain diberi tag baru dari tag yang sedang jalan.
if [ "$WHAT" != all ]; then
  prev="$(grep '^VAULTY_TAG=' .env.tag | cut -d= -f2)"
  other=$([ "$WHAT" = backend ] && echo vaulty-frontend || echo vaulty-backend)
  docker tag "$other:$prev" "$other:$TAG"
fi

# Cache build lama dibuang agar disk tidak terus tumbuh; cache seminggu terakhir dipertahankan.
docker builder prune -f --filter until=168h >/dev/null 2>&1 || true

exec bash ./remote-deploy.sh "$TAG"
