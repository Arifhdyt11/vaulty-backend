-- name: CreateReminder :one
INSERT INTO reminders (user_id, note_id, text, remind_at, repeat, lead_minutes, notify_at, notify_kind)
VALUES (sqlc.arg(user_id), sqlc.narg(note_id), sqlc.arg(text), sqlc.arg(remind_at), sqlc.arg(repeat),
        sqlc.arg(lead_minutes), sqlc.arg(notify_at), sqlc.arg(notify_kind))
RETURNING *;

-- name: GetReminder :one
SELECT * FROM reminders WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: ListUpcomingReminders :many
-- Reminder aktif (belum selesai/dibatalkan), urut notifikasi terdekat.
SELECT * FROM reminders
WHERE user_id = sqlc.arg(user_id) AND status IN ('pending', 'sending')
ORDER BY notify_at
LIMIT sqlc.arg(row_limit);

-- name: ClaimDueReminders :many
-- Query worker lintas user (pengecualian ADR-007): pengirim notifikasi memproses semua user,
-- dan setiap baris membawa user_id-nya sendiri untuk menentukan penerima.
-- Ambil reminder jatuh tempo untuk dikirim. SKIP LOCKED + status 'sending' mencegah kiriman dobel
-- bila ada lebih dari satu worker; 'sending' yang tertahan >5 menit (worker crash) diambil ulang.
UPDATE reminders SET status = 'sending', claimed_at = now(), updated_at = now()
WHERE id IN (
  SELECT id FROM reminders
  WHERE (status = 'pending' AND notify_at <= now())
     OR (status = 'sending' AND claimed_at < now() - interval '5 minutes')
  ORDER BY notify_at
  LIMIT sqlc.arg(row_limit)
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: FinishReminderSend :exec
-- Query worker (pengecualian ADR-007): hanya baris yang sedang di-claim worker (status sending).
-- Hasil kiriman: jadwal berikutnya (status pending) atau selesai (status sent).
UPDATE reminders
SET status = sqlc.arg(status), remind_at = sqlc.arg(remind_at), notify_at = sqlc.arg(notify_at),
    notify_kind = sqlc.arg(notify_kind), attempts = 0, claimed_at = NULL,
    last_sent_at = now(), updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'sending';

-- name: FailReminderSend :exec
-- Query worker (pengecualian ADR-007): hanya baris yang sedang di-claim worker (status sending).
-- Gagal kirim: coba lagi 1 menit kemudian; setelah max_attempts jadi failed.
UPDATE reminders
SET attempts = attempts + 1,
    status = CASE WHEN attempts + 1 >= sqlc.arg(max_attempts)::int THEN 'failed' ELSE 'pending' END,
    notify_at = now() + interval '1 minute', claimed_at = NULL, updated_at = now()
WHERE id = sqlc.arg(id) AND status = 'sending';

-- name: RescheduleReminder :one
-- Tunda (snooze) atau aktifkan ulang: notifikasi berikutnya di notify_at.
UPDATE reminders
SET notify_at = sqlc.arg(notify_at), notify_kind = sqlc.arg(notify_kind), status = 'pending',
    attempts = 0, updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status IN ('pending', 'sent', 'failed')
RETURNING *;

-- name: SetReminderStatus :one
UPDATE reminders SET status = sqlc.arg(status), updated_at = now()
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id) AND status = ANY(sqlc.arg(from_status)::text[])
RETURNING *;
