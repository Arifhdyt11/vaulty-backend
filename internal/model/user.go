// Package model berisi struktur data domain Vaulty yang dipakai lintas layer
// (handler, service, repository). Tidak bergantung pada database maupun HTTP.
package model

import "time"

const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	Role      string    `json:"role" enum:"user,admin"`
	HasGoogle bool      `json:"has_google"`
	CreatedAt time.Time `json:"created_at"`
}

// Session adalah hasil login: access token untuk header Authorization: Bearer (ADR-015, ADR-018).
type Session struct {
	User        User      `json:"user"`
	AccessToken string    `json:"access_token" doc:"Kirim sebagai Authorization: Bearer <token>"`
	ExpiresAt   time.Time `json:"expires_at"`
}
