---
paths:
  - "**/*_test.go"
---
# Testing

- Hanya package `testing` stdlib: tanpa testify, gomock, atau library assertion lain.
- File test di package yang sama (`package service`) agar bisa menguji fungsi unexported.
- Utamakan table-driven test: `cases := []struct{...}{...}` atau `map[input]want`, lalu `t.Errorf("Fungsi(%q) = %q; want %q", ...)`. `t.Fatal` hanya bila langkah berikutnya tidak masuk akal.
- Unit test tidak boleh butuh Postgres, Redis, Ollama, atau jaringan. Ketergantungan eksternal diganti fake yang memenuhi interface (mis. `IndexEnqueuer`), atau `httptest.Server` untuk provider HTTP.
- File sementara pakai `t.TempDir()`; env pakai `t.Setenv`.
- Wajib ada kasus negatif untuk aturan keamanan: tipe credential ditolak, input invalid, data user lain tidak terlihat.
- Jalankan `go test ./<package>/...` saat iterasi, `make test` sebelum selesai.
