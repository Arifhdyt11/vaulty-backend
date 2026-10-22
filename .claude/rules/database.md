---
paths:
  - "db/**"
  - "internal/repository/**"
  - "internal/database/**"
---
# Database

- Migrasi goose di `db/migrations/0000N_nama.sql` dengan `-- +goose Up` / `-- +goose Down`. Migrasi yang sudah di-commit tidak diubah; buat file baru.
- Query ditulis tangan dengan pgx di `internal/repository` (tanpa sqlc, ADR-002). Semua query data user WAJIB `WHERE user_id = $n` (ADR-007), termasuk UPDATE/DELETE.
- Soft delete (`deleted_at`) untuk items & conversations; hard delete hanya oleh worker (sampah kedaluwarsa, hapus akun).
- Worker menulis hasil hanya bila `content_version` sama (idempoten, ADR-006).
- `item_chunks` adalah unit search & citation; embedding dimensi ≤ 1536 (`AI_EMBED_DIM`), kolom `embedding_model` untuk re-index saat ganti model.
- Setelah mengubah skema: update `../vaulty-vault/02 Architecture/Database.md`.
