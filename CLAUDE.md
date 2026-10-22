# vaulty-backend

Backend Vaulty: simpan apa pun (link/gambar/teks/suara/file) → Vee (AI) memahami → tanya dengan citation → reminder + push. Go 1.26, chi + huma v2 (OpenAPI), pgx + goose (tanpa sqlc), asynq (Redis), pgvector, S3-compatible storage, API kompatibel OpenAI, FCM, RevenueCat.

Dokumentasi produk & arsitektur: `../vaulty-vault/Home.md` (mulai dari sini), kontrak API: `../vaulty-vault/02 Architecture/API Kontrak v1.md`, ADR: `../vaulty-vault/03 Decisions/`. Knowledge graph: `graphify-out/` di `../` (pakai `graphify query` untuk pertanyaan arsitektur).

## Peta
```
cmd/vaulty            satu binary: api | worker | migrate [status]
db/migrations         goose SQL (embed)
internal/config       env → Config
internal/bootstrap    wiring bersama api & worker
internal/model        struct domain + model.Err*
internal/repository   pgx, semua query ber-user_id
internal/service      logika: auth, entitlement, item, ingest, search, answer, reminder, billing, account
internal/handler/v1   huma operations (+ handler.E pemetaan error)
internal/middleware   Bearer auth, request log
internal/router       chi + huma + /api/docs + file lokal
internal/worker       task asynq + pekerjaan periodik (reminder, safety net, housekeeping)
pkg/ai|fetch|extract|push|storage|password|googleauth   library tanpa import internal/
```

## Perintah
`make dev-deps` (Postgres 5435, Redis 6380; `--profile s3` untuk MinIO) · `make tunnel` (Hermes + 9router di sipantas; WAJIB untuk AI, jangan pakai Ollama lokal) · `make migrate` · `make api` (Swagger http://localhost:8081/api/docs) · `make worker` · `make verify` (fmt+vet+test) · `make openapi`.

## Aturan singkat
Lihat `.claude/rules/`. Intinya: user_id di semua query; kerja berat di worker; task idempoten per `content_version`; AI hanya lewat `pkg/ai`; citation tervalidasi; kuota di backend; tidak ada credential; jangan baca `.env`. Keputusan teknis baru → `/new-adr` dulu. Akhir sesi → `/dev-log`.
