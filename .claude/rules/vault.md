# Aturan vault Obsidian (`../vaulty-vault/`)

- Boleh create/update/delete note jika diminta. Jangan sentuh `.obsidian/` dan `_archive/`.
- Pakai template di `Templates/` (ADR, Architecture Note, Dev Log); ganti `{{title}}`/`{{date}}` manual.
- Format: YAML frontmatter (`title`, `tags`, `type`, `status`, `created`/`updated` atau `date`, `related`), satu H1, wikilink `[[Nama Note]]` (nama file tanpa path/ekstensi, unik), callout `> [!note]`, checklist `- [ ]`.
- Keputusan teknis baru → ADR baru di `03 Decisions/` dengan nomor berikutnya; ADR yang diganti diberi `status: superseded` (+ `superseded_by`), jangan dihapus. Tautkan ADR ke note komponen terkait.
- Update field `updated` tiap mengubah note. Nama file tanpa `/ \ : # ^ [ ] |`.
- Vault cukup di-commit lokal, tidak perlu di-push (repo tanpa remote).
