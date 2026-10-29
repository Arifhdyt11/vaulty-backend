// Package handler: lapisan HTTP. errors.go memetakan error domain ke HTTP (dipakai semua versi).
package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"vaulty/internal/model"
	"vaulty/pkg/ai"
	"vaulty/pkg/storage"
)

// QuotaProblem: 402 dengan detail kuota (kontrak API).
type QuotaProblem struct {
	Status   int       `json:"status"`
	Title    string    `json:"title"`
	Detail   string    `json:"detail"`
	Metric   string    `json:"metric"`
	Limit    int       `json:"limit"`
	Used     int       `json:"used"`
	ResetsAt time.Time `json:"resets_at"`
	Upgrade  bool      `json:"upgrade"`
}

func (q *QuotaProblem) Error() string  { return q.Detail }
func (q *QuotaProblem) GetStatus() int { return q.Status }

// E memetakan error service ke huma.StatusError. Pesan Bahasa Indonesia, tanpa detail internal untuk 500.
func E(err error) error {
	if err == nil {
		return nil
	}
	var qe *model.QuotaError
	if errors.As(err, &qe) {
		return &QuotaProblem{Status: http.StatusPaymentRequired, Title: "Kuota habis", Detail: qe.Error(), Metric: qe.Metric, Limit: qe.Limit, Used: qe.Used, ResetsAt: qe.ResetsAt, Upgrade: qe.Upgrade}
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	switch {
	case errors.Is(err, model.ErrNotFound), errors.Is(err, storage.ErrNotFound):
		return huma.Error404NotFound("tidak ditemukan")
	case errors.Is(err, model.ErrUnauthorized):
		return huma.Error401Unauthorized(model.ErrUnauthorized.Error())
	case errors.Is(err, model.ErrForbidden):
		return huma.Error403Forbidden(model.ErrForbidden.Error())
	case errors.Is(err, model.ErrInvalidInput):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, model.ErrEmailTaken), errors.Is(err, model.ErrConflict):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, model.ErrBadCredentials):
		return huma.Error401Unauthorized(model.ErrBadCredentials.Error())
	case errors.Is(err, model.ErrSuspended):
		return huma.Error403Forbidden(model.ErrSuspended.Error())
	case errors.Is(err, model.ErrTooMany):
		return huma.Error429TooManyRequests(model.ErrTooMany.Error())
	case errors.Is(err, model.ErrMailOff):
		return huma.Error503ServiceUnavailable(model.ErrMailOff.Error())
	case errors.Is(err, model.ErrRegistrationOff):
		return huma.Error403Forbidden(model.ErrRegistrationOff.Error())
	case errors.Is(err, model.ErrTooLarge):
		return huma.NewError(http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, model.ErrUnsupportedFile):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, model.ErrAIDisabled), errors.Is(err, ai.ErrDisabled):
		return huma.Error503ServiceUnavailable("fitur AI belum dikonfigurasi di server")
	}
	slog.Error("internal error", "err", err)
	return huma.Error500InternalServerError("terjadi kesalahan di server")
}
