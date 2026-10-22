---
paths:
  - "internal/handler/**"
  - "internal/router/**"
  - "internal/middleware/**"
---
# HTTP API (/api/v1)

- Kontrak: `../vaulty-vault/02 Architecture/API Kontrak v1.md`. Perubahan breaking butuh versi baru (`/api/v2`), bukan mengubah v1.
- Semua operasi lewat `huma.Register` (OpenAPI otomatis). Operasi yang butuh login: `Security: middleware.Secured`; user dari `middleware.UserFrom(ctx)`. Jangan pernah ambil `user_id` dari input client.
- Handler hanya validasi & pemetaan → panggil service → `handler.E(err)`. Tanpa SQL, tanpa logika bisnis.
- Upload: multipart hanya di `/items/upload` dan `/items/{id}/attachments` (`RawBody multipart.Form`), batas `service.MaxUploadBytes(plan)`.
- Kuota habis → biarkan `model.QuotaError` lewat `handler.E` menjadi 402.
- SSE (`/chat`) memakai `sse.Register`; urutan event: meta → token* → citations → done, atau error.
- Setelah menambah/mengubah endpoint: `make openapi` (api harus jalan) dan catat di dev log supaya client Flutter di-generate ulang.
