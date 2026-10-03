package model

import "errors"

// Error domain. Handler memetakannya ke status HTTP (lihat handler/errors.go).
var (
	ErrNotFound = errors.New("data tidak ditemukan")

	ErrEmailTaken         = errors.New("email sudah terdaftar")
	ErrInvalidCredentials = errors.New("email atau password salah")
	ErrGoogleOnly         = errors.New("akun ini terdaftar lewat Google, silakan login dengan Google")
	ErrInvalidEmail       = errors.New("format email tidak valid")
	ErrWeakPassword       = errors.New("password minimal 8 karakter")
	ErrEmailUnverified    = errors.New("email Google belum terverifikasi")
	ErrRegistrationClosed = errors.New("registrasi belum dibuka untuk email ini")
	ErrInvalidGoogleToken = errors.New("id_token Google tidak valid")
	ErrGoogleDisabled     = errors.New("login Google belum dikonfigurasi (GOOGLE_CLIENT_IDS)")

	ErrInvalidType = errors.New("type harus lowercase, diawali huruf, maksimal 32 karakter (a-z, 0-9, -, _)")
	ErrBlockedType = errors.New("Vaulty tidak menyimpan credential; gunakan password manager (Bitwarden/Vaultwarden)")
	ErrEmptyNote   = errors.New("isi minimal salah satu: title, body, atau url")

	ErrEmptyReminder   = errors.New("isi reminder wajib diisi (maksimal 500 karakter)")
	ErrReminderPast    = errors.New("waktu reminder sudah lewat")
	ErrInvalidRepeat   = errors.New("repeat harus kosong, daily, weekly, monthly, atau yearly")
	ErrInvalidLead     = errors.New("pengingat awal harus 1 menit sampai 365 hari, maksimal 5")
	ErrReminderTooFar  = errors.New("waktu reminder maksimal 10 tahun dari sekarang")
	ErrReminderInvalid = errors.New("reminder tidak bisa diubah pada status ini")
)
