# Keamanan (selalu berlaku)

Vaulty menyimpan catatan pribadi; perlakukan semua isi note, file upload, dan token sebagai data sensitif.

- Otorisasi di server: user selalu dari `middleware.CurrentUser(ctx)`, dan setiap akses data di-scope `user_id` di repository (ADR-007). Akses milik user lain → `model.ErrNotFound` (404), bukan 403, supaya tidak membocorkan keberadaan data.
- Access token opaque disimpan sebagai hash di DB (ADR-018); jangan log, kembalikan ulang, atau simpan token mentah.
- Password hanya lewat `pkg/password` (argon2id). Jangan pernah log password, token, `id_token`, header `Authorization`, atau isi note/file.
- Registrasi tertutup: akun baru hanya untuk `ALLOWED_EMAILS` (ADR-019). Jangan melonggarkan tanpa ADR.
- Tipe credential ditolak (`model.ErrBlockedType`, ADR-016). Deteksi pola secret di body hanya menghasilkan `warnings`, tidak memblokir.
- File upload: batasi ukuran (`MAX_UPLOAD_MB`), jangan percaya nama file/MIME dari client untuk path; key storage dibuat server. Download wajib cek kepemilikan note.
- Error 500 tidak boleh membocorkan detail internal ke client (pakai `firstLine`/pesan generik); detail cukup di log.
- Secret hanya dari environment (`internal/config`); `.env` tidak dibaca Claude dan tidak di-commit.
