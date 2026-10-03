# Vaulty API

Backend Vaulty (Personal Knowledge Assistant): Go + Gin + huma, PostgreSQL + pgvector, Redis + asynq.
Dokumentasi produk & arsitektur ada di Obsidian vault `../vaulty-vault/` (mulai dari `Home.md`).

## Struktur

```
cmd/
├── api/            # HTTP server (/api/v1)
├── worker/         # asynq worker: indexing note, safety net, cleanup session
└── migrate/        # migration database (goose)
db/
├── migrations/     # SQL migration (ter-embed di binary)
└── queries/        # SQL untuk sqlc -> internal/repository/queries
internal/
├── bootstrap/      # inisialisasi bersama (env, logger, provider AI)
├── config/         # konfigurasi dari environment (.env)
├── database/       # koneksi PostgreSQL & runner migration
├── handler/        # layer HTTP; error mapping bersama semua versi
│   └── v1/         # handler + request/response /api/v1 (kontrak v1)
├── middleware/     # auth Bearer, request ID, access log, batas ukuran upload
├── model/          # struktur data domain & error
├── repository/     # akses data (membungkus sqlc); semua query data user di-scope user_id
│   └── queries/    # kode hasil generate sqlc (jangan diedit manual)
├── router/         # gin + middleware global, /healthz, /readyz, Swagger UI
│                   # router.go (umum), v1.go (/api/v1), docs.go (/api/docs)
├── service/        # logika bisnis (auth, note, search, indexing, audit)
└── worker/         # definisi task asynq & handler-nya
pkg/                # library yang tidak bergantung pada Vaulty
├── aiagent/        # provider AI agent Vee (OpenAI / Ollama): embedding & auto-tag
├── extract/        # ekstraksi teks PDF, DOCX, teks/kode
├── googleauth/     # verifikasi id_token Google
├── password/       # hashing argon2id
└── storage/        # penyimpanan file (lokal; S3/R2 nanti)
```

Alur request: `router` → `middleware` → `handler/vN` → `service` → `repository` → PostgreSQL.
Pekerjaan berat (ekstraksi file, AI) dijadwalkan service ke `worker` lewat Redis.

## Versi API

Setiap versi punya prefix (`/api/v1`, `/api/v2`, ...) dan OpenAPI sendiri (`/api/vN/openapi.json`).
Swagger UI di **`/api/docs`** menampilkan semua versi lewat dropdown.

Menambah versi baru (hanya untuk perubahan yang breaking):
1. Buat `internal/handler/v2/` (salin handler v1 yang berubah; yang tidak berubah boleh memakai ulang v1).
2. Buat `internal/router/v2.go` dengan `r.newVersion("v2", "/api/v2", "2.0.0")`, lalu panggil `r.v2()` di `router.New`.
3. Service, repository, dan model dipakai bersama, jadi tidak perlu diduplikasi.

## Skalabilitas

- API stateless: session di Postgres, antrean di Redis, sehingga api dan worker bisa dijalankan banyak instance di belakang load balancer.
- `/healthz` (liveness) dan `/readyz` (cek database + Redis) untuk load balancer / orchestrator.
- Setiap request punya `X-Request-ID` (dipakai ulang dari proxy jika ada) dan access log terstruktur (JSON di production).
- `DB_MAX_CONNS` membatasi pool per proses; total semua instance harus di bawah `max_connections` Postgres.
- Pekerjaan berat di worker (asynq); tambah instance worker atau `WORKER_CONCURRENCY` untuk throughput indexing.
- Batasan saat ini: file upload di disk lokal (`STORAGE_DIR`). Untuk lebih dari satu server, ganti implementasi `pkg/storage` ke S3/R2.

## Menjalankan (development)

Butuh Go 1.26, Docker (Postgres), Redis lokal, dan Ollama (opsional, untuk AI).

```bash
cp .env.example .env      # isi ALLOWED_EMAILS dengan email Anda
make dev-deps             # Postgres pgvector (container vaulty-postgres, port 5434)
make migrate
make api                  # Swagger: http://localhost:8080/api/docs
make worker               # terminal terpisah
```

Model Ollama default: `ollama pull bge-m3` (embedding) dan `ollama pull qwen2.5:7b-instruct` (auto-tag).
Pindah ke OpenAI cukup ganti blok "Provider AI" di `.env` lalu restart api + worker; note lama
otomatis di-embed ulang dengan model baru.

## Perintah lain

```bash
make test     # unit test
make sqlc     # generate ulang internal/repository/queries setelah mengubah db/queries atau migration
make build    # binary ke ./bin
```
