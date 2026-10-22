# Vaulty API

Backend Vaulty: simpan apa pun yang kamu temukan (link video/artikel, gambar, tulisan, suara, file) → Vee (AI) mengambil isi & meringkas → tanya dengan jawaban berkutipan → diingatkan tepat waktu. Go 1.26, chi + huma v2 (OpenAPI), PostgreSQL + pgvector, Redis + asynq, S3-compatible storage, API kompatibel OpenAI, FCM, RevenueCat.

Dokumentasi produk & arsitektur ada di vault Obsidian `../vaulty-vault/` (mulai dari `Home.md`). Kontrak API: `../vaulty-vault/02 Architecture/API Kontrak v1.md`; bentuk finalnya `openapi.json` di repo ini (`make openapi`).

## Struktur

```
cmd/vaulty/         # satu binary: api | worker | migrate [status]
db/migrations/      # goose SQL, ter-embed di binary
internal/
├── bootstrap/      # inisialisasi bersama api & worker
├── config/         # env → Config
├── database/       # pool pgx + runner migrasi
├── handler/v1/     # operasi huma /api/v1 (+ handler.E pemetaan error)
├── middleware/     # Bearer auth, request id, access log
├── model/          # struct domain, model.Err*
├── repository/     # pgx (tanpa sqlc); semua query data user ber-user_id
├── router/         # chi + huma, /healthz, /readyz, /api/docs, file lokal (dev)
├── service/        # auth, entitlement, item, ingest, search, answer, reminder, billing, account
└── worker/         # task asynq + pekerjaan periodik (reminder, safety net, housekeeping)
pkg/                # library tanpa import internal/
├── ai/             # client API kompatibel OpenAI: chat (multimodal), embedding, STT
├── fetch/          # metadata/isi URL (OG, readability, oEmbed, caption YouTube) + anti-SSRF
├── extract/        # teks dari PDF/DOCX/teks
├── push/           # FCM HTTP v1
├── storage/        # S3-compatible (R2/MinIO) atau disk lokal
├── password/       # argon2id
└── googleauth/     # verifikasi id_token Google
```

Alur: `router` → `middleware` → `handler/v1` → `service` → `repository` → PostgreSQL. Kerja berat (fetch, STT, vision, ringkasan, embedding) dijadwalkan ke `worker` lewat Redis; capture selalu instan.

## Menjalankan (development)

Butuh Go 1.26, Docker, `yt-dlp` + `ffmpeg` (`brew install yt-dlp ffmpeg`, untuk isi video), dan akses SSH ke sipantas (AI lewat Hermes + 9router).

```bash
cp .env.example .env     # isi AI_API_KEY & AI_EMBED_API_KEY; port dev 8081 (8080 dipakai proyek lain)
make dev-deps            # Postgres pgvector :5435, Redis :6380 (MinIO: docker compose --profile s3 up -d)
make tunnel              # terminal terpisah: tunnel ke Hermes :8642 & 9router :20128, menyambung ulang otomatis
make migrate
make api                 # Swagger: http://localhost:8081/api/docs
make worker              # terminal terpisah
```

Chat/ringkasan lewat Hermes (`hermes-vaulty`), embedding lewat 9router (`openai/text-embedding-3-small`, 1536 dim). Untuk OCR gambar butuh model multimodal di `AI_VISION_MODEL`; untuk transkrip suara butuh endpoint STT Whisper-kompatibel (`AI_STT_*`). Kalau tunnel mati, capture tetap tersimpan dan item diproses ulang oleh safety net worker setelah tunnel hidup (atau `go run ./cmd/vaulty requeue`).

Push butuh `FCM_CREDENTIALS_FILE` (service account Firebase). Billing butuh `REVENUECAT_WEBHOOK_SECRET` (+ `REVENUECAT_API_KEY` untuk `/billing/sync`).

## Impor catatan Vaulty v1

```bash
ssh sipantas "docker exec shared-postgres pg_dump -U vaulty -d vaulty -t notes --data-only --column-inserts" > notes-v1.sql
scripts/import-v1.sh notes-v1.sql email@anda.com           # pratinjau
scripts/import-v1.sh notes-v1.sql email@anda.com --apply   # impor (idempoten, tanpa kuota)
```

Note → item teks, link → item URL (di-fetch ulang), command → item teks verbatim bertag `command`. Catatan berisi pola credential dilewati dan dilaporkan.

## Perintah lain

```bash
make verify      # gofmt + go vet + go test
make openapi     # simpan openapi.json dari API yang sedang jalan
make build       # binary ke ./bin/vaulty
go run ./cmd/vaulty requeue   # enqueue ulang item yang macet di queued/processing
```

## Status

- Selesai & teruji lokal: auth (email/password, refresh rotasi), capture url/teks/gambar/suara/file, pipeline fetch→transcribe→understand→index, search full-text + hybrid, chat SSE dengan citation tervalidasi, reminder (kalimat natural & terstruktur, berulang, bertahap, snooze), devices, kuota Free/Pro + 402, webhook RevenueCat, ekspor data, hapus akun.
- Belum diuji dengan layanan nyata: login Google, push FCM, RevenueCat, S3/R2 (MinIO opsional), STT, vision (perlu model multimodal).
- Isi video YouTube/TikTok/Instagram (Pro) dibaca dari transkrip Whisper + frame vision (ADR-015). Instagram yang minta login dan link Dribbble jatuh ke caption/screenshot.
