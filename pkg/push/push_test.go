package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

func newTestFCM(t *testing.T, handler http.HandlerFunc) *FCM {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "tes-token", TokenType: "Bearer"})
	client := oauth2.NewClient(context.Background(), ts)
	return NewFCMWithClient("proj-1", client, srv.URL+"/v1/projects/proj-1/messages:send")
}

func TestSendPayload(t *testing.T) {
	var got map[string]any
	var auth string
	f := newTestFCM(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"name":"projects/proj-1/messages/1"}`))
	})
	err := f.Send(context.Background(), "tok123", Notification{Title: "Ingat", Body: "Baca artikel", ImageURL: "https://img/x.jpg"}, map[string]string{"item_id": "42"})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer tes-token" {
		t.Errorf("auth = %q", auth)
	}
	msg := got["message"].(map[string]any)
	if msg["token"] != "tok123" {
		t.Errorf("token = %v", msg["token"])
	}
	n := msg["notification"].(map[string]any)
	if n["title"] != "Ingat" || n["body"] != "Baca artikel" || n["image"] != "https://img/x.jpg" {
		t.Errorf("notification = %v", n)
	}
	if msg["data"].(map[string]any)["item_id"] != "42" {
		t.Errorf("data = %v", msg["data"])
	}
	android := msg["android"].(map[string]any)
	if android["priority"] != "high" {
		t.Errorf("android = %v", android)
	}
	an := android["notification"].(map[string]any)
	if an["channel_id"] != "reminders" || an["click_action"] != "FLUTTER_NOTIFICATION_CLICK" {
		t.Errorf("android.notification = %v", an)
	}
	apns := msg["apns"].(map[string]any)
	if apns["headers"].(map[string]any)["apns-priority"] != "10" {
		t.Errorf("apns headers = %v", apns["headers"])
	}
	aps := apns["payload"].(map[string]any)["aps"].(map[string]any)
	if aps["sound"] != "default" || aps["category"] != "REMINDER" || aps["mutable-content"] != float64(1) {
		t.Errorf("aps = %v", aps)
	}
}

func TestSendUnregistered(t *testing.T) {
	f := newTestFCM(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
	})
	err := f.Send(context.Background(), "tok", Notification{Title: "x"}, nil)
	if !errors.Is(err, ErrInvalidToken) {
		t.Errorf("want ErrInvalidToken, got %v", err)
	}
}

func TestSendInvalidArgumentToken(t *testing.T) {
	f := newTestFCM(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"The registration token is not a valid FCM registration token","status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`))
	})
	if err := f.Send(context.Background(), "tok", Notification{Title: "x"}, nil); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("want ErrInvalidToken, got %v", err)
	}
	// INVALID_ARGUMENT karena payload (bukan token) tidak boleh menghapus device.
	f = newTestFCM(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid JSON payload","status":"INVALID_ARGUMENT"}}`))
	})
	if err := f.Send(context.Background(), "tok", Notification{Title: "x"}, nil); err == nil || errors.Is(err, ErrInvalidToken) {
		t.Errorf("payload salah harus error biasa, got %v", err)
	}
}

func TestSendServerError(t *testing.T) {
	f := newTestFCM(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"unavailable","status":"UNAVAILABLE"}}`))
	})
	err := f.Send(context.Background(), "tok", Notification{Title: "x"}, nil)
	if err == nil || errors.Is(err, ErrInvalidToken) {
		t.Errorf("want error biasa, got %v", err)
	}
}

func TestEmptyTokenAndDisabled(t *testing.T) {
	f := NewFCMWithClient("p", nil, "")
	if err := f.Send(context.Background(), "", Notification{}, nil); !errors.Is(err, ErrInvalidToken) {
		t.Errorf("token kosong: %v", err)
	}
	if f.endpoint != "https://fcm.googleapis.com/v1/projects/p/messages:send" {
		t.Errorf("endpoint default = %s", f.endpoint)
	}
	if err := (Disabled{}).Send(context.Background(), "tok", Notification{}, nil); !errors.Is(err, ErrDisabled) {
		t.Errorf("disabled: %v", err)
	}
}

func TestNewFCMParsesJSON(t *testing.T) {
	if _, err := NewFCM(context.Background(), []byte(`{"project_id":"p"}`)); err == nil {
		t.Error("json tidak lengkap harus error")
	}
	f, err := NewFCM(context.Background(), []byte(`{"project_id":"proj-x","client_email":"a@b.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n","token_uri":"https://oauth2.googleapis.com/token"}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.ProjectID() != "proj-x" || f.endpoint != "https://fcm.googleapis.com/v1/projects/proj-x/messages:send" {
		t.Errorf("fcm = %+v", f)
	}
}
