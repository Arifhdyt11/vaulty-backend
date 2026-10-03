---
paths:
  - "**/*.go"
---
# Gaya kode Go

- Format `gofmt` (otomatis via hook). Import dikelompokkan: stdlib, pihak ketiga, lalu `vaulty-api/...`.
- Error dibungkus dengan konteks: `fmt.Errorf("simpan note: %w", err)`. Bandingkan pakai `errors.Is`/`errors.As`, bukan `==` pada string.
- Error domain baru → tambah `model.Err*` di `internal/model/errors.go` **dan** petakan di `handler.ToHTTPError`. Pesan error Bahasa Indonesia (tampil ke client).
- `context.Context` selalu parameter pertama dan diteruskan ke DB/HTTP/asynq. Jangan `context.Background()` di dalam request.
- Constructor `NewXxx(deps...) *Xxx`; dependency lewat field struct, tanpa global state. Interface didefinisikan di sisi pemakai (contoh: `service.IndexEnqueuer`).
- Logging hanya `log/slog` dengan varian `...Context(ctx, ...)` dan key-value (`"note_id", id, "err", err`).
- `pkg/*` tidak boleh mengimpor `vaulty-api/internal/...`.
- Komentar Bahasa Indonesia, menjelaskan *kenapa*; sebut ADR bila relevan (`// ADR-007`).
- Jangan tambah dependency (`go get`) tanpa ADR. Utamakan stdlib.
