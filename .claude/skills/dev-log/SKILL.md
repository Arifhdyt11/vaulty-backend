---
name: dev-log
description: Mencatat progres kerja hari ini ke dev log harian di vault Obsidian (../vaulty-vault/04 Dev Log/YYYY-MM-DD.md). Gunakan di akhir sesi kerja atau saat user minta catat progres.
argument-hint: [catatan tambahan]
disable-model-invocation: true
---
# Dev log hari ini

Catatan tambahan dari user: $ARGUMENTS

1. **Kumpulkan bahan**: ringkasan pekerjaan di sesi ini, `git log --since=midnight --oneline` dan `git status --short` di repo ini.
2. **File**: `../vaulty-vault/04 Dev Log/<YYYY-MM-DD>.md` (tanggal hari ini).
   - Belum ada → salin `../vaulty-vault/Templates/Dev Log Template.md`, ganti `{{title}}`/`{{date}}` manual.
   - Sudah ada → tambahkan di bagian yang sesuai, jangan timpa catatan lama; update `updated:` bila field itu ada.
3. **Isi** singkat, berupa poin:
   - Yang selesai (prefix `backend:`), dengan wikilink ke note/ADR terkait (`[[Backend]]`, `[[ADR-007 Scope user_id Dipaksa Middleware dan Repository]]`).
   - Keputusan baru → sarankan `/new-adr` bila belum ada ADR-nya.
   - Blocker / selisih kontrak dengan backend.
   - Next step sebagai checklist `- [ ]`.
4. Kalau status implementasi berubah, tawarkan update bagian "Status implementasi" di `02 Architecture/Backend.md`.
5. Jangan sertakan isi `.env`, token, atau data note user.
