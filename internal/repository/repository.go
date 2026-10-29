// Package repository: akses data dengan pgx. Semua query data user WAJIB memfilter user_id (ADR-007).
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"vaulty/internal/model"
)

// DB adalah subset pgxpool.Pool / pgx.Tx yang dipakai repository, supaya bisa dipakai di transaksi.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Repos struct {
	pool     *pgxpool.Pool
	Users    *UserRepo
	Sessions *SessionRepo
	Devices  *DeviceRepo
	Items    *ItemRepo
	Chunks   *ChunkRepo
	Chats    *ChatRepo
	Reminder *ReminderRepo
	Billing  *BillingRepo
	Audit    *AuditRepo
	Cache    *FetchCacheRepo
	Briefing *BriefingRepo
	Admin    *AdminRepo
	Settings *SettingsRepo
	Codes    *EmailCodeRepo
}

func New(pool *pgxpool.Pool) *Repos {
	return &Repos{
		pool:     pool,
		Users:    &UserRepo{db: pool},
		Sessions: &SessionRepo{db: pool},
		Devices:  &DeviceRepo{db: pool},
		Items:    &ItemRepo{db: pool},
		Chunks:   &ChunkRepo{db: pool},
		Chats:    &ChatRepo{db: pool},
		Reminder: &ReminderRepo{db: pool},
		Billing:  &BillingRepo{db: pool},
		Audit:    &AuditRepo{db: pool},
		Cache:    &FetchCacheRepo{db: pool},
		Briefing: &BriefingRepo{db: pool},
		Admin:    &AdminRepo{db: pool},
		Settings: &SettingsRepo{db: pool},
		Codes:    &EmailCodeRepo{db: pool},
	}
}

func (r *Repos) Pool() *pgxpool.Pool { return r.pool }

// Tx menjalankan fn dalam transaksi dengan Repos yang terikat ke tx tersebut.
func (r *Repos) Tx(ctx context.Context, fn func(tx *Repos) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tr := &Repos{
		pool:     r.pool,
		Users:    &UserRepo{db: tx},
		Sessions: &SessionRepo{db: tx},
		Devices:  &DeviceRepo{db: tx},
		Items:    &ItemRepo{db: tx},
		Chunks:   &ChunkRepo{db: tx},
		Chats:    &ChatRepo{db: tx},
		Reminder: &ReminderRepo{db: tx},
		Billing:  &BillingRepo{db: tx},
		Audit:    &AuditRepo{db: tx},
		Cache:    &FetchCacheRepo{db: tx},
		Briefing: &BriefingRepo{db: tx},
		Admin:    &AdminRepo{db: tx},
		Settings: &SettingsRepo{db: tx},
		Codes:    &EmailCodeRepo{db: tx},
	}
	if err := fn(tr); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return model.ErrConflict
	}
	return err
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
