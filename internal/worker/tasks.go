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

// Client mengimplementasikan service.Enqueuer.
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
		asynq.MaxRetry(5), asynq.Timeout(10*time.Minute), asynq.Retention(time.Hour))
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
