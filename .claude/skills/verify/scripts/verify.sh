#!/usr/bin/env bash
# Pemeriksaan wajib sebelum tugas dianggap selesai. Pakai: verify.sh [--race]
set -uo pipefail
root="$(cd "$(dirname "$0")/../../../.." && pwd)"  # .claude/skills/verify/scripts → root repo

fail=0
step() {
  local name=$1; shift
  echo "▶ $name"
  if "$@"; then echo "✔ $name"; else echo "✘ $name"; fail=1; fi
}

gofmt_clean() {
  local out
  out=$(gofmt -l "$root/cmd" "$root/internal" "$root/pkg" "$root/db")
  [[ -z "$out" ]] || { echo "Belum di-gofmt:"; echo "$out"; return 1; }
}

# Generate sqlc ke direktori sementara lalu bandingkan dengan hasil yang di-commit.
sqlc_in_sync() {
  local tmp; tmp=$(mktemp -d)
  cp -R "$root/db" "$root/sqlc.yaml" "$tmp/"
  mkdir -p "$tmp/internal/repository"
  (cd "$tmp" && go run github.com/sqlc-dev/sqlc/cmd/sqlc@latest generate) || { rm -rf "$tmp"; return 1; }
  diff -r "$tmp/internal/repository/queries" "$root/internal/repository/queries" >/dev/null
  local rc=$?
  [[ $rc -eq 0 ]] || echo "internal/repository/queries tidak sinkron dengan db/: jalankan make sqlc"
  rm -rf "$tmp"
  return $rc
}

race=()
[[ "${1:-}" == "--race" ]] && race=(-race)

step "gofmt" gofmt_clean
step "sqlc" sqlc_in_sync
step "go vet" go -C "$root" vet ./...
step "go test" go -C "$root" test ${race[@]+"${race[@]}"} ./...
step "go build" go -C "$root" build -o /dev/null ./...

exit $fail
