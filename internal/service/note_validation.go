package service

import (
	"regexp"
	"strings"

	"vaulty-api/internal/model"
)

// Tipe item bebas (note, link, command, document, snippet, ...), tetapi credential
// tetap dilarang secara desain (NF1, ADR-004).
var (
	typePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

	blockedTypes = map[string]bool{
		"credential": true, "credentials": true, "password": true, "passwords": true,
		"secret": true, "secrets": true, "apikey": true, "api-key": true, "api_key": true,
		"token": true, "tokens": true, "private-key": true, "private_key": true,
		"ssh-key": true, "ssh_key": true, "pin": true, "otp": true,
	}

	singleURL = regexp.MustCompile(`^https?://\S+$`)
)

func NormalizeType(t string) (string, error) {
	t = strings.ToLower(strings.TrimSpace(t))
	if !typePattern.MatchString(t) {
		return "", model.ErrInvalidType
	}
	if blockedTypes[t] {
		return "", model.ErrBlockedType
	}
	return t, nil
}

// InferType dipakai jika client tidak mengirim type (F2): URL dikenali sebagai link.
// Jika body hanya berisi satu URL, URL itu dipindah ke field url.
func InferType(body, url string) (typ, newBody, newURL string) {
	trimmed := strings.TrimSpace(body)
	if url == "" && singleURL.MatchString(trimmed) {
		return model.NoteTypeLink, "", trimmed
	}
	if url != "" {
		return model.NoteTypeLink, body, url
	}
	return model.NoteTypeNote, body, url
}

// Pola yang mengindikasikan credential. Hanya memberi warning, tidak memblokir,
// karena catatan teknis sering menyebut kata seperti "password" tanpa berisi rahasia.
var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"AWS access key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"OpenAI/Anthropic API key", regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_\-]{20,}`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[abpr]-[A-Za-z0-9-]{10,}`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"password di connection string", regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^\s:/@]+:[^\s@/]{3,}@`)},
	{"password/secret assignment", regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|api[_-]?key|access[_-]?token)\b\s*[:=]\s*["']?[^\s"'$<{]{6,}`)},
}

// DetectSecrets mengembalikan warning untuk setiap jenis pola credential yang ditemukan.
func DetectSecrets(texts ...string) []string {
	var warnings []string
	joined := strings.Join(texts, "\n")
	for _, p := range secretPatterns {
		if p.re.MatchString(joined) {
			warnings = append(warnings, "Terdeteksi pola "+p.name+". Vaulty tidak dirancang untuk menyimpan credential; pertimbangkan memindahkannya ke password manager.")
		}
	}
	return warnings
}
