package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"vaulty/internal/model"
)

// BriefingRepo: Siaran Vee (ADR-016). Semua query ber-user_id kecuali Prune (housekeeping global).
type BriefingRepo struct{ db DB }

// BriefingCandidate adalah data ringan item untuk pemilihan bab.
type BriefingCandidate struct {
	ID         int64
	CreatedAt  time.Time
	HasSummary bool
}

type NewChapter struct {
	Seq    int
	ItemID int64
	Label  string
	Title  string
	Quote  string
}

const briefingCols = `id, user_id, date::text, status, greeting, created_at, updated_at`

func scanBriefing(row pgx.Row) (model.Briefing, error) {
	var b model.Briefing
	err := row.Scan(&b.ID, &b.UserID, &b.Date, &b.Status, &b.Greeting, &b.CreatedAt, &b.GeneratedAt)
	return b, mapErr(err)
}

// ByDate membaca siaran user pada tanggal (YYYY-MM-DD) beserta babnya.
func (r *BriefingRepo) ByDate(ctx context.Context, userID int64, date string) (model.Briefing, error) {
	b, err := scanBriefing(r.db.QueryRow(ctx, `SELECT `+briefingCols+` FROM briefings WHERE user_id = $1 AND date = $2::date`, userID, date))
	if err != nil {
		return b, err
	}
	b.Chapters, err = r.chapters(ctx, userID, b.ID)
	return b, err
}

func (r *BriefingRepo) ByID(ctx context.Context, userID, id int64) (model.Briefing, error) {
	b, err := scanBriefing(r.db.QueryRow(ctx, `SELECT `+briefingCols+` FROM briefings WHERE user_id = $1 AND id = $2`, userID, id))
	if err != nil {
		return b, err
	}
	b.Chapters, err = r.chapters(ctx, userID, b.ID)
	return b, err
}

