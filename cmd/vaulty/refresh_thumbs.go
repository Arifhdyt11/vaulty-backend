package main

import (
	"context"
	"fmt"

	"vaulty/internal/bootstrap"
	"vaulty/internal/config"
)

// runRefreshThumbs memperbaiki thumbnail item Threads lama: og:image Threads berupa kartu
// tulisan caption, jadi diganti media asli post (gambar, atau frame video). Post yang hanya
// tulisan dikosongkan thumbnail-nya. Cache fetch Threads ikut dibuang supaya simpanan ulang
// link yang sama tidak memakai hasil lama. `--dry-run` hanya mencetak rencana.
func runRefreshThumbs(ctx context.Context, cfg config.Config, args []string) error {
	dry := len(args) > 0 && args[0] == "--dry-run"
	app, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer app.Close()

	rows, err := app.Pool.Query(ctx, `SELECT id, user_id, source_url, thumbnail_key FROM items
		WHERE deleted_at IS NULL AND platform = 'threads' AND source_url IS NOT NULL ORDER BY id`)
	if err != nil {
		return err
	}
	type row struct {
		id, userID int64
		src        string
		old        *string
	}
	var items []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.userID, &r.src, &r.old); err != nil {
			rows.Close()
			return err
		}
		items = append(items, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var withMedia, textOnly, failed int
	for _, r := range items {
		if dry {
			m, err := app.Ingest.ThreadsMediaFor(ctx, r.src)
			if err != nil {
				failed++
				fmt.Printf("item %d: gagal (%v)\n", r.id, err)
				continue
			}
			fmt.Printf("item %d: %d gambar, %d video\n", r.id, len(m.Images), len(m.Videos))
			continue
		}
		key, err := app.Ingest.RefreshThreadsThumbnail(ctx, r.userID, r.id, r.src)
		if err != nil {
			// Gagal ambil media: biarkan thumbnail lama daripada kehilangan gambar.
			failed++
			fmt.Printf("item %d: dilewati (%v)\n", r.id, err)
			continue
		}
		if _, err := app.Pool.Exec(ctx, `UPDATE items SET thumbnail_key = $1, updated_at = now() WHERE id = $2 AND user_id = $3`,
			key, r.id, r.userID); err != nil {
			return fmt.Errorf("simpan item %d: %w", r.id, err)
		}
		if r.old != nil && (key == nil || *r.old != *key) {
			_ = app.Store.Delete(ctx, *r.old)
		}
		if key == nil {
			textOnly++
		} else {
			withMedia++
		}
	}
	if !dry {
		if _, err := app.Pool.Exec(ctx, `DELETE FROM fetch_cache WHERE canonical_url ILIKE '%threads.%'`); err != nil {
			return fmt.Errorf("hapus cache threads: %w", err)
		}
	}
	fmt.Printf("%d item threads: %d bermedia, %d hanya tulisan, %d gagal/dilewati\n", len(items), withMedia, textOnly, failed)
	return nil
}
