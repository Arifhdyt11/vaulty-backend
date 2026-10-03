// Package telegram adalah client minimal Telegram Bot API lewat HTTP (tanpa SDK, ADR-021):
// long polling getUpdates, sendMessage/editMessageText dengan tombol inline, dan answerCallbackQuery.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultBaseURL = "https://api.telegram.org"

type Client struct {
	baseURL, token string
	http           *http.Client
}

func NewClient(token string) *Client {
	// Timeout harus lebih panjang dari timeout long polling getUpdates.
	return &Client{baseURL: defaultBaseURL, token: token, http: &http.Client{Timeout: 70 * time.Second}}
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsBot    bool   `json:"is_bot"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"` // private, group, supergroup, channel
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

// CallbackQuery dikirim Telegram saat user menekan tombol inline.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// Button adalah tombol inline; CallbackData maksimal 64 byte.
type Button struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// Keyboard adalah baris-baris tombol inline di bawah pesan. nil = tanpa tombol.
type Keyboard [][]Button

// GetUpdates menunggu pesan baru hingga timeoutSec detik (long polling).
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	var out []Update
	err := c.call(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message", "callback_query"},
	}, &out)
	return out, err
}

// SendMessage mengirim teks. parseMode "HTML" atau "" (teks polos).
func (c *Client) SendMessage(ctx context.Context, chatID int64, text, parseMode string, kb Keyboard) error {
	return c.call(ctx, "sendMessage", messageBody(map[string]any{"chat_id": chatID}, text, parseMode, kb), nil)
}

// EditMessageText mengganti teks pesan bot. kb nil menghapus tombolnya.
func (c *Client) EditMessageText(ctx context.Context, chatID, messageID int64, text, parseMode string, kb Keyboard) error {
	return c.call(ctx, "editMessageText", messageBody(map[string]any{"chat_id": chatID, "message_id": messageID}, text, parseMode, kb), nil)
}

// AnswerCallbackQuery menghentikan loading di tombol; text tampil sebagai notifikasi singkat.
func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}

// Command adalah item menu "/" bawaan Telegram. Nama hanya boleh huruf kecil, angka, dan "_".
type Command struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// SetMyCommands mengisi tombol Menu di samping kolom ketik.
func (c *Client) SetMyCommands(ctx context.Context, cmds []Command) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

func messageBody(body map[string]any, text, parseMode string, kb Keyboard) map[string]any {
	body["text"] = text
	body["link_preview_options"] = map[string]bool{"is_disabled": true}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	if len(kb) > 0 {
		body["reply_markup"] = map[string]any{"inline_keyboard": kb}
	}
	return body
}

func (c *Client) call(ctx context.Context, method string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		// Error dari net/http menyertakan URL yang berisi token; jangan diteruskan ke log.
		return fmt.Errorf("telegram %s: request gagal", method)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 10<<20))
	if err != nil {
		return err
	}
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("telegram %s: status %d, respons tidak valid", method, res.StatusCode)
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: status %d: %s", method, res.StatusCode, env.Description)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}
