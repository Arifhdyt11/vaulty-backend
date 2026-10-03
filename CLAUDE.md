# Vaulty Backend (vaulty-api)

API + worker Vaulty (Personal Knowledge Assistant). Satu codebase, tiga binary: `cmd/api`, `cmd/worker`, `cmd/migrate` (ADR-001).

## Stack
- Go 1.26, Gin + **huma v2** (validasi input & OpenAPI 3.1 dari kode)
- PostgreSQL 17 + pgvector, `pgx/v5` + **sqlc**, migration **goose** (ter-embed di binary)
- Redis + **asynq** (queue & scheduler worker)
- AI lewat API kompatibel OpenAI (`pkg/aiagent`): dev Ollama (`bge-m3`, `qwen2.5:7b-instruct`), prod OpenAI
- Test: package `testing` standard library saja (tanpa testify/mock lib)

## Command
- `make dev-deps`: Postgres pgvector (container `vaulty-postgres`, port 5434). Redis & Ollama lokal
- `make migrate` → `make api` (Swagger semua versi: http://localhost:8080/api/docs) → `make worker`
- `make test` (= `go test ./...`), `go vet ./...`, `make build`
- `make sqlc`: **wajib** setelah mengubah `db/queries/*.sql` atau migration
- Verifikasi lengkap: skill `/verify`

## Alur & struktur
`router` → `middleware` → `handler/vN` → `service` → `repository` (sqlc) → PostgreSQL. Struktur lengkap di `README.md`.
- `internal/handler/vN`: parsing request + mapping error (`handler.ToHTTPError`). Logika bisnis di `internal/service`.
- `internal/repository`: satu-satunya layer yang menyentuh DB. `internal/repository/queries` = hasil sqlc, **jangan diedit manual**.
- `internal/model`: struct domain & error `model.Err*`, dipakai semua layer.
- `pkg/`: library yang tidak boleh mengimpor `internal/`.
- Pekerjaan berat (ekstraksi file, AI) di worker lewat task asynq, bukan di request HTTP.
- Komentar, pesan error, dan doc OpenAPI dalam Bahasa Indonesia.

## Larangan
- Jangan baca/tulis `.env` (berisi secret). Referensi: `.env.example`
- Jangan ubah kontrak `/api/v1` secara breaking; buat `handler/v2` + `router/v2.go`
- Jangan ubah migration yang sudah ada; buat migration baru
- Jangan query data user tanpa filter `user_id` (ADR-007)
- Jangan tambah tipe credential atau melewatkan isi `command` ke LLM untuk ditulis ulang (ADR-016, ADR-010)
- Jangan tambah dependency (`go get`) tanpa ADR

Aturan detail per area ada di `.claude/rules/` (dimuat otomatis sesuai path).

## Dokumentasi (Obsidian vault)
Sumber kebenaran produk & arsitektur ada di `../vaulty-vault/` (bukan di repo ini). Mulai dari `../vaulty-vault/Home.md`:

- `01 Product/Vaulty BRD.md`: requirement (F1–F22, NF1–NF11), scope, roadmap
- `02 Architecture/`: satu note per komponen (mulai dari `Backend.md`, `Database.md`, `Search.md`, `Vee AI Layer.md`)
- `03 Decisions/`: ADR, satu file per keputusan
- `04 Dev Log/`: catatan progres harian (`YYYY-MM-DD.md`)

Baca note yang relevan sebelum mengerjakan fitur atau mengambil keputusan arsitektur. Jangan mengambil keputusan yang bertentangan dengan ADR berstatus `accepted` tanpa bertanya dulu. Aturan format vault: `.claude/rules/vault.md`; workflow: skill `/new-adr` dan `/dev-log`.
