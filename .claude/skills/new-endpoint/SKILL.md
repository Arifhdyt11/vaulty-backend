---
name: new-endpoint
description: Menambah endpoint /api/v1 baru di vaulty-backend mengikuti layering handler → service → repository, kontrak API, dan OpenAPI huma.
argument-hint: <METHOD /path ringkasan>
disable-model-invocation: true
---
# Endpoint baru: $ARGUMENTS

1. **Kontrak dulu**: tambah/ubah tabel endpoint di `../vaulty-vault/02 Architecture/API Kontrak v1.md` (request, response, error). Kalau breaking untuk client lama → bukan v1, buat ADR.
2. **Repository** (`internal/repository/*_repo.go`): query pgx dengan `user_id` di WHERE; kembalikan `model.*`; `mapErr`.
3. **Service** (`internal/service/*_service.go`): validasi (`model.Invalid`), aturan bisnis, kuota lewat `EntitlementService` bila konsumsi AI/penyimpanan, audit bila perlu.
4. **Handler** (`internal/handler/v1/*.go`): `huma.Register` dengan `OperationID` kebab-case, `Tags`, `Security: middleware.Secured` bila perlu login, `DefaultStatus` untuk 201/202/204; input struct pakai tag `path/query/json`; error lewat `handler.E`.
5. Daftarkan di `Register*` yang dipanggil `router.New`.
6. **Test** untuk logika service baru; `.claude/skills/verify/scripts/verify.sh`.
7. `make openapi` lalu catat di dev log bahwa client Flutter perlu di-generate ulang.
