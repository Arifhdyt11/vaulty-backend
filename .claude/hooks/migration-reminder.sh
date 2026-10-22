#!/bin/bash
# PostToolUse: ingatkan bahwa migrasi lama tidak boleh diubah; tambah file baru dengan nomor berikutnya.
input=$(cat)
file=$(printf '%s' "$input" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null)
case "$file" in
  */db/migrations/*.sql)
    if git -C "$CLAUDE_PROJECT_DIR" ls-files --error-unmatch "$file" >/dev/null 2>&1 && ! git -C "$CLAUDE_PROJECT_DIR" diff --quiet -- "$file"; then
      echo "Peringatan: $file sudah pernah di-commit. Migrasi yang sudah jalan tidak boleh diubah; buat file baru 0000N_*.sql."
    fi
    echo "Setelah mengubah migrasi: update ../vaulty-vault/02 Architecture/Database.md dan jalankan make migrate."
    ;;
esac
exit 0
