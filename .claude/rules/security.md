# Keamanan

- Token hanya disimpan sebagai hash SHA-256; access 1 jam, refresh 90 hari dirotasi (ADR-008).
- Tidak ada penyimpanan credential: `detectSecret` menolak pola API key/password (ADR-012). Jangan melonggarkan tanpa diskusi.
- Fetch URL melewati guard anti-SSRF di `pkg/fetch`. Jangan bypass dengan `http.Get` langsung ke URL dari user.
- Webhook billing diverifikasi `REVENUECAT_WEBHOOK_SECRET`; entitlement dihitung di backend (ADR-011).
- Error 500 tidak membocorkan detail internal; detail hanya di log.
- Jangan baca `.env`, `*.pem`, `*.p8`, service-account JSON, atau isi `storage/`.
