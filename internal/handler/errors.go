// Package handler adalah layer HTTP. Handler per versi API ada di subpackage (v1, v2, ...),
// karena bentuk request/response adalah kontrak versi. Package ini berisi bagian yang dipakai
// bersama semua versi. Handler tidak berisi logika bisnis; itu tugas service.
package handler

import (
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"vaulty-api/internal/model"
)

// ToHTTPError memetakan error domain (model.Err*) ke status HTTP. Error lain menjadi 500.
func ToHTTPError(err error) error {
	switch {
	case errors.Is(err, model.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, model.ErrInvalidCredentials), errors.Is(err, model.ErrGoogleOnly),
		errors.Is(err, model.ErrInvalidGoogleToken):
		return huma.Error401Unauthorized(firstLine(err))
	case errors.Is(err, model.ErrRegistrationClosed), errors.Is(err, model.ErrEmailUnverified):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, model.ErrEmailTaken):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, model.ErrInvalidEmail), errors.Is(err, model.ErrWeakPassword),
		errors.Is(err, model.ErrInvalidType), errors.Is(err, model.ErrBlockedType), errors.Is(err, model.ErrEmptyNote):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, model.ErrGoogleDisabled):
		return huma.Error503ServiceUnavailable(err.Error())
	default:
		return err
	}
}

// firstLine menyembunyikan detail teknis dari error gabungan (errors.Join) dari client.
func firstLine(err error) string {
	msg := err.Error()
	for i, c := range msg {
		if c == '\n' {
			return msg[:i]
		}
	}
	return msg
}
