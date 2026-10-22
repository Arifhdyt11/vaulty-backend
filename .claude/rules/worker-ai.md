---
paths:
  - "internal/worker/**"
  - "internal/service/ingest_service.go"
  - "internal/service/answer_service.go"
  - "internal/service/search_service.go"
  - "internal/service/prompts.go"
  - "pkg/ai/**"
  - "pkg/fetch/**"
---
# Worker, AI & search

- Kerja berat (fetch, STT, vision, LLM, embedding) hanya di worker lewat task asynq; request HTTP tidak menunggu LLM kecuali `/chat` (streaming) dan parse reminder.
- Task `<resource>:<aksi>`, payload hanya ID. `asynq.TaskID` deterministik (`item-ingest:<id>:<version>`), abaikan `ErrTaskIDConflict`.
- Pipeline ingest: fetch → transcribe → understand → index; tiap tahap dicatat di `items.ingest`, hanya jalan bila `pending`. Capture tidak boleh gagal karena AI.
- Provider AI hanya lewat interface `pkg/ai` (API kompatibel OpenAI, ADR-005). Parsing JSON LLM pakai `ai.ParseJSONLoose`.
- Jawaban Vee wajib citation `chunk_id` yang ada di hasil search; ID lain dibuang (ADR-009). Tidak ada tool eksekusi apa pun.
- AI nonaktif (`AI_BASE_URL` kosong) harus tetap jalan: search full-text, ringkasan dilewati, chat 503.
- Fitur Pro (STT, usulan reminder AI) dicek lewat `service.RulesFor(plan)` / `EntitlementService`, bukan hardcode.
- Jangan log prompt/isi item/hasil LLM; cukup ID, model, durasi, error.
