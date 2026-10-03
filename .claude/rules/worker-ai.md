---
paths:
  - "internal/worker/**"
  - "internal/service/index_service.go"
  - "internal/service/search_service.go"
  - "pkg/aiagent/**"
  - "pkg/extract/**"
  - "cmd/worker/**"
---
# Worker, AI & search

- Pekerjaan berat (ekstraksi file, auto-tag, embedding) hanya di worker lewat task asynq; service cukup `EnqueueIndex`. Request HTTP tidak boleh menunggu LLM.
- Nama task `<resource>:<aksi>` (konstanta `Task*` di `internal/worker/tasks.go`), payload struct JSON kecil berisi ID (bukan isi note).
- Task harus **idempoten**: pakai `asynq.TaskID` deterministik (mis. `note-index:<id>:<version>`) dan abaikan `ErrTaskIDConflict`. Cek `content_version` supaya hasil versi lama tidak menimpa yang baru.
- Gagal enqueue tidak menggagalkan request: log warning, safety net `TaskRequeuePending` yang menyusul.
- Handler task tetap men-scope data dengan `user_id` dari payload (ADR-007).
- Provider AI hanya lewat interface di `pkg/aiagent` (API kompatibel OpenAI, ADR-008). Jangan mengimpor SDK vendor langsung di service.
- Isi note bertipe `command` tidak boleh ditulis ulang LLM (ADR-010); LLM hanya memilih/menandai, body ditampilkan verbatim.
- AI nonaktif (`OPENAI_API_KEY` kosong) harus tetap jalan: search jatuh ke full-text, auto-tag dilewati.
- Search hybrid memakai RRF (ADR-002); embedding dari model berbeda tidak dibandingkan (kolom `embedding_model`).
- Jangan log prompt/isi note/hasil LLM; cukup ID, model, durasi, dan error.
