// Package push mengirim notifikasi ke perangkat lewat FCM HTTP v1.
//
// Satu implementasi FCM dipakai untuk Android dan iOS (lewat APNs via FCM),
// sehingga backend cukup menyimpan satu jenis push_token per device.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// Notification adalah bagian yang terlihat user. ImageURL opsional.
type Notification struct {
	Title    string
	Body     string
	ImageURL string
}

// Sender diabstraksi supaya worker reminder bisa dites tanpa FCM dan supaya
// deployment tanpa kredensial Firebase tetap jalan (pakai Disabled).
type Sender interface {
	Send(ctx context.Context, token string, n Notification, data map[string]string) error
}

var (
	// ErrInvalidToken: token sudah tidak terdaftar/invalid → pemanggil
	// sebaiknya menghapus device supaya tidak dikirimi lagi.
	ErrInvalidToken = errors.New("push token tidak valid")
	ErrDisabled     = errors.New("push tidak dikonfigurasi")
)

const (
	scopeMessaging = "https://www.googleapis.com/auth/firebase.messaging"
	// Channel dan category harus cocok dengan yang didaftarkan di app Flutter.
	androidChannel = "reminders"
	clickAction    = "FLUTTER_NOTIFICATION_CLICK"
	apnsCategory   = "REMINDER"
)

// FCM mengirim lewat endpoint HTTP v1. Endpoint dan client adalah field
// supaya tes bisa mengarahkannya ke httptest.
type FCM struct {
	projectID string
	client    *http.Client
	endpoint  string
}

// serviceAccount adalah subset file JSON service account Firebase.
type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// NewFCM membaca service account JSON dan menyiapkan client OAuth2 (JWT
// bearer flow). Token di-cache dan diperbarui otomatis oleh oauth2.
//
// Dipakai golang.org/x/oauth2/jwt langsung, bukan oauth2/google, karena
// oauth2/google menarik cloud.google.com/go/compute/metadata yang tidak ada
// di go.mod; secara fungsional sama dengan google.JWTConfigFromJSON.
func NewFCM(ctx context.Context, serviceAccountJSON []byte) (*FCM, error) {
	var sa serviceAccount
	if err := json.Unmarshal(serviceAccountJSON, &sa); err != nil {
		return nil, fmt.Errorf("service account json: %w", err)
	}
	if sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("service account json tidak lengkap (project_id, client_email, private_key)")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	conf := &jwt.Config{
		Email:      sa.ClientEmail,
		PrivateKey: []byte(sa.PrivateKey),
		Scopes:     []string{scopeMessaging},
		TokenURL:   sa.TokenURI,
	}
	return NewFCMWithTokenSource(ctx, sa.ProjectID, conf.TokenSource(ctx)), nil
}

// NewFCMWithTokenSource memakai token source yang disuntik (mis. untuk tes
// atau kredensial dari metadata server).
func NewFCMWithTokenSource(ctx context.Context, projectID string, ts oauth2.TokenSource) *FCM {
	base := &http.Client{Timeout: 15 * time.Second}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, base)
	return NewFCMWithClient(projectID, oauth2.NewClient(ctx, ts), "")
}

// NewFCMWithClient memakai http.Client yang sudah membawa autentikasi
// sendiri. endpoint kosong → endpoint FCM resmi untuk projectID.
func NewFCMWithClient(projectID string, client *http.Client, endpoint string) *FCM {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", projectID)
	}
	return &FCM{projectID: projectID, client: client, endpoint: endpoint}
}

// ProjectID mengembalikan project Firebase yang dipakai.
func (f *FCM) ProjectID() string { return f.projectID }

