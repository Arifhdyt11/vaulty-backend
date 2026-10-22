package v1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"vaulty/internal/handler"
	"vaulty/internal/middleware"
	"vaulty/internal/model"
	"vaulty/internal/service"
)

// Event SSE sesuai kontrak.
type MetaEvent struct {
	ConversationID int64 `json:"conversation_id"`
	MessageID      int64 `json:"message_id"`
}
type TokenEvent struct {
	Text string `json:"text"`
}
type CitationsEvent struct {
	Citations []model.Citation `json:"citations"`
}
type DoneEvent struct {
	FromNotes bool  `json:"from_notes"`
	MessageID int64 `json:"message_id"`
}
type ErrorEvent struct {
	Message string `json:"message"`
}

type sseSink struct{ send sse.Sender }

func (s sseSink) Meta(c, m int64) error              { return s.send.Data(MetaEvent{c, m}) }
func (s sseSink) Token(t string) error               { return s.send.Data(TokenEvent{t}) }
func (s sseSink) Citations(c []model.Citation) error { return s.send.Data(CitationsEvent{c}) }
func (s sseSink) Done(f bool, id int64) error        { return s.send.Data(DoneEvent{f, id}) }

func (h *Handlers) RegisterChat(api huma.API) {
	sse.Register(api, huma.Operation{OperationID: "chat", Method: http.MethodPost, Path: "/chat", Summary: "Tanya Vee (SSE: meta, token, citations, done, error)", Tags: []string{"chat"}, Security: middleware.Secured},
		map[string]any{"meta": MetaEvent{}, "token": TokenEvent{}, "citations": CitationsEvent{}, "done": DoneEvent{}, "error": ErrorEvent{}},
		func(ctx context.Context, in *struct {
			Body struct {
				ConversationID *int64 `json:"conversation_id,omitempty"`
				Message        string `json:"message" minLength:"1" maxLength:"4000"`
				Mode           string `json:"mode,omitempty" enum:",notes,general" default:"notes"`
			}
		}, send sse.Sender) {
			u := middleware.UserFrom(ctx)
			err := h.Answer.Chat(ctx, u, service.ChatInput{ConversationID: in.Body.ConversationID, Message: in.Body.Message, Mode: in.Body.Mode}, sseSink{send})
			if err != nil {
				he := handler.E(err)
				_ = send.Data(ErrorEvent{Message: he.Error()})
			}
		})

	huma.Register(api, huma.Operation{OperationID: "conversation-list", Method: http.MethodGet, Path: "/conversations", Summary: "Daftar percakapan", Tags: []string{"chat"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			Cursor int64 `query:"cursor"`
			Limit  int   `query:"limit" default:"20" minimum:"1" maximum:"100"`
		}) (*struct {
			Body struct {
				Conversations []model.Conversation `json:"conversations"`
				NextCursor    *int64               `json:"next_cursor"`
			}
		}, error) {
			cs, next, err := h.Repos.ListConversations(ctx, middleware.UserFrom(ctx).ID, in.Cursor, in.Limit)
			if err != nil {
				return nil, handler.E(err)
			}
			if cs == nil {
				cs = []model.Conversation{}
			}
			out := &struct {
				Body struct {
					Conversations []model.Conversation `json:"conversations"`
					NextCursor    *int64               `json:"next_cursor"`
				}
			}{}
			out.Body.Conversations, out.Body.NextCursor = cs, next
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "conversation-get", Method: http.MethodGet, Path: "/conversations/{id}", Summary: "Percakapan + pesan", Tags: []string{"chat"}, Security: middleware.Secured},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct {
			Body struct {
				Conversation model.Conversation `json:"conversation"`
				Messages     []model.Message    `json:"messages"`
			}
		}, error) {
			u := middleware.UserFrom(ctx)
			c, err := h.Repos.Conversation(ctx, u.ID, in.ID)
			if err != nil {
				return nil, handler.E(err)
			}
			msgs, err := h.Repos.Messages(ctx, u.ID, in.ID, 200)
			if err != nil {
				return nil, handler.E(err)
			}
			// kartu item untuk citations
			for i := range msgs {
				for j := range msgs[i].Citations {
					if it, err := h.Items.Get(ctx, u, msgs[i].Citations[j].ItemID); err == nil {
						card := it.Card()
						msgs[i].Citations[j].Item = &card
					}
				}
			}
			if msgs == nil {
				msgs = []model.Message{}
			}
			out := &struct {
				Body struct {
					Conversation model.Conversation `json:"conversation"`
					Messages     []model.Message    `json:"messages"`
				}
			}{}
			out.Body.Conversation, out.Body.Messages = c, msgs
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "conversation-delete", Method: http.MethodDelete, Path: "/conversations/{id}", Summary: "Hapus percakapan", Tags: []string{"chat"}, Security: middleware.Secured, DefaultStatus: 204},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct{}, error) {
			if err := h.Repos.DeleteConversation(ctx, middleware.UserFrom(ctx).ID, in.ID); err != nil {
				return nil, handler.E(err)
			}
			return nil, nil
		})
}
