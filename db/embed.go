// Package db meng-embed file migrasi supaya binary bisa menjalankan migrasi sendiri.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
