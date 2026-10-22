---
name: new-migration
description: Membuat migrasi goose baru di db/migrations dengan nomor berikutnya, Up/Down, dan memperbarui note Database di vault.
argument-hint: <nama_migrasi>
disable-model-invocation: true
---
# Migrasi baru: $ARGUMENTS

1. Nomor berikutnya: `ls db/migrations` → `0000N` + 1 (5 digit). Nama file `0000N_<nama>.sql` snake_case.
2. Isi dengan `-- +goose Up` dan `-- +goose Down` yang benar-benar membalikkan. Index besar → `CREATE INDEX CONCURRENTLY` butuh `-- +goose NO TRANSACTION`.
3. Jangan ubah migrasi lama. Kolom baru untuk data user tetap ber-`user_id` bila tabel baru.
4. Sesuaikan `internal/repository` (scan kolom) dan `internal/model`.
5. `go run ./cmd/vaulty migrate` lokal, lalu `migrate status`.
6. Update `../vaulty-vault/02 Architecture/Database.md` (tabel/kolom/index) dan `updated:`.
