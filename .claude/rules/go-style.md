---
paths:
  - "**/*.go"
---
# Gaya kode Go

- Format `gofmt` (otomatis via hook). Import dikelompokkan: stdlib, pihak ketiga, lalu `vaulty/...`.
- Error dibungkus dengan konteks: `fmt.Errorf("simpan item: %w", err)`. Bandingkan dengan `errors.Is`/`errors.As`.
- Error domain baru → `model.Err*` di `internal/model/errors.go` **dan** petakan di `handler.E`. Pesan Bahasa Indonesia (tampil ke client).
- `context.Context` selalu parameter pertama; jangan `context.Background()` di dalam request.
- Constructor `NewXxx(deps...) *Xxx`; dependency lewat field struct, tanpa global state. Interface didefinisikan di sisi pemakai (contoh `service.Enqueuer`).
- Logging hanya `log/slog` varian `...Context(ctx, ...)` dengan key-value. Jangan log prompt, isi item, token.
- `pkg/*` tidak boleh mengimpor `vaulty/internal/...`.
- Komentar Bahasa Indonesia menjelaskan *kenapa*; sebut ADR bila relevan (`// ADR-007`).
- Jangan tambah dependency (`go get`) tanpa ADR baru.
