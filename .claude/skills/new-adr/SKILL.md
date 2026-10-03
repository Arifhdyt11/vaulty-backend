---
name: new-adr
description: Menulis ADR (Architecture Decision Record) baru di vault Obsidian ../vaulty-vault/03 Decisions dengan nomor berikutnya dan menautkannya ke note komponen. Gunakan saat ada keputusan teknis baru (dependency, pola arsitektur, kontrak API) atau user minta ADR.
argument-hint: <judul keputusan>
disable-model-invocation: true
---
# ADR baru: $ARGUMENTS

1. **Nomor berikutnya**: `ls "../vaulty-vault/03 Decisions/"`, ambil nomor `ADR-NNN` terbesar + 1 (3 digit).
2. **Cek tumpang tindih**: `Grep` judul/kata kunci di `03 Decisions/`. Kalau keputusan ini mengganti ADR lama, catat untuk langkah 5.
3. **Template**: baca `../vaulty-vault/Templates/ADR Template.md`, salin ke `../vaulty-vault/03 Decisions/ADR-NNN <Judul>.md`.
   - Ganti `{{title}}` dan `{{date}}` manual (tanggal hari ini `YYYY-MM-DD`).
   - Nama file tanpa `/ \ : # ^ [ ] |`.
   - `status: proposed` kecuali user bilang sudah diputuskan (`accepted`).
   - Isi **Konteks**, **Keputusan**, **Konsekuensi**, **Terkait** dengan singkat dan konkret; rujuk requirement BRD (`[[Vaulty BRD#...|F6]]`) bila relevan.
   - `related:` berisi wikilink note komponen yang terdampak.
4. **Tautkan** ADR di bagian "Keputusan terkait (ADR)" pada note komponen di `02 Architecture/` (mis. `Backend.md`), lalu update `updated:` di frontmatter note itu.
5. **Supersede** (bila ada): di ADR lama set `status: superseded`, tambah `superseded_by: "[[ADR-NNN <Judul>]]"` dan callout `> [!warning]`. Jangan hapus ADR lama.
6. Jangan sentuh `.obsidian/`. Laporkan path file baru dan note yang diubah.
