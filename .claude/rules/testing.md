# Testing

- Unit test tanpa DB untuk logika murni (chunking, citation parse, reminder scheduling, kuota period, parser). Taruh di package yang sama, nama fungsi deskriptif Bahasa Indonesia.
- Paket `pkg/*` dites dengan `httptest`, tanpa jaringan sungguhan (tes live di-skip kecuali `VAULTY_LIVE=1`).
- Repository belum punya tes integrasi; bila menambah, pakai `DATABASE_URL_TEST` dan skip jika kosong.
- Sebelum commit: `make verify` (fmt, vet, test).
