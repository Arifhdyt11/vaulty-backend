package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository/queries"
)

type SessionRepository struct{ q *queries.Queries }

func NewSessionRepository(q *queries.Queries) *SessionRepository { return &SessionRepository{q: q} }

func (r *SessionRepository) Create(ctx context.Context, userID int64, tokenHash, userAgent, ip string, expiresAt time.Time) error {
	return r.q.CreateSession(ctx, queries.CreateSessionParams{
		UserID:    userID,
		TokenHash: tokenHash,
		UserAgent: userAgent,
		Ip:        ip,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
}

// FindUserByTokenHash mengembalikan pemilik session yang belum kedaluwarsa.
func (r *SessionRepository) FindUserByTokenHash(ctx context.Context, tokenHash string) (model.User, error) {
	row, err := r.q.GetUserBySessionToken(ctx, tokenHash)
	return toUser(row), notFound(err)
}

func (r *SessionRepository) Delete(ctx context.Context, tokenHash string) error {
	return r.q.DeleteSession(ctx, tokenHash)
}

func (r *SessionRepository) DeleteExpired(ctx context.Context) (int64, error) {
	return r.q.DeleteExpiredSessions(ctx)
}
