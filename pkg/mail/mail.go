// Package mail: kirim email transaksional (OTP) lewat SMTP stdlib, tanpa dependency (ADR-022).
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured: server belum punya SMTP; pemanggil menampilkan pesan yang jelas ke user.
var ErrNotConfigured = errors.New("pengiriman email belum dikonfigurasi")

type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type Sender interface {
	Send(ctx context.Context, m Message) error
	Enabled() bool
}

type SMTPConfig struct {
	Host string
	Port int // 465 = TLS langsung, lainnya STARTTLS bila server mendukung
	User string
	Pass string
	From string // "Vaulty <no-reply@domain>"
}

type SMTP struct{ cfg SMTPConfig }

func NewSMTP(cfg SMTPConfig) *SMTP { return &SMTP{cfg: cfg} }

func (s *SMTP) Enabled() bool { return true }

func (s *SMTP) Send(ctx context.Context, m Message) error {
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return fmt.Errorf("MAIL_FROM tidak valid: %w", err)
	}
	body, err := Build(s.cfg.From, m, time.Now())
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := &net.Dialer{Deadline: deadline}
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	if s.cfg.Port == 465 {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("sambung smtp: %w", err)
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp hello: %w", err)
	}
	defer func() { _ = c.Close() }()
	if s.cfg.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	if s.cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("smtp tulis: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp kirim: %w", err)
	}
	return c.Quit()
}

// Build menyusun pesan MIME multipart/alternative (teks + HTML).
func Build(from string, m Message, now time.Time) ([]byte, error) {
	if strings.ContainsAny(m.To+m.Subject+from, "\r\n") {
		return nil, errors.New("header email tidak boleh berisi baris baru")
	}
	boundary := randomBoundary()
	var b bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	h("From", from)
	h("To", m.To)
	h("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	h("Date", now.Format(time.RFC1123Z))
	h("MIME-Version", "1.0")
	h("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")
	part := func(ctype, content string) error {
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", boundary, ctype)
		qp := quotedprintable.NewWriter(&b)
		if _, err := qp.Write([]byte(content)); err != nil {
			return err
		}
		if err := qp.Close(); err != nil {
			return err
		}
		b.WriteString("\r\n")
		return nil
	}
	if err := part("text/plain", m.Text); err != nil {
		return nil, err
	}
	if m.HTML != "" {
		if err := part("text/html", m.HTML); err != nil {
			return nil, err
		}
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes(), nil
}

func randomBoundary() string {
	x := make([]byte, 12)
	_, _ = rand.Read(x)
	return "vaulty-" + hex.EncodeToString(x)
}

// Log: pengganti SMTP khusus development; isi email (termasuk kode) masuk log lokal supaya
// alur OTP bisa dicoba tanpa server email. Jangan dipakai di production.
type Log struct{}

func (Log) Enabled() bool { return true }

func (Log) Send(ctx context.Context, m Message) error {
	slog.WarnContext(ctx, "EMAIL (dev, tidak dikirim)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

// Disabled: production tanpa SMTP. Fitur email dimatikan dan client diberi tahu lewat /config.
type Disabled struct{}

func (Disabled) Enabled() bool                       { return false }
func (Disabled) Send(context.Context, Message) error { return ErrNotConfigured }
