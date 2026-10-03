// Package db menyimpan migration SQL agar ikut ter-embed di binary.
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
