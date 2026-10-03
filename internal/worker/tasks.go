// Package worker berisi task asynq (Redis): indexing note, safety net, pembersihan session, dan pengiriman reminder.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/hibiken/asynq"
)

const (
	// TaskIndexNote: ekstraksi teks file, auto-tag, dan embedding untuk satu versi note.
	TaskIndexNote = "note:index"
	// TaskRequeuePending: safety net periodik (note pending tertahan, model embedding berubah).
	TaskRequeuePending = "note:requeue_pending"
	// TaskCleanupSessions: hapus session kedaluwarsa.
	TaskCleanupSessions = "session:cleanup"
	// TaskDeliverReminders: kirim reminder yang jatuh tempo (memindai tabel reminders, ADR-023).
	TaskDeliverReminders = "reminder:deliver"

	indexMaxRetry = 5
)

type indexPayload struct {
	NoteID  int64 `json:"note_id"`
	UserID  int64 `json:"user_id"`
	Version int32 `json:"version"`
}

// Enqueuer mengimplementasikan service.IndexEnqueuer dengan asynq.
type Enqueuer struct{ client *asynq.Client }

func NewEnqueuer(client *asynq.Client) *Enqueuer { return &Enqueuer{client: client} }

// EnqueueIndex idempoten per (note, versi): TaskID yang sama tidak di-enqueue dua kali.
func (e *Enqueuer) EnqueueIndex(ctx context.Context, noteID, userID int64, version int32) error {
	b, err := json.Marshal(indexPayload{NoteID: noteID, UserID: userID, Version: version})
	if err != nil {
		return err
	}
	_, err = e.client.EnqueueContext(ctx, asynq.NewTask(TaskIndexNote, b),
		asynq.TaskID(fmt.Sprintf("note-index:%d:%d", noteID, version)),
		asynq.MaxRetry(indexMaxRetry))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// RedisOpt mem-parse REDIS_URL untuk client, server, dan scheduler asynq.
func RedisOpt(url string) (asynq.RedisConnOpt, error) {
	opt, err := asynq.ParseRedisURI(url)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	return opt, nil
}
