// Package service berisi logika bisnis Vaulty. Dipanggil handler (HTTP) dan worker (asynq),
// dan mengakses data hanya lewat repository.
package service

import (
	"context"
	"log/slog"

	"vaulty-api/internal/repository"
)

// AuditService mencatat aksi penting (NF4, ADR-006).
type AuditService struct{ repo *repository.AuditRepository }

func NewAuditService(repo *repository.AuditRepository) *AuditService {
	return &AuditService{repo: repo}
}

// Log tidak pernah menggagalkan request; kegagalan hanya dicatat di log aplikasi.
func (s *AuditService) Log(ctx context.Context, e repository.AuditEntry) {
	if err := s.repo.Insert(ctx, e); err != nil {
		slog.ErrorContext(ctx, "audit log gagal", "action", e.Action, "err", err)
	}
}
