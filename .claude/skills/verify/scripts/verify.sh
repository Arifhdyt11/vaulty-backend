#!/bin/bash
set -u
cd "$(dirname "$0")/../../../.." || exit 1
fail=0
echo "== gofmt"; bad=$(gofmt -l .); if [ -n "$bad" ]; then echo "$bad"; gofmt -w .; echo "(sudah diformat ulang)"; fi
echo "== go vet"; go vet ./... || fail=1
echo "== go test"; go test ./... 2>&1 | grep -v 'no test files' || fail=1
if curl -sf -m 2 ${API_URL:-http://localhost:8080}/healthz >/dev/null 2>&1; then
  echo "== openapi"; curl -sf ${API_URL:-http://localhost:8080}/api/v1/openapi.json -o openapi.json && echo "openapi.json diperbarui"
else
  echo "== openapi: API tidak jalan, dilewati"
fi
[ $fail -eq 0 ] && echo "VERIFY OK" || { echo "VERIFY GAGAL"; exit 1; }
