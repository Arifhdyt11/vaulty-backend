---
paths:
  - "db/**"
  - "internal/repository/**"
  - "internal/database/**"
  - "sqlc.yaml"
---
# Database, sqlc & migration

- Hanya `internal/repository` yang menyentuh DB. Service memanggil repository, tidak pernah `queries.*` langsung.
- Query ditulis di `db/queries/*.sql` dengan anotasi `-- name: Xxx :one|:many|:exec`, lalu `make sqlc`. `internal/repository/queries/` hasil generate: **jangan diedit**.
- Setiap query data user **wajib** `WHERE user_id = sqlc.arg(user_id)` (ADR-007). Pengecualian hanya query worker yang tidak mengembalikan isi note, dan wajib diberi komentar alasan.
- Query baca selalu `AND deleted_at IS NULL` (soft delete, ADR-006). Delete = set `deleted_at`, bukan `DELETE`.
- Parameter nullable pakai `sqlc.narg(...)::type`; jangan merangkai SQL dengan string (`fmt.Sprintf`) dari input user.
- Repository mengubah `pgx.ErrNoRows` jadi `model.ErrNotFound` (helper `notFound`), dan mengembalikan `model.*`, bukan tipe `queries.*`.
- Migration: file baru `db/migrations/NNNNN_<snake_case>.sql` (5 digit, nomor berikutnya) dengan blok `-- +goose Up` dan `-- +goose Down`. **Jangan ubah migration yang sudah ada.**
- Migration harus aman untuk data yang ada: kolom baru nullable/default, index besar `CONCURRENTLY` (+ `-- +goose NO TRANSACTION`), backfill terpisah dari perubahan skema.
- Kolom/fitur credential dilarang (ADR-004, ADR-016). Embedding hanya dibandingkan dengan `embedding_model` yang sama.
- Perubahan skema → perbarui `../vaulty-vault/02 Architecture/Database.md`.
