package repository

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	"vaulty-api/internal/repository/queries"
)

type AuditEntry struct {
	UserID   int64
	Action   string // mis. note.create, auth.login
	Entity   string
	EntityID int64
	Metadata map[string]any
	IP       string
}

type AuditRepository struct{ q *queries.Queries }

func NewAuditRepository(q *queries.Queries) *AuditRepository { return &AuditRepository{q: q} }

func (r *AuditRepository) Insert(ctx context.Context, e AuditEntry) error {
	meta := []byte("{}")
	if e.Metadata != nil {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			return err
		}
		meta = b
	}
	return r.q.InsertAuditLog(ctx, queries.InsertAuditLogParams{
		UserID:   pgtype.Int8{Int64: e.UserID, Valid: e.UserID != 0},
		Action:   e.Action,
		Entity:   e.Entity,
		EntityID: pgtype.Int8{Int64: e.EntityID, Valid: e.EntityID != 0},
		Metadata: meta,
		Ip:       e.IP,
	})
}
