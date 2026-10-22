package main

import (
	"context"
	"fmt"

	"vaulty/internal/bootstrap"
	"vaulty/internal/config"
)

// runRequeue meng-enqueue ulang semua item berstatus queued/processing tanpa menunggu
// safety net worker (15 menit). Dipakai setelah reset manual atau worker mati lama.
func runRequeue(ctx context.Context, cfg config.Config) error {
	app, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer app.Close()
	rows, err := app.Pool.Query(ctx, `SELECT i.id, i.user_id, i.content_version, u.plan FROM items i JOIN users u ON u.id = i.user_id
		WHERE i.deleted_at IS NULL AND i.status IN ('queued','processing') ORDER BY i.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, userID int64
		var version int32
		var plan string
		if err := rows.Scan(&id, &userID, &version, &plan); err != nil {
			return err
		}
		queue := "default"
		if plan == "pro" {
			queue = "critical"
		}
		if err := app.Queue.EnqueueIngest(ctx, id, userID, version, queue); err != nil {
			return fmt.Errorf("enqueue item %d: %w", id, err)
		}
		n++
	}
	fmt.Printf("%d item di-enqueue ulang\n", n)
	return rows.Err()
}
