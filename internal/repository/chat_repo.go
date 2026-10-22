package repository

import (
	"context"
	"encoding/json"
	"time"

	"vaulty/internal/model"
)

type ChatRepo struct{ db DB }

func (r *ChatRepo) CreateConversation(ctx context.Context, userID int64, title string) (model.Conversation, error) {
	var c model.Conversation
	err := r.db.QueryRow(ctx, `INSERT INTO conversations (user_id, title) VALUES ($1, $2) RETURNING id, title, created_at, updated_at`, userID, title).
		Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (r *ChatRepo) Conversation(ctx context.Context, userID, id int64) (model.Conversation, error) {
	var c model.Conversation
	err := r.db.QueryRow(ctx, `SELECT id, title, created_at, updated_at FROM conversations WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, userID).
		Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	return c, mapErr(err)
}

func (r *ChatRepo) ListConversations(ctx context.Context, userID int64, cursor int64, limit int) ([]model.Conversation, *int64, error) {
	rows, err := r.db.Query(ctx, `SELECT id, title, created_at, updated_at FROM conversations
		WHERE user_id = $1 AND deleted_at IS NULL AND ($2 = 0 OR id < $2) ORDER BY id DESC LIMIT $3`, userID, cursor, limit+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []model.Conversation
	for rows.Next() {
		var c model.Conversation
		if err := rows.Scan(&c.ID, &c.Title, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, nil, err
		}
		out = append(out, c)
	}
	var next *int64
	if len(out) > limit {
		out = out[:limit]
		id := out[len(out)-1].ID
		next = &id
	}
	return out, next, rows.Err()
}

func (r *ChatRepo) DeleteConversation(ctx context.Context, userID, id int64) error {
	t, err := r.db.Exec(ctx, `UPDATE conversations SET deleted_at = now() WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}

// PruneConversations: riwayat Free hanya disimpan beberapa hari (ADR-011).
func (r *ChatRepo) PruneConversations(ctx context.Context, plan string, olderThan time.Duration) (int64, error) {
	t, err := r.db.Exec(ctx, `UPDATE conversations c SET deleted_at = now() FROM users u
		WHERE u.id = c.user_id AND u.plan = $1 AND c.deleted_at IS NULL AND c.updated_at < now() - $2::interval`, plan, olderThan.String())
	return t.RowsAffected(), err
}

func (r *ChatRepo) AddMessage(ctx context.Context, userID int64, m model.Message) (model.Message, error) {
	if m.Citations == nil {
		m.Citations = []model.Citation{}
	}
	cits, _ := json.Marshal(stripCards(m.Citations))
	err := r.db.QueryRow(ctx, `INSERT INTO messages (conversation_id, user_id, role, mode, content, citations, from_notes)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at`, m.ConversationID, userID, m.Role, m.Mode, m.Content, cits, m.FromNotes).
		Scan(&m.ID, &m.CreatedAt)
	if err != nil {
		return m, err
	}
	_, _ = r.db.Exec(ctx, `UPDATE conversations SET updated_at = now(), title = CASE WHEN title = '' AND $3 = 'user' THEN left($4, 80) ELSE title END WHERE id = $1 AND user_id = $2`,
		m.ConversationID, userID, m.Role, m.Content)
	return m, nil
}

func stripCards(c []model.Citation) []model.Citation {
	out := make([]model.Citation, len(c))
	for i, x := range c {
		x.Item = nil
		out[i] = x
	}
	return out
}

func (r *ChatRepo) Messages(ctx context.Context, userID, convID int64, limit int) ([]model.Message, error) {
	rows, err := r.db.Query(ctx, `SELECT id, conversation_id, role, mode, content, citations, from_notes, created_at FROM messages
		WHERE conversation_id = $1 AND user_id = $2 ORDER BY id DESC LIMIT $3`, convID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Message
	for rows.Next() {
		var m model.Message
		var cits []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Mode, &m.Content, &cits, &m.FromNotes, &m.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(cits, &m.Citations)
		if m.Citations == nil {
			m.Citations = []model.Citation{}
		}
		out = append(out, m)
	}
	// urutkan kronologis
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}
