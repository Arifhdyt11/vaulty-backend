---
name: new-migration
description: Membuat migration goose baru di db/migrations (nomor berikutnya, Up + Down, aman untuk data yang ada) lalu regenerate sqlc. Gunakan saat perlu menambah/mengubah tabel, kolom, index, atau constraint.
argument-hint: <deskripsi_snake_case>
---
# Migration baru: $ARGUMENTS

1. **Nomor**: `ls db/migrations/`, ambil nomor terbesar + 1, format 5 digit → `db/migrations/NNNNN_<snake_case>.sql`. Jangan pernah mengubah file migration yang sudah ada.
2. **Isi** dengan pola:
   ```sql
   -- +goose Up
   -- <alasan perubahan, sebut ADR/F-requirement bila ada>
   ALTER TABLE ...;

   -- +goose Down
   ALTER TABLE ...;
   ```
   - `Down` harus benar-benar membalik `Up`.
   - Kolom baru: nullable atau punya `DEFAULT`; `NOT NULL` tanpa default hanya setelah backfill di migration terpisah.
   - Index pada tabel besar: `CREATE INDEX CONCURRENTLY` + `-- +goose NO TRANSACTION` di atas file.
   - Tabel data user wajib punya `user_id` (FK ke `users`) dan `deleted_at` (ADR-003, ADR-006, ADR-007).
   - Fungsi/trigger multi-statement dibungkus `-- +goose StatementBegin` / `StatementEnd`.
   - Tanpa kolom/tipe credential (ADR-016).
3. **Query**: sesuaikan `db/queries/*.sql` yang terdampak (kolom baru ikut di `SELECT`/`RETURNING`).
4. **Generate**: `make sqlc`, lalu perbaiki mapping di `internal/repository` (`toNote`, dst.) dan `internal/model`.
5. **Uji**: `go build ./... && go test ./...`. Menjalankan ke DB dev (`make migrate`) minta izin user dulu; uji juga `Down` bila memungkinkan (`go run ./cmd/migrate down` juga minta izin).
6. **Dokumentasi**: perbarui skema di `../vaulty-vault/02 Architecture/Database.md` (update `updated:`). Keputusan skema yang signifikan → `/new-adr`.
