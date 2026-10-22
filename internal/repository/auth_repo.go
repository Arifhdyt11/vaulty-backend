package repository

import (
	"context"
	"fmt"
	"time"

	"vaulty/internal/model"
)

// ---------- Users ----------

type UserRepo struct{ db DB }

const userCols = `id, email, name, avatar_url, timezone, plan, role, created_at`

func scanUser(row interface{ Scan(...any) error }) (model.User, error) {
	var u model.User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Timezone, &u.Plan, &u.Role, &u.CreatedAt)
	return u, mapErr(err)
}

type NewUser struct {
	Email, Name  string
	PasswordHash *string
	GoogleSub    *string
	AvatarURL    *string
}

func (r *UserRepo) Create(ctx context.Context, n NewUser) (model.User, error) {
	row := r.db.QueryRow(ctx, `INSERT INTO users (email, name, password_hash, google_sub, avatar_url)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+userCols, n.Email, n.Name, n.PasswordHash, n.GoogleSub, n.AvatarURL)
	u, err := scanUser(row)
	if isUnique(err) {
		return u, model.ErrEmailTaken
	}
	return u, err
}

func (r *UserRepo) ByEmail(ctx context.Context, email string) (model.User, string, error) {
	var u model.User
	var hash *string
	err := r.db.QueryRow(ctx, `SELECT `+userCols+`, password_hash FROM users WHERE email = $1 AND deleted_at IS NULL`, email).
		Scan(&u.ID, &u.Email, &u.Name, &u.AvatarURL, &u.Timezone, &u.Plan, &u.Role, &u.CreatedAt, &hash)
	if err != nil {
		return u, "", mapErr(err)
	}
	h := ""
	if hash != nil {
		h = *hash
	}
	return u, h, nil
}

func (r *UserRepo) ByID(ctx context.Context, id int64) (model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1 AND deleted_at IS NULL`, id))
}

func (r *UserRepo) ByGoogleSub(ctx context.Context, sub string) (model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE google_sub = $1 AND deleted_at IS NULL`, sub))
}

func (r *UserRepo) LinkGoogle(ctx context.Context, id int64, sub string, avatar *string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET google_sub = $2, avatar_url = COALESCE(avatar_url, $3), updated_at = now() WHERE id = $1`, id, sub, avatar)
	return err
}

func (r *UserRepo) Update(ctx context.Context, id int64, name, timezone *string) (model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `UPDATE users SET name = COALESCE($2, name), timezone = COALESCE($3, timezone), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL RETURNING `+userCols, id, name, timezone))
}

func (r *UserRepo) SetPlan(ctx context.Context, id int64, plan string) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET plan = $2, updated_at = now() WHERE id = $1`, id, plan)
	return err
}

// MarkDeleted menandai akun; penghapusan data nyata dilakukan worker (ADR-013).
func (r *UserRepo) MarkDeleted(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE users SET deleted_at = now(), email = email || '.deleted.' || id, google_sub = NULL, updated_at = now() WHERE id = $1 AND deleted_at IS NULL`, id)
	return err
}

// HardDelete menghapus user beserta semua data (FK cascade untuk sebagian; sisanya manual).
func (r *UserRepo) HardDelete(ctx context.Context, id int64) error {
	stmts := []string{
		`DELETE FROM item_chunks WHERE user_id = $1`,
		`DELETE FROM reminders WHERE user_id = $1`,
		`DELETE FROM messages WHERE user_id = $1`,
		`DELETE FROM conversations WHERE user_id = $1`,
		`DELETE FROM items WHERE user_id = $1`,
		`DELETE FROM audit_logs WHERE user_id = $1`,
		`DELETE FROM users WHERE id = $1`,
	}
	for _, s := range stmts {
		if _, err := r.db.Exec(ctx, s, id); err != nil {
			return fmt.Errorf("hapus akun: %w", err)
		}
	}
	return nil
}

// ---------- Sessions ----------

type SessionRepo struct{ db DB }

type NewSession struct {
	UserID                            int64
	AccessHash, RefreshHash           string
	AccessExpiresAt, RefreshExpiresAt time.Time
	UserAgent, IP                     string
}

func (r *SessionRepo) Create(ctx context.Context, n NewSession) (int64, error) {
	var id int64
	err := r.db.QueryRow(ctx, `INSERT INTO sessions (user_id, access_token_hash, refresh_token_hash, access_expires_at, refresh_expires_at, user_agent, ip)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`, n.UserID, n.AccessHash, n.RefreshHash, n.AccessExpiresAt, n.RefreshExpiresAt, n.UserAgent, n.IP).Scan(&id)
	return id, err
}

