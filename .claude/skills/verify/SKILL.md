---
name: verify
description: Verifikasi vaulty-backend sebelum commit: gofmt, go vet, go test, dan (bila API jalan) perbarui openapi.json. Gunakan setelah selesai mengubah kode.
disable-model-invocation: false
---
# Verifikasi

Jalankan `.claude/skills/verify/scripts/verify.sh` dari root repo dan laporkan hasilnya ringkas. Bila gagal, perbaiki penyebabnya lalu jalankan lagi; jangan melonggarkan test. Bila `openapi.json` berubah, sebutkan supaya `vaulty-apps` regenerate client (`/gen-client` di repo apps).
