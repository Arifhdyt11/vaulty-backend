# Aturan vault Obsidian (`../vaulty-vault/`)

- Boleh create/update/delete note jika diminta. Jangan sentuh `.obsidian/`.
- Pakai template di `Templates/` (ADR, Architecture Note, Dev Log); ganti `{{title}}`/`{{date}}` secara manual.
- Format: YAML frontmatter (`title`, `tags`, `type`, `status`, `created`/`updated` atau `date`, `related`), satu H1, wikilink `[[Nama Note]]` (nama file tanpa path/ekstensi, harus unik), callout `> [!note]`, checklist `- [ ]`.
- Keputusan teknis baru → ADR baru di `03 Decisions/` dengan nomor berikutnya; ADR yang diganti diberi `status: superseded` (+ `superseded_by`), jangan dihapus. Tambahkan link ADR ke note komponen terkait.
- Update field `updated` di frontmatter setiap mengubah note.
- Nama file tidak boleh mengandung `/ \ : # ^ [ ] |`.
