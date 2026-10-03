package bootstrap

import (
	"context"
	"errors"
	"log/slog"

	"vaulty-api/internal/model"
	"vaulty-api/internal/repository"
)

// TelegramUsers mengubah TELEGRAM_USERS (ID Telegram -> email) menjadi ID Telegram -> user_id.
// Dipakai bot (siapa yang dilayani) dan worker (ke chat mana reminder dikirim). Email yang belum
// terdaftar dilewati; proses perlu di-restart setelah akunnya dibuat.
func TelegramUsers(ctx context.Context, repo *repository.UserRepository, emails map[int64]string) map[int64]int64 {
	out := make(map[int64]int64, len(emails))
	for tgID, email := range emails {
		u, _, err := repo.FindByEmail(ctx, email)
		if errors.Is(err, model.ErrNotFound) {
			slog.WarnContext(ctx, "akun Vaulty untuk TELEGRAM_USERS belum ada, dilewati", "telegram_id", tgID)
			continue
		}
		if err != nil {
			Fatal("cari akun TELEGRAM_USERS", err)
		}
		out[tgID] = u.ID
	}
	return out
}
