package v1

import (
	"github.com/danielgtaylor/huma/v2"

	"vaulty-api/internal/service"
)

// Services adalah dependensi yang dibutuhkan handler v1.
type Services struct {
	Auth           *service.AuthService
	Audit          *service.AuditService
	Note           *service.NoteService
	Search         *service.SearchService
	MaxUploadBytes int64
}

// Register mendaftarkan semua endpoint v1 ke api (yang sudah ber-prefix /api/v1).
func Register(api huma.API, s Services) {
	NewAuthHandler(s.Auth, s.Audit).Register(api)
	NewNoteHandler(s.Note, s.Audit, s.MaxUploadBytes).Register(api)
	NewSearchHandler(s.Search).Register(api)
}
