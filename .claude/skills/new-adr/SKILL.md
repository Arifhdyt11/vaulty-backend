---
name: new-adr
description: Menulis ADR (Architecture Decision Record) baru di vault Obsidian ../vaulty-vault/03 Decisions dengan nomor berikutnya dan menautkannya ke note komponen. Gunakan saat ada keputusan teknis baru (dependency, pola arsitektur, kontrak API) atau user minta ADR.
argument-hint: <judul keputusan>
disable-model-invocation: true
---
# ADR baru: $ARGUMENTS

1. **Nomor berikutnya**: `ls "../vaulty-vault/03 Decisions/"`, ambil `ADR-NNN` terbesar + 1 (3 digit).
2. **Cek tumpang tindih**: Grep judul/kata kunci di `03 Decisions/`. Kalau mengganti ADR lama, catat untuk langkah 5.
3. **Template**: baca `../vaulty-vault/Templates/ADR Template.md`, salin ke `../vaulty-vault/03 Decisions/ADR-NNN <Judul>.md`.
   - Ganti `{{title}}` dan `{{date}}` (hari ini `YYYY-MM-DD`). Nama file tanpa `/ \ : # ^ [ ] |`.
   - `status: proposed` kecuali user bilang sudah diputuskan (`accepted`).
   - Isi **Konteks**, **Opsi**, **Keputusan**, **Konsekuensi**, **Terkait** singkat dan konkret; rujuk BRD (`[[Vaulty BRD#5. Kebutuhan Fungsional|F5]]`) bila relevan.
   - `related:` berisi wikilink note komponen terdampak.
4. **Tautkan** ADR di bagian "Keputusan terkait (ADR)" pada note komponen di `02 Architecture/`, update `updated:`.
5. **Supersede** bila ada: ADR lama `status: superseded`, `superseded_by: "[[ADR-NNN <Judul>]]"`, callout `> [!warning]`. Jangan hapus ADR lama.
6. Jangan sentuh `.obsidian/`. Laporkan path file baru dan note yang diubah.
