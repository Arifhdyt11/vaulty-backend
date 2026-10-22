package repository

import (
	"context"

	"vaulty/internal/model"
)

// Access: adaptor kecil untuk handler yang hanya butuh CRUD sederhana (devices, conversations).
type Access struct{ r *Repos }

func (r *Repos) Access() *Access { return &Access{r: r} }

func (a *Access) UpsertDevice(ctx context.Context, userID int64, platform, token, appVersion string) (model.Device, error) {
	return a.r.Devices.Upsert(ctx, userID, platform, token, appVersion)
}
func (a *Access) DeleteDevice(ctx context.Context, userID, id int64) error {
	return a.r.Devices.Delete(ctx, userID, id)
}
func (a *Access) ListConversations(ctx context.Context, userID int64, cursor int64, limit int) ([]model.Conversation, *int64, error) {
	return a.r.Chats.ListConversations(ctx, userID, cursor, limit)
}
func (a *Access) Conversation(ctx context.Context, userID, id int64) (model.Conversation, error) {
	return a.r.Chats.Conversation(ctx, userID, id)
}
func (a *Access) Messages(ctx context.Context, userID, convID int64, limit int) ([]model.Message, error) {
	return a.r.Chats.Messages(ctx, userID, convID, limit)
}
func (a *Access) DeleteConversation(ctx context.Context, userID, id int64) error {
	return a.r.Chats.DeleteConversation(ctx, userID, id)
}
