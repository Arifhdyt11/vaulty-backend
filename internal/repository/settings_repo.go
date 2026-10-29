package repository

import (
	"context"
	"encoding/json"
)

// SettingsRepo: app_settings, pengaturan platform yang diubah admin (ADR-021).
type SettingsRepo struct{ db DB }

func (r *SettingsRepo) All(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := r.db.Query(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v json.RawMessage
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (r *SettingsRepo) Set(ctx context.Context, key string, value any, by int64) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO app_settings (key, value, updated_by) VALUES ($1, $2, $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_by = EXCLUDED.updated_by, updated_at = now()`, key, b, by)
	return err
}
