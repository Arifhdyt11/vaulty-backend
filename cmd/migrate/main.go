// Command migrate menjalankan migration database: go run ./cmd/migrate [up|down|status|redo|reset|version]
package main

import (
	"context"
	"log/slog"
	"os"

	"vaulty-api/internal/bootstrap"
	"vaulty-api/internal/database"
)

func main() {
	cfg := bootstrap.Config()
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if err := database.Migrate(context.Background(), cfg.DatabaseURL, command, os.Args[min(2, len(os.Args)):]...); err != nil {
		slog.Error("migration gagal", "command", command, "err", err)
		os.Exit(1)
	}
}
