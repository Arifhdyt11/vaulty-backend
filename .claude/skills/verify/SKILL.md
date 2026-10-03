---
name: verify
description: Menjalankan pemeriksaan wajib vaulty-backend (gofmt, sqlc up-to-date, go vet, go test, go build) dan melaporkan hasilnya. Gunakan sebelum menyatakan tugas selesai, sebelum commit, atau saat user minta cek/verifikasi.
argument-hint: [--race]
---
# Verifikasi

1. Jalankan `.claude/skills/verify/scripts/verify.sh $ARGUMENTS` dari root repo. `--race` menambahkan race detector (lebih lambat; pakai bila menyentuh goroutine/worker).
2. Gagal → perbaiki penyebabnya (bukan `//nolint`, skip test, atau menghapus assertion), lalu jalankan ulang sampai lolos.
3. Langkah "sqlc" gagal berarti `internal/repository/queries` tidak sinkron dengan `db/` → jalankan `make sqlc` dan ikutkan hasilnya.
4. Laporkan hasil apa adanya: langkah mana yang lolos/gagal. Sebutkan bila perubahan butuh uji manual dengan DB/Redis/Ollama (unit test tidak menyentuh layanan itu).
