package aiagent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrNoReminderTime: kalimat tidak menyebut waktu yang bisa dipahami.
var ErrNoReminderTime = errors.New("waktu reminder tidak ditemukan")

// WIB dipakai tetap (UTC+7): image alpine tidak membawa tzdata dan Indonesia tidak memakai DST.
var WIB = time.FixedZone("WIB", 7*3600)

// ReminderDraft hasil membaca kalimat reminder. Repeat: "", daily, weekly, monthly, yearly.
type ReminderDraft struct {
	Text        string
	At          time.Time
	Repeat      string
	LeadMinutes []int
}

type ReminderParser interface {
	// ParseReminder membaca "besok jam 9 follow up client X" relatif terhadap now.
	ParseReminder(ctx context.Context, text string, now time.Time) (ReminderDraft, error)
}

// Pola "30 menit lagi", "2 jam lagi", "3 hari lagi" dihitung tanpa LLM supaya pasti benar.
var relativeTime = regexp.MustCompile(`(?i)\b(\d{1,4})\s*(menit|mnt|m|jam|j|hari|h)\s+(lagi|dari sekarang)\b`)

// ParseRelative mencoba pola waktu relatif. ok=false jika tidak cocok.
func ParseRelative(text string, now time.Time) (ReminderDraft, bool) {
	m := relativeTime.FindStringSubmatchIndex(text)
	if m == nil {
		return ReminderDraft{}, false
	}
	n, _ := strconv.Atoi(text[m[2]:m[3]])
	var d time.Duration
	switch strings.ToLower(text[m[4]:m[5]]) {
	case "menit", "mnt", "m":
		d = time.Duration(n) * time.Minute
	case "jam", "j":
		d = time.Duration(n) * time.Hour
	default:
		d = time.Duration(n) * 24 * time.Hour
	}
	rest := strings.TrimSpace(text[:m[0]] + " " + text[m[1]:])
	return ReminderDraft{Text: cleanReminderText(rest), At: now.Add(d).Truncate(time.Minute)}, true
}

var reminderVerb = regexp.MustCompile(`(?i)^\s*/?(ingatkan|ingetin|ingatin|remind(er)?|pengingat)\s*(aku|saya|gue|me)?\s*(untuk|buat|utk|to)?\s*`)

// Kata waktu yang sering ikut tertulis di teks reminder oleh model (atau dari pola relatif).
var timeWords = []*regexp.Regexp{
	regexp.MustCompile(`(?i),?\s*(dan\s+)?ingatkan\s+(juga|lagi)\b.*$`),
	regexp.MustCompile(`(?i)\b(besok|lusa|hari ini|malam ini|nanti)(\s+(pagi|siang|sore|malam))?\b`),
	regexp.MustCompile(`(?i)\b(jam|pukul)\s*\d{1,2}([:.]\d{2})?\b`),
	regexp.MustCompile(`(?i)\b(tiap|setiap)\s+(hari|minggu|bulan|tahun|senin|selasa|rabu|kamis|jumat|sabtu|tanggal\s*\d{1,2})\b`),
	regexp.MustCompile(`(?i)\btanggal\s*\d{1,2}\b`),
	regexp.MustCompile(`(?i)\bH\s*-\s*\d{1,3}\b`),
	regexp.MustCompile(`(?i),?\s*(\d{1,4}\s*(hari|jam|menit)|sehari|seminggu|sebulan|sejam)\s+sebelum(nya)?(\s+juga)?\b`),
}

// timeHint: kalimat tanpa petunjuk waktu sama sekali langsung ditanya "kapan?" tanpa LLM,
// karena model cenderung mengarang waktu (mis. hari ini 09:00).
var timeHint = regexp.MustCompile(`(?i)\d|\b(besok|lusa|hari ini|nanti|pagi|siang|sore|malam|senin|selasa|rabu|kamis|jumat|jum'at|sabtu|minggu|tanggal|tgl|jam|pukul|tiap|setiap|lagi|depan|jan|feb|mar|apr|mei|jun|jul|agu|agt|sep|okt|nov|des)\w*`)

func cleanReminderText(s string) string {
	s = reminderVerb.ReplaceAllString(strings.TrimSpace(s), "")
	for _, re := range timeWords {
		s = re.ReplaceAllString(s, " ")
	}
	return strings.Trim(strings.Join(strings.Fields(s), " "), " ,.-")
}

// Pengulangan hanya diterima bila user memang menyebutnya; model gratis kadang mengarang "harian".
var repeatHint = regexp.MustCompile(`(?i)\b(tiap|setiap|every|harian|mingguan|bulanan|tahunan|rutin)\b`)

var (
	leadHRe     = regexp.MustCompile(`(?i)\bH\s*-\s*(\d{1,3})\b`)
	leadBefore  = regexp.MustCompile(`(?i)\b(\d{1,4})\s*(hari|jam|menit)\s+sebelum`)
	leadWordsRe = regexp.MustCompile(`(?i)\b(sehari|seminggu|sebulan|sejam)\s+sebelum`)
)

