-- +goose Up
-- Reminder (ADR-023). remind_at = waktu acara; notify_at = notifikasi berikutnya, bisa lebih awal
-- (pengingat bertahap, lead_minutes) atau hasil tunda (snooze). Worker memindai notify_at,
-- jadi tabel ini satu-satunya sumber kebenaran jadwal (tidak bergantung task di Redis).
CREATE TABLE reminders (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT NOT NULL REFERENCES users(id),
  note_id      BIGINT REFERENCES notes(id) ON DELETE SET NULL,
  text         TEXT NOT NULL CHECK (length(text) BETWEEN 1 AND 500),
  remind_at    TIMESTAMPTZ NOT NULL,
  repeat       TEXT NOT NULL DEFAULT '' CHECK (repeat IN ('', 'daily', 'weekly', 'monthly', 'yearly')),
  lead_minutes INT[] NOT NULL DEFAULT '{}',   -- mis. {43200, 10080, 1440} = H-30, H-7, H-1
  notify_at    TIMESTAMPTZ NOT NULL,
  notify_kind  TEXT NOT NULL DEFAULT 'event' CHECK (notify_kind IN ('event', 'lead', 'snooze')),
  -- pending: menunggu notify_at; sending: sedang dikirim worker; sent: reminder sekali sudah terkirim;
  -- done: ditandai selesai user; cancelled: dibatalkan; failed: gagal kirim berulang.
  status       TEXT NOT NULL DEFAULT 'pending'
               CHECK (status IN ('pending', 'sending', 'sent', 'done', 'cancelled', 'failed')),
  attempts     INT NOT NULL DEFAULT 0,
  claimed_at   TIMESTAMPTZ,
  last_sent_at TIMESTAMPTZ,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX reminders_due_idx ON reminders (notify_at) WHERE status = 'pending';
CREATE INDEX ON reminders (user_id, notify_at);

-- +goose Down
DROP TABLE reminders;
