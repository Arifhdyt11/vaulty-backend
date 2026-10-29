package model

import (
	"errors"
	"fmt"
	"time"
)

// Error domain; dipetakan ke HTTP di handler.ToHTTPError. Pesan Bahasa Indonesia tampil ke client.
var (
	ErrNotFound        = errors.New("tidak ditemukan")
	ErrUnauthorized    = errors.New("token tidak valid atau kedaluwarsa")
	ErrForbidden       = errors.New("tidak diizinkan")
	ErrInvalidInput    = errors.New("input tidak valid")
	ErrEmailTaken      = errors.New("email sudah terdaftar")
	ErrBadCredentials  = errors.New("email atau password salah")
	ErrRegistrationOff = errors.New("registrasi belum dibuka untuk email ini")
	ErrTooLarge        = errors.New("file terlalu besar")
	ErrUnsupportedFile = errors.New("tipe file tidak didukung")
	ErrAIDisabled      = errors.New("fitur AI belum dikonfigurasi")
	ErrConflict        = errors.New("konflik")
	ErrSuspended       = errors.New("akun ini dinonaktifkan admin")
	ErrTooMany         = errors.New("terlalu sering, coba lagi sebentar lagi")
	ErrMailOff         = errors.New("pengiriman email belum dikonfigurasi di server")
)

// QuotaError → 402 dengan detail kuota supaya app bisa menampilkan paywall.
type QuotaError struct {
	Metric   string
	Limit    int
	Used     int
	ResetsAt time.Time
	Upgrade  bool // false bila Pro ditutup admin atau user sudah Pro (ADR-021)
}

func (e *QuotaError) Error() string {
	return fmt.Sprintf("kuota %s habis (%d/%d)", e.Metric, e.Used, e.Limit)
}

// Invalid membungkus ErrInvalidInput dengan pesan spesifik.
func Invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, a...))
}
