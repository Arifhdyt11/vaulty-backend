#!/usr/bin/env bash
# PreToolUse(Bash): tolak perintah shell yang membaca file env berisi secret.
# Pelengkap `permissions.deny` (yang hanya mencocokkan prefix perintah).
set -euo pipefail

# .env, .env.local, .env.production, dst. — .env.example boleh (dibuang dulu sebelum dicek)
cmd=$(jq -r '.tool_input.command // empty' | sed 's/\.env\.example//g')
if grep -Eq '(^|[^A-Za-z0-9_.-])\.env(\.[A-Za-z0-9_-]+)?([^A-Za-z0-9_.-]|$)' <<<"$cmd"; then
  echo "Diblokir: perintah ini menyentuh file .env yang berisi secret. Pakai .env.example sebagai referensi, atau minta user yang menjalankan." >&2
  exit 2
fi
exit 0