// Struktur payload FCM v1 (hanya field yang dipakai).
type message struct {
	Token        string            `json:"token"`
	Notification *notification     `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	Android      *androidConfig    `json:"android,omitempty"`
	APNS         *apnsConfig       `json:"apns,omitempty"`
}

type notification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
	Image string `json:"image,omitempty"`
}

type androidConfig struct {
	Priority     string               `json:"priority"`
	Notification *androidNotification `json:"notification,omitempty"`
}

type androidNotification struct {
	ChannelID   string `json:"channel_id"`
	ClickAction string `json:"click_action"`
}

type apnsConfig struct {
	Headers    map[string]string `json:"headers"`
	Payload    apnsPayload       `json:"payload"`
	FCMOptions *apnsFCMOptions   `json:"fcm_options,omitempty"`
}

type apnsPayload struct {
	APS aps `json:"aps"`
}

type aps struct {
	Sound          string `json:"sound"`
	Category       string `json:"category"`
	MutableContent int    `json:"mutable-content"`
}

type apnsFCMOptions struct {
	Image string `json:"image,omitempty"`
}

// Send mengirim satu pesan. 404/400 dengan errorCode UNREGISTERED atau
// INVALID_ARGUMENT (token) → ErrInvalidToken; selain itu error biasa yang
// layak di-retry oleh antrean.
func (f *FCM) Send(ctx context.Context, token string, n Notification, data map[string]string) error {
	if strings.TrimSpace(token) == "" {
		return ErrInvalidToken
	}
	msg := message{
		Token:        token,
		Notification: &notification{Title: n.Title, Body: n.Body, Image: n.ImageURL},
		Data:         data,
		Android: &androidConfig{
			// "high" supaya reminder tetap tiba saat Doze; channel & click_action
			// harus sama dengan yang dibuat app.
			Priority:     "high",
			Notification: &androidNotification{ChannelID: androidChannel, ClickAction: clickAction},
		},
		APNS: &apnsConfig{
			Headers: map[string]string{"apns-priority": "10"},
			Payload: apnsPayload{APS: aps{Sound: "default", Category: apnsCategory, MutableContent: 1}},
		},
	}
	if n.ImageURL != "" {
		// iOS hanya menampilkan gambar lewat fcm_options + Notification Service
		// Extension (mutable-content=1 di atas).
		msg.APNS.FCMOptions = &apnsFCMOptions{Image: n.ImageURL}
	}
	body, err := json.Marshal(struct {
		Message message `json:"message"`
	}{msg})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fcm: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return classifyError(resp.StatusCode, raw)
}

// fcmError adalah bentuk error standar Google API + detail FcmError.
type fcmError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type      string `json:"@type"`
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

func classifyError(status int, raw []byte) error {
	var fe fcmError
	_ = json.Unmarshal(raw, &fe)
	code := ""
	for _, d := range fe.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
			break
		}
	}
	msg := fe.Error.Message
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	switch {
	case code == "UNREGISTERED", status == http.StatusNotFound && fe.Error.Status == "NOT_FOUND":
		return fmt.Errorf("%w: unregistered: %s", ErrInvalidToken, msg)
	case code == "SENDER_ID_MISMATCH":
		// Token milik project Firebase lain; tidak akan pernah berhasil.
		return fmt.Errorf("%w: sender id mismatch: %s", ErrInvalidToken, msg)
	case code == "INVALID_ARGUMENT" || fe.Error.Status == "INVALID_ARGUMENT":
		// INVALID_ARGUMENT juga dipakai untuk payload salah; hanya anggap token
		// invalid bila pesannya memang menyebut token, supaya bug payload tidak
		// menghapus device user.
		if strings.Contains(strings.ToLower(msg), "token") {
			return fmt.Errorf("%w: %s", ErrInvalidToken, msg)
		}
	}
	return fmt.Errorf("fcm: status %d %s: %s", status, fe.Error.Status, msg)
}

// Disabled dipakai saat FIREBASE_SERVICE_ACCOUNT tidak diset (dev/CI).
type Disabled struct{}

func (Disabled) Send(context.Context, string, Notification, map[string]string) error {
	return ErrDisabled
}

var (
	_ Sender = (*FCM)(nil)
	_ Sender = Disabled{}
)
