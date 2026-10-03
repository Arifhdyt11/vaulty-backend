---
paths:
  - "internal/handler/**"
  - "internal/router/**"
  - "internal/middleware/**"
---
# Handler, router & kontrak API

- Handler hanya: parsing input huma → panggil service → map error via `handler.ToHTTPError(err)` → bentuk response. Tanpa SQL, tanpa logika bisnis.
- Endpoint didaftarkan dengan `huma.Register` di method `Register(api huma.API)` handler; path relatif (`/notes`), prefix dari `internal/router/vN.go`.
- `OperationID` unik berpola `<resource>-<aksi>` (`notes-list`), `Summary` Bahasa Indonesia, `Tags` per resource, `DefaultStatus` eksplisit untuk 201/202/204.
- Route yang butuh login: `Security: middleware.Secured`, user dari `middleware.CurrentUser(ctx)`. Jangan pernah menerima `user_id` dari body/query.
- Validasi pakai tag huma (`maxLength`, `maxItems`, `minimum`, `maximum`, `pattern`) + `doc`/`example` agar OpenAPI informatif.
- Struct yang di-embed di input huma harus tipe **exported** (huma mengabaikan field embedded unexported).
- Tipe request/response unexported dan per versi (di `handler/vN`); model domain dari `internal/model`.
- `/api/v1` adalah kontrak yang dipakai web & iOS: menambah field opsional boleh; menghapus/mengganti nama/mengubah tipe field, status code, atau semantik = **breaking** → `handler/v2` + `router/v2.go` (ADR-005).
- Aksi capture/edit/delete/login dicatat ke audit log (ADR-006); metadata audit tanpa isi note.
- Setiap perubahan kontrak: perbarui tabel endpoint di `../vaulty-vault/02 Architecture/Backend.md` dan beri tahu bila FE (`vaulty-frontend/src/lib/api/types.ts`) perlu menyesuaikan.
