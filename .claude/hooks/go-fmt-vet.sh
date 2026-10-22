#!/bin/bash
# PostToolUse(Edit|Write): gofmt file Go yang diubah + go vet paketnya.
input=$(cat)
file=$(printf '%s' "$input" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null)
case "$file" in
  *.go)
    cd "$CLAUDE_PROJECT_DIR" || exit 0
    gofmt -w "$file" 2>/dev/null
    pkg=$(dirname "${file#$CLAUDE_PROJECT_DIR/}")
    out=$(go vet "./$pkg" 2>&1)
    if [ -n "$out" ]; then echo "go vet ./$pkg:"; echo "$out" | head -20; fi
    ;;
esac
exit 0
