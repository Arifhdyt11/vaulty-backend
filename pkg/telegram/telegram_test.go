package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient("TOKEN")
	c.baseURL = srv.URL
	return c
}

func TestGetUpdates(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"ok":true,"result":[{"update_id":7,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"/start"}}]}`))
	})
	ups, err := c.GetUpdates(context.Background(), 5, 30)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/botTOKEN/getUpdates" || gotBody["offset"] != float64(5) {
		t.Errorf("request = %s %v", gotPath, gotBody)
	}
	if len(ups) != 1 || ups[0].UpdateID != 7 || ups[0].Message.From.ID != 42 || ups[0].Message.Text != "/start" {
		t.Errorf("updates = %+v", ups)
	}
}

func TestCallErrorTidakMembocorkanToken(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	})
	err := c.SendMessage(context.Background(), 1, "hai", "", nil)
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("err = %v; want Unauthorized", err)
	}
	if strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("error memuat token: %v", err)
	}

	c.baseURL = "http://127.0.0.1:1" // koneksi ditolak
	if err := c.SendMessage(context.Background(), 1, "hai", "", nil); err == nil || strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("error jaringan = %v; harus error tanpa token", err)
	}
}

func TestSendMessageDenganTombol(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"ok":true,"result":{}}`))
	})
	if err := c.SendMessage(context.Background(), 1, "hai", "HTML", Keyboard{{{Text: "🗑 1", CallbackData: "del:9"}}}); err != nil {
		t.Fatal(err)
	}
	kb, _ := got["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	if len(kb) != 1 || kb[0].([]any)[0].(map[string]any)["callback_data"] != "del:9" {
		t.Errorf("reply_markup = %v", got["reply_markup"])
	}

	got = nil
	if err := c.EditMessageText(context.Background(), 1, 5, "selesai", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["reply_markup"]; ok || got["message_id"] != float64(5) {
		t.Errorf("edit tanpa tombol = %v", got)
	}
}

func TestGetUpdatesCallbackQuery(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true,"result":[{"update_id":8,"callback_query":{"id":"cb1","from":{"id":42},"message":{"message_id":3,"chat":{"id":42,"type":"private"}},"data":"del:9"}}]}`))
	})
	ups, err := c.GetUpdates(context.Background(), 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cq := ups[0].CallbackQuery; cq == nil || cq.Data != "del:9" || cq.Message.MessageID != 3 {
		t.Errorf("callback = %+v", ups[0].CallbackQuery)
	}
}
