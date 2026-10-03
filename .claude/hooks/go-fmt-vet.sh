#!/usr/bin/env bash
# PostToolUse: gofmt -w file .go yang baru diedit, lalu go vet package-nya.
# Error vet/compile dikembalikan ke Claude (exit 2) supaya langsung diperbaiki.
set -euo pipefail

file=$(jq -r '.tool_input.file_path // empty')
[[ "$file" == *.go && -f "$file" ]] || exit 0
case "$file" in
  "$CLAUDE_PROJECT_DIR"/*) ;;
  *) exit 0 ;;
esac
[[ "$file" == */internal/repository/queries/* ]] && exit 0

if ! out=$(gofmt -w "$file" 2>&1); then
  echo "gofmt gagal (syntax error?) di $file:" >&2
  echo "$out" >&2
  exit 2
fi

pkg="./$(dirname "${file#"$CLAUDE_PROJECT_DIR"/}")"
if ! out=$(go -C "$CLAUDE_PROJECT_DIR" vet "$pkg" 2>&1); then
  echo "go vet $pkg gagal:" >&2
  echo "$out" >&2
  exit 2
fi
exit 0
