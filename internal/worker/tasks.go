// Package worker: definisi task asynq dan handler-nya. Nama task `<resource>:<aksi>`; payload hanya ID.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
)

const (
	TaskItemIngest    = "item:ingest"
	TaskAccountDelete = "account:delete"
	TaskExportBuild   = "export:build"
	TaskBriefingBuild = "briefing:build"
)

type IngestPayload struct {
	ItemID  int64 `json:"item_id"`
	UserID  int64 `json:"user_id"`
	Version int32 `json:"version"`
}

type AccountPayload struct {
	UserID int64 `json:"user_id"`
}

type ExportPayload struct {
	JobID  int64 `json:"job_id"`
	UserID int64 `json:"user_id"`
}

type BriefingPayload struct {
	BriefingID int64 `json:"briefing_id"`
	UserID     int64 `json:"user_id"`
}

// Client mengimplementasikan service.Enqueuer dan service.BriefingQueue.
type Client struct{ c *asynq.Client }

func NewClient(redisOpt asynq.RedisConnOpt) *Client { return &Client{c: asynq.NewClient(redisOpt)} }

func (c *Client) Close() error { return c.c.Close() }

func (c *Client) EnqueueIngest(ctx context.Context, itemID, userID int64, version int32, queue string) error {
	b, _ := json.Marshal(IngestPayload{ItemID: itemID, UserID: userID, Version: version})
	if queue == "" {
		queue = "default"
	}
	// TaskID deterministik per versi → enqueue ganda diabaikan (idempoten, ADR-006).
	_, err := c.c.EnqueueContext(ctx, asynq.NewTask(TaskItemIngest, b),
		asynq.TaskID(fmt.Sprintf("item-ingest:%d:%d", itemID, version)), asynq.Queue(queue),
		asynq.MaxRetry(5), asynq.Timeout(25*time.Minute), asynq.Retention(time.Hour))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

func (c *Client) EnqueueAccountDelete(ctx context.Context, userID int64) error {
	b, _ := json.Marshal(AccountPayload{UserID: userID})
	_, err := c.c.EnqueueContext(ctx, asynq.NewTask(TaskAccountDelete, b), asynq.TaskID(fmt.Sprintf("account-delete:%d", userID)),
		asynq.Queue("low"), asynq.MaxRetry(10), asynq.Timeout(30*time.Minute))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

func (c *Client) EnqueueExport(ctx context.Context, jobID, userID int64) error {
	b, _ := json.Marshal(ExportPayload{JobID: jobID, UserID: userID})
	_, err := c.c.EnqueueContext(ctx, asynq.NewTask(TaskExportBuild, b), asynq.TaskID(fmt.Sprintf("export:%d", jobID)),
		asynq.Queue("low"), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}

// EnqueueBriefing menjadwalkan naskah + suara Siaran Vee (ADR-016). TaskID per siaran: GET yang
// berulang selama status building tidak menggandakan task yang masih antre/berjalan. Tanpa Retention
// supaya build bisa dijadwalkan lagi setelah selesai bila masih ada bab pending.
func (c *Client) EnqueueBriefing(ctx context.Context, briefingID, userID int64, queue string) error {
	b, _ := json.Marshal(BriefingPayload{BriefingID: briefingID, UserID: userID})
	if queue == "" {
		queue = "default"
	}
	_, err := c.c.EnqueueContext(ctx, asynq.NewTask(TaskBriefingBuild, b), asynq.TaskID(fmt.Sprintf("briefing:%d", briefingID)),
		asynq.Queue(queue), asynq.MaxRetry(3), asynq.Timeout(15*time.Minute))
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		return nil
	}
	return err
}