// UserByAccess mengembalikan user dari access token yang masih berlaku.
func (r *SessionRepo) UserByAccess(ctx context.Context, accessHash string) (model.User, error) {
	return scanUser(r.db.QueryRow(ctx, `SELECT u.id, u.email, u.name, u.avatar_url, u.timezone, u.plan, u.role, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.access_token_hash = $1 AND s.access_expires_at > now() AND u.deleted_at IS NULL`, accessHash))
}

// Rotate mengganti access+refresh token untuk sesi dengan refresh hash lama yang masih berlaku.
func (r *SessionRepo) Rotate(ctx context.Context, oldRefreshHash string, n NewSession) (int64, int64, error) {
	var id, userID int64
	err := r.db.QueryRow(ctx, `UPDATE sessions SET access_token_hash = $2, refresh_token_hash = $3, access_expires_at = $4,
		refresh_expires_at = $5, rotated_at = now()
		WHERE refresh_token_hash = $1 AND refresh_expires_at > now() RETURNING id, user_id`,
		oldRefreshHash, n.AccessHash, n.RefreshHash, n.AccessExpiresAt, n.RefreshExpiresAt).Scan(&id, &userID)
	return id, userID, mapErr(err)
}

func (r *SessionRepo) DeleteByRefresh(ctx context.Context, userID int64, refreshHash string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND refresh_token_hash = $2`, userID, refreshHash)
	return err
}

func (r *SessionRepo) DeleteByAccess(ctx context.Context, accessHash string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE access_token_hash = $1`, accessHash)
	return err
}

func (r *SessionRepo) DeleteAll(ctx context.Context, userID int64) error {
	_, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

func (r *SessionRepo) CleanupExpired(ctx context.Context) (int64, error) {
	t, err := r.db.Exec(ctx, `DELETE FROM sessions WHERE refresh_expires_at < now()`)
	return t.RowsAffected(), err
}

// ---------- Devices ----------

type DeviceRepo struct{ db DB }

const deviceCols = `id, user_id, platform, push_token, app_version, created_at`

func scanDevice(row interface{ Scan(...any) error }) (model.Device, error) {
	var d model.Device
	err := row.Scan(&d.ID, &d.UserID, &d.Platform, &d.PushToken, &d.AppVersion, &d.CreatedAt)
	return d, mapErr(err)
}

// Upsert: token yang sama pindah ke user yang sekarang login di perangkat itu.
func (r *DeviceRepo) Upsert(ctx context.Context, userID int64, platform, token, appVersion string) (model.Device, error) {
	return scanDevice(r.db.QueryRow(ctx, `INSERT INTO devices (user_id, platform, push_token, app_version)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (push_token) DO UPDATE SET user_id = EXCLUDED.user_id, platform = EXCLUDED.platform,
		  app_version = EXCLUDED.app_version, last_seen_at = now()
		RETURNING `+deviceCols, userID, platform, token, appVersion))
}

func (r *DeviceRepo) Delete(ctx context.Context, userID, id int64) error {
	t, err := r.db.Exec(ctx, `DELETE FROM devices WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if t.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}

func (r *DeviceRepo) DeleteByToken(ctx context.Context, token string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM devices WHERE push_token = $1`, token)
	return err
}

func (r *DeviceRepo) ListByUser(ctx context.Context, userID int64) ([]model.Device, error) {
	rows, err := r.db.Query(ctx, `SELECT `+deviceCols+` FROM devices WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---------- Audit ----------

type AuditRepo struct{ db DB }

func (r *AuditRepo) Log(ctx context.Context, userID *int64, action, entity string, entityID *int64, meta map[string]any, ip string) error {
	if meta == nil {
		meta = map[string]any{}
	}
	_, err := r.db.Exec(ctx, `INSERT INTO audit_logs (user_id, action, entity, entity_id, metadata, ip) VALUES ($1,$2,$3,$4,$5,$6)`,
		userID, action, entity, entityID, meta, ip)
	return err
}

// ---------- Fetch cache ----------

type FetchCacheRepo struct{ db DB }

func (r *FetchCacheRepo) Get(ctx context.Context, url string, maxAge time.Duration, out any) (bool, error) {
	var payload []byte
	err := r.db.QueryRow(ctx, `SELECT payload FROM fetch_cache WHERE canonical_url = $1 AND fetched_at > now() - $2::interval`, url, maxAge.String()).Scan(&payload)
	if err != nil {
		if mapErr(err) == model.ErrNotFound {
			return false, nil
		}
		return false, err
	}
	return true, jsonUnmarshal(payload, out)
}

func (r *FetchCacheRepo) Put(ctx context.Context, url string, payload any) error {
	b, err := jsonMarshal(payload)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO fetch_cache (canonical_url, payload, fetched_at) VALUES ($1, $2, now())
		ON CONFLICT (canonical_url) DO UPDATE SET payload = EXCLUDED.payload, fetched_at = now()`, url, b)
	return err
}
