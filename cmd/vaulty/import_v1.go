package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"vaulty/internal/bootstrap"
	"vaulty/internal/config"
	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/internal/service"
	"vaulty/pkg/fetch"
)

// v1Note adalah satu baris tabel notes Vaulty v1 yang sudah dimuat ke schema v1.
type v1Note struct {
	ID        int64
	Type      string
	Title     *string
	Body      string
	URL       *string
	Tags      []string
	Project   *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// runImportV1 memindahkan catatan v1 (schema v1.notes) ke items milik user tujuan.
// Impor tidak memakai kuota, idempoten lewat v1.import_map, dan meng-enqueue pipeline
// supaya ringkasan, tag, dan index dibuat ulang dengan Vee versi baru.
func runImportV1(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("import-v1", flag.ContinueOnError)
	email := fs.String("email", "", "email akun Vaulty baru tujuan impor (wajib)")
	dryRun := fs.Bool("dry-run", false, "tampilkan rencana tanpa menulis")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return errors.New("--email wajib diisi")
	}

	app, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer app.Close()

	u, _, err := app.Repos.Users.ByEmail(ctx, strings.ToLower(strings.TrimSpace(*email)))
	if err != nil {
		return fmt.Errorf("akun %s tidak ditemukan; daftar dulu lewat app: %w", *email, err)
	}

	rows, err := app.Pool.Query(ctx, `SELECT n.id, coalesce(n.type,'note'), n.title, coalesce(n.body,''), n.url,
		coalesce(n.tags,'{}'), n.project, coalesce(n.created_at, now()), coalesce(n.updated_at, n.created_at, now())
		FROM v1.notes n LEFT JOIN v1.import_map m ON m.v1_id = n.id
		WHERE n.deleted_at IS NULL AND m.v1_id IS NULL ORDER BY n.id`)
	if err != nil {
		return fmt.Errorf("baca v1.notes (sudah jalankan scripts/import-v1.sh?): %w", err)
	}
	var notes []v1Note
	for rows.Next() {
		var n v1Note
		if err := rows.Scan(&n.ID, &n.Type, &n.Title, &n.Body, &n.URL, &n.Tags, &n.Project, &n.CreatedAt, &n.UpdatedAt); err != nil {
			rows.Close()
			return err
		}
		notes = append(notes, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var imported, skipped int
	for _, n := range notes {
		ni, reason := mapV1Note(u.ID, n)
		if reason != "" {
			skipped++
			fmt.Printf("LEWATI v1#%d (%s) %q: %s\n", n.ID, n.Type, deref(n.Title), reason)
			continue
		}
		if *dryRun {
			fmt.Printf("RENCANA v1#%d %s → kind=%s title=%q tags=%v\n", n.ID, n.Type, ni.Kind, deref(ni.Title), ni.Tags)
			imported++
			continue
		}
		it, err := app.Repos.Items.Create(ctx, ni)
		if err != nil {
			return fmt.Errorf("simpan v1#%d: %w", n.ID, err)
		}
		// Pertahankan tanggal asli supaya urutan Library dan konteks waktu Vee benar.
		if _, err := app.Pool.Exec(ctx, `UPDATE items SET created_at = $3, updated_at = $4 WHERE id = $1 AND user_id = $2`,
			it.ID, u.ID, n.CreatedAt, n.UpdatedAt); err != nil {
			return err
		}
		if _, err := app.Pool.Exec(ctx, `INSERT INTO v1.import_map (v1_id, item_id) VALUES ($1, $2)`, n.ID, it.ID); err != nil {
			return err
		}
		if err := app.Queue.EnqueueIngest(ctx, it.ID, u.ID, it.ContentVersion, "low"); err != nil {
			slog.WarnContext(ctx, "enqueue gagal; safety net worker akan menyusul", "item_id", it.ID, "err", err)
		}
		imported++
	}
	verb := "diimpor"
	if *dryRun {
		verb = "akan diimpor"
	}
	fmt.Printf("\nSelesai: %d %s, %d dilewati, ke akun %s.\n", imported, verb, skipped, u.Email)
	if !*dryRun && imported > 0 {
		fmt.Println("Pastikan worker jalan (make worker) supaya ringkasan, tag, dan index dibuat.")
	}
	return nil
}

// mapV1Note memetakan satu catatan v1 ke item baru. Alasan non-kosong = dilewati.
func mapV1Note(userID int64, n v1Note) (repository.NewItem, string) {
	tags := append([]string{}, n.Tags...)
	if n.Project != nil && strings.TrimSpace(*n.Project) != "" {
		tags = append(tags, *n.Project)
	}
	tags = append(tags, "impor-v1")
	pending := model.Stage{Status: "pending"}
	skipped := model.Stage{Status: "skipped"}
	ni := repository.NewItem{UserID: userID, Title: trimPtr(n.Title)}

	body := strings.TrimSpace(n.Body)
	url := ""
	if n.URL != nil {
		url = strings.TrimSpace(*n.URL)
	}
	if warn := service.DetectSecret(body, deref(n.Title)); warn != "" {
		return ni, "terdeteksi berisi credential (ADR-012); salin manual ke password manager bila perlu"
	}

	switch {
	case url != "" && (n.Type == "link" || body == ""):
		canon, platform, err := fetch.Canonicalize(url)
		if err != nil {
			return ni, "url tidak valid: " + url
		}
		ni.Kind = "url"
		ni.SourceURL, ni.CanonicalURL, ni.Platform = &url, &canon, &platform
		ni.UserNote = truncate(body, 2000)
		ni.Ingest = model.Ingest{Fetch: pending, Transcribe: pending, Understand: pending, Index: pending}
	default:
		if body == "" && ni.Title == nil {
			return ni, "kosong"
		}
		ni.Kind = "text"
		ni.Body = body
		if url != "" {
			// Catatan bertaut: URL tetap disimpan sebagai sumber tanpa di-fetch ulang.
			ni.SourceURL = &url
		}
		if n.Type == "command" {
			tags = append(tags, "command")
		}
		ni.Ingest = model.Ingest{Fetch: skipped, Transcribe: skipped, Understand: pending, Index: pending}
	}
	ni.Tags = service.NormalizeTags(tags)
	return ni, ""
}

func trimPtr(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