// ParseLeads membaca pengingat bertahap langsung dari kalimat user (tanpa LLM):
// "H-30", "3 hari sebelumnya", "seminggu sebelum".
func ParseLeads(text string) []int {
	var out []int
	for _, m := range leadHRe.FindAllStringSubmatch(text, -1) {
		if n, _ := strconv.Atoi(m[1]); n > 0 {
			out = append(out, n*1440)
		}
	}
	for _, m := range leadBefore.FindAllStringSubmatch(text, -1) {
		n, _ := strconv.Atoi(m[1])
		switch strings.ToLower(m[2]) {
		case "hari":
			n *= 1440
		case "jam":
			n *= 60
		}
		if n > 0 {
			out = append(out, n)
		}
	}
	words := map[string]int{"sehari": 1440, "seminggu": 7 * 1440, "sebulan": 30 * 1440, "sejam": 60}
	for _, m := range leadWordsRe.FindAllStringSubmatch(text, -1) {
		out = append(out, words[strings.ToLower(m[1])])
	}
	return out
}

var hari = [...]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}

const reminderPrompt = `Kamu pengurai reminder. Ubah pesan user menjadi reminder. Balas HANYA 3 baris ini:
TEKS: <apa yang diingatkan, singkat, tanpa kata waktu dan tanpa kata "ingatkan">
WAKTU: <YYYY-MM-DD HH:MM, waktu ACARA UTAMA dalam WIB; jika jam tidak disebut pakai 09:00; "pagi"=08:00, "siang"=12:00, "sore"=16:00, "malam"=19:00; jika tidak ada waktu sama sekali tulis ->
ULANG: <tidak | harian | mingguan | bulanan | tahunan>

Aturan:
- ULANG hanya jika user menulis "tiap"/"setiap"/"rutin"; selain itu ULANG: tidak.
- Untuk "tiap senin" pakai Senin terdekat yang belum lewat dan ULANG: mingguan. Untuk "tiap tanggal 5" pakai tanggal 5 terdekat dan ULANG: bulanan.
- Abaikan pengingat awal seperti "H-30" atau "seminggu sebelumnya": WAKTU tetap waktu acara utama, bukan waktu pengingatnya.`

func (o *OpenAI) ParseReminder(ctx context.Context, text string, now time.Time) (ReminderDraft, error) {
	if d, ok := ParseRelative(text, now); ok {
		return d, nil
	}
	if !timeHint.MatchString(reminderVerb.ReplaceAllString(text, "")) {
		return ReminderDraft{}, ErrNoReminderTime
	}
	content, err := o.complete(ctx, reminderPrompt, nil, reminderContext(now)+"\n\nPESAN USER:\n"+Truncate(text, 1000))
	if err != nil {
		return ReminderDraft{}, err
	}
	d, err := parseReminderReply(content, now)
	if err != nil {
		return d, err
	}
	if !repeatHint.MatchString(text) {
		d.Repeat = ""
	}
	d.LeadMinutes = ParseLeads(text)
	return d, nil
}

// reminderContext memberi waktu sekarang + kalender 8 hari supaya model tidak salah hitung hari.
func reminderContext(now time.Time) string {
	now = now.In(WIB)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Sekarang: %s %s WIB.\nKalender:", hari[now.Weekday()], now.Format("2006-01-02 15:04"))
	for i := 0; i < 8; i++ {
		d := now.AddDate(0, 0, i)
		label := ""
		switch i {
		case 0:
			label = " (hari ini)"
		case 1:
			label = " (besok)"
		case 2:
			label = " (lusa)"
		}
		fmt.Fprintf(&sb, "\n- %s %s%s", hari[d.Weekday()], d.Format("2006-01-02"), label)
	}
	return sb.String()
}

var (
	lineRe = regexp.MustCompile(`(?im)^\s*\**\s*(teks|waktu|ulang)\s*\**\s*:\s*(.*?)\s*$`)
	// Model kadang menambah " WIB", detik, atau "T"; cukup ambil tanggal dan jam.
	dateRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})(?:[ T]+(\d{1,2})[:.](\d{2}))?`)
)

var repeatWords = map[string]string{
	"tidak": "", "-": "", "harian": "daily", "mingguan": "weekly", "bulanan": "monthly", "tahunan": "yearly",
}

func parseReminderReply(content string, now time.Time) (ReminderDraft, error) {
	fields := map[string]string{}
	for _, m := range lineRe.FindAllStringSubmatch(content, -1) {
		fields[strings.ToLower(m[1])] = strings.Trim(m[2], "`*\" ")
	}
	m := dateRe.FindStringSubmatch(fields["waktu"])
	if m == nil {
		return ReminderDraft{}, ErrNoReminderTime
	}
	clock := "09:00"
	if m[2] != "" {
		clock = fmt.Sprintf("%02s:%s", m[2], m[3])
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", m[1]+" "+clock, WIB)
	if err != nil {
		return ReminderDraft{}, fmt.Errorf("%w: %q", ErrNoReminderTime, fields["waktu"])
	}
	d := ReminderDraft{Text: cleanReminderText(fields["teks"]), At: at}
	d.Repeat = repeatWords[strings.ToLower(strings.Fields(fields["ulang"] + " -")[0])]
	return d, nil
}
