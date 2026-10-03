---
name: new-endpoint
description: Menambah endpoint REST baru di vaulty-backend secara end-to-end (SQL sqlc → repository → service → handler huma → test → dokumentasi vault). Gunakan saat user minta endpoint/fitur API baru.
argument-hint: <METHOD /path> <deskripsi singkat>
---
# Endpoint baru: $ARGUMENTS

Sebelum mulai, baca `../vaulty-vault/02 Architecture/Backend.md` (tabel endpoint) dan ADR terkait. Kalau endpoint mengubah kontrak `/api/v1` secara breaking, berhenti dan tanyakan apakah perlu `/api/v2`.

1. **Model**: struct domain/input baru di `internal/model/`. Error domain baru → `model.Err*` + mapping di `internal/handler/errors.go` (`ToHTTPError`).
2. **SQL** (bila perlu data baru): tulis query di `db/queries/<resource>.sql` dengan filter `user_id` dan `deleted_at IS NULL`. Butuh kolom/tabel baru → skill `/new-migration` dulu. Lalu `make sqlc`.
3. **Repository**: method di `internal/repository/<resource>_repository.go` yang membungkus query sqlc, menerima `userID`, mengembalikan `model.*`, dan memetakan no-rows via `notFound`.
4. **Service**: logika bisnis + validasi di `internal/service/<resource>_service.go`. Kerja berat → enqueue task worker, bukan sinkron.
5. **Handler**: di `internal/handler/v1/<resource>.go`:
   - request/response struct unexported dengan tag validasi huma + `doc`;
   - `huma.Register` di `Register()` dengan `OperationID` `<resource>-<aksi>`, `Summary` Bahasa Indonesia, `Tags`, `Security: middleware.Secured`, `DefaultStatus` bila bukan 200;
   - user dari `middleware.CurrentUser(ctx)`, error lewat `handler.ToHTTPError`;
   - catat audit untuk aksi tulis.
   Handler baru (resource baru) → daftarkan di `internal/handler/v1/v1.go`.
6. **Test**: table-driven untuk validasi service dan helper handler (delegasikan ke agent `test-writer` bila banyak). Wajib kasus negatif (input invalid, akses lintas user).
7. **Verifikasi**: `/verify`. Cek OpenAPI di `http://localhost:8080/api/v1/openapi.json` bila server jalan.
8. **Dokumentasi**: tambah baris di tabel endpoint `Backend.md` (update `updated:`), dan catat bila `vaulty-frontend` perlu menyesuaikan tipe/client.
