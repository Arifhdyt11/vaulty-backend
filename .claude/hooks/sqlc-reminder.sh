#!/usr/bin/env bash
# PostToolUse: setelah mengubah SQL query atau migration, ingatkan Claude untuk regenerate sqlc.
set -euo pipefail

file=$(jq -r '.tool_input.file_path // empty')
case "$file" in
  */db/queries/*.sql|*/db/migrations/*.sql)
    jq -n '{hookSpecificOutput: {hookEventName: "PostToolUse",
      additionalContext: "File SQL berubah: jalankan `make sqlc` lalu `go build ./...` sebelum lanjut. Pastikan query data user memfilter user_id (ADR-007) dan migration punya blok -- +goose Down."}}'
    ;;
esac
exit 0
