// Package repository adalah layer akses data. Membungkus query sqlc (subpackage queries)
// dan memetakan hasilnya ke model. Setiap method untuk data user wajib menerima userID (ADR-007).
package repository

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"vaulty-api/internal/model"
)

// notFound mengubah pgx.ErrNoRows menjadi model.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrNotFound
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// text: string kosong disimpan sebagai NULL.
func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func nullable(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
