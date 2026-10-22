#!/bin/bash
# Impor catatan Vaulty v1 (dump tabel notes dari sipantas) ke akun Vaulty baru di database lokal.
#
# 1. Ambil dump (jalankan sendiri, butuh akses SSH):
#    ssh sipantas "docker exec shared-postgres pg_dump -U vaulty -d vaulty -t notes --data-only --column-inserts" > notes-v1.sql
# 2. Daftar akun di app, lalu:
#    scripts/import-v1.sh path/ke/notes-v1.sql email@anda.com           # pratinjau (dry run)
#    scripts/import-v1.sh path/ke/notes-v1.sql email@anda.com --apply   # impor sungguhan
set -euo pipefail
DUMP=${1:?path dump wajib}
EMAIL=${2:?email akun tujuan wajib}
MODE=${3:-}
cd "$(dirname "$0")/.."
PG="docker exec -i vaulty2-postgres psql -U vaulty -d vaulty -v ON_ERROR_STOP=1 -q"

$PG < db/import/v1_notes_schema.sql
# Muat ulang staging dari dump: arahkan INSERT ke v1.notes, buang perintah sesi pg_dump.
$PG -c "TRUNCATE v1.notes"
# Seluruh dump dimuat utuh (isi catatan bisa multi-baris); hanya target INSERT dialihkan ke v1.notes,
# dan setval sequence dibuang karena sequence notes lama tidak ada di database baru.
sed -E 's/^INSERT INTO (public\.)?notes /INSERT INTO v1.notes /' "$DUMP" | grep -v 'pg_catalog.setval' | $PG
echo "Staging: $($PG -tAc 'select count(*) from v1.notes where deleted_at is null') catatan aktif di v1.notes"

if [ "$MODE" = "--apply" ]; then
  go run ./cmd/vaulty import-v1 --email "$EMAIL"
else
  go run ./cmd/vaulty import-v1 --email "$EMAIL" --dry-run
  echo "Ini pratinjau. Jalankan ulang dengan --apply untuk mengimpor."
fi
