#!/bin/bash
# PreToolUse(Bash): tolak perintah yang membaca/menulis file rahasia (.env, kunci, service account).
input=$(cat)
cmd=$(printf '%s' "$input" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("command",""))' 2>/dev/null)
if printf '%s' "$cmd" | grep -Eq '(^|[^A-Za-z0-9_./-])\.env([^A-Za-z0-9_.-]|$|\.)|\.pem\b|\.p8\b|service-account[^ ]*\.json|storage/'; then
  if ! printf '%s' "$cmd" | grep -Eq '\.env\.example'; then
    echo "Ditolak: perintah menyentuh file rahasia (.env/.pem/.p8/service-account/storage). Pakai .env.example." >&2
    exit 2
  fi
fi
exit 0