func (r *BriefingRepo) chapters(ctx context.Context, userID, briefingID int64) ([]model.BriefingChapter, error) {
	rows, err := r.db.Query(ctx, `SELECT id, seq, item_id, label, title, quote, script, audio_key, duration_sec, status, coalesce(error, '')
		FROM briefing_chapters WHERE user_id = $1 AND briefing_id = $2 ORDER BY seq`, userID, briefingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.BriefingChapter{}
	for rows.Next() {
		var c model.BriefingChapter
		if err := rows.Scan(&c.ID, &c.Seq, &c.ItemID, &c.Label, &c.Title, &c.Quote, &c.Script, &c.AudioKey, &c.DurationSec, &c.Status, &c.Error); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Insert membuat baris siaran. created=false bila siaran tanggal itu sudah ada (GET bersamaan).
func (r *BriefingRepo) Insert(ctx context.Context, userID int64, date, status, greeting string) (int64, bool, error) {
	var id int64
	err := r.db.QueryRow(ctx, `INSERT INTO briefings (user_id, date, status, greeting) VALUES ($1, $2::date, $3, $4)
		ON CONFLICT (user_id, date) DO NOTHING RETURNING id`, userID, date, status, greeting).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("simpan siaran: %w", err)
	}
	return id, true, nil
}

func (r *BriefingRepo) InsertChapter(ctx context.Context, userID, briefingID int64, c NewChapter) error {
	_, err := r.db.Exec(ctx, `INSERT INTO briefing_chapters (briefing_id, user_id, seq, item_id, label, title, quote) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		briefingID, userID, c.Seq, c.ItemID, c.Label, c.Title, c.Quote)
	if err != nil {
		return fmt.Errorf("simpan bab: %w", err)
	}
	return nil
}

// DeleteByDate menghapus siaran tanggal itu (bab ikut terhapus) dan mengembalikan key audio-nya.
func (r *BriefingRepo) DeleteByDate(ctx context.Context, userID int64, date string) ([]string, error) {
	keys, err := r.keys(ctx, `SELECT c.audio_key FROM briefing_chapters c JOIN briefings b ON b.id = c.briefing_id
		WHERE c.user_id = $1 AND b.user_id = $1 AND b.date = $2::date AND c.audio_key IS NOT NULL`, userID, date)
	if err != nil {
		return nil, err
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM briefings WHERE user_id = $1 AND date = $2::date`, userID, date); err != nil {
		return nil, fmt.Errorf("hapus siaran: %w", err)
	}
	return keys, nil
}

// Candidates: item siap milik user, terbaru dulu (dibatasi supaya tetap ringan).
func (r *BriefingRepo) Candidates(ctx context.Context, userID int64, limit int) ([]BriefingCandidate, error) {
	rows, err := r.db.Query(ctx, `SELECT id, created_at, coalesce(summary, '') <> '' FROM items
		WHERE user_id = $1 AND deleted_at IS NULL AND status = 'ready' ORDER BY created_at DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BriefingCandidate
	for rows.Next() {
		var c BriefingCandidate
		if err := rows.Scan(&c.ID, &c.CreatedAt, &c.HasSummary); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ShownHistory: item_id → tanggal terakhir tampil di siaran (YYYY-MM-DD).
func (r *BriefingRepo) ShownHistory(ctx context.Context, userID int64) (map[int64]string, error) {
	rows, err := r.db.Query(ctx, `SELECT c.item_id, max(b.date)::text FROM briefing_chapters c JOIN briefings b ON b.id = c.briefing_id
		WHERE c.user_id = $1 AND b.user_id = $1 GROUP BY c.item_id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var d string
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, rows.Err()
}

func (r *BriefingRepo) SetChapterScript(ctx context.Context, userID, chapterID int64, script string) error {
	_, err := r.db.Exec(ctx, `UPDATE briefing_chapters SET script = $3 WHERE user_id = $1 AND id = $2 AND status = 'pending'`, userID, chapterID, script)
	return err
}

// SetChapterAudio menandai bab siap. ok=false bila bab sudah tidak ada (siaran disusun ulang).
func (r *BriefingRepo) SetChapterAudio(ctx context.Context, userID, chapterID int64, key string, durationSec int) (bool, error) {
	tag, err := r.db.Exec(ctx, `UPDATE briefing_chapters SET audio_key = $3, duration_sec = $4, status = 'ready', error = NULL
		WHERE user_id = $1 AND id = $2`, userID, chapterID, key, durationSec)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (r *BriefingRepo) SetChapterFailed(ctx context.Context, userID, chapterID int64, msg string) error {
	_, err := r.db.Exec(ctx, `UPDATE briefing_chapters SET status = 'failed', error = $3 WHERE user_id = $1 AND id = $2`, userID, chapterID, msg)
	return err
}

func (r *BriefingRepo) SetStatus(ctx context.Context, userID, id int64, status string) error {
	_, err := r.db.Exec(ctx, `UPDATE briefings SET status = $3, updated_at = now() WHERE user_id = $1 AND id = $2`, userID, id, status)
	return err
}

// AudioKeys: semua key audio siaran milik user (untuk hapus akun).
func (r *BriefingRepo) AudioKeys(ctx context.Context, userID int64) ([]string, error) {
	return r.keys(ctx, `SELECT audio_key FROM briefing_chapters WHERE user_id = $1 AND audio_key IS NOT NULL`, userID)
}

// Prune menghapus siaran lebih tua dari tanggal before (semua user, housekeeping) dan mengembalikan key audio-nya.
func (r *BriefingRepo) Prune(ctx context.Context, before string) ([]string, error) {
	keys, err := r.keys(ctx, `SELECT c.audio_key FROM briefing_chapters c JOIN briefings b ON b.id = c.briefing_id
		WHERE b.date < $1::date AND c.audio_key IS NOT NULL`, before)
	if err != nil {
		return nil, err
	}
	if _, err := r.db.Exec(ctx, `DELETE FROM briefings WHERE date < $1::date`, before); err != nil {
		return nil, fmt.Errorf("pangkas siaran: %w", err)
	}
	return keys, nil
}

func (r *BriefingRepo) keys(ctx context.Context, sql string, args ...any) ([]string, error) {
	rows, err := r.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k != "" {
			out = append(out, k)
		}
	}
	return out, rows.Err()
}
