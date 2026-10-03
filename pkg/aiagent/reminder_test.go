package aiagent

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 4, 10, 15, 30, 0, WIB) // Minggu

func TestParseRelative(t *testing.T) {
	cases := []struct {
		in, text string
		want     time.Time
	}{
		{"ingatkan 30 menit lagi angkat jemuran", "angkat jemuran", time.Date(2026, 10, 4, 10, 45, 0, 0, WIB)},
		{"/ingatkan aku untuk minum obat 2 jam lagi", "minum obat", time.Date(2026, 10, 4, 12, 15, 0, 0, WIB)},
		{"remind cek deploy 3 hari lagi", "cek deploy", time.Date(2026, 10, 7, 10, 15, 0, 0, WIB)},
	}
	for _, c := range cases {
		d, ok := ParseRelative(c.in, now)
		if !ok || d.Text != c.text || !d.At.Equal(c.want) {
			t.Errorf("ParseRelative(%q) = %+v, %v; want %q %s", c.in, d, ok, c.text, c.want)
		}
	}
	if _, ok := ParseRelative("ingatkan besok jam 9 follow up", now); ok {
		t.Error("waktu absolut harus diserahkan ke LLM")
	}
}

func TestParseReminderReply(t *testing.T) {
	got, err := parseReminderReply("TEKS: perpanjang server sipantas\nWAKTU: 2027-08-01 09:00 WIB\nULANG: tahunan", now)
	want := ReminderDraft{Text: "perpanjang server sipantas", At: time.Date(2027, 8, 1, 9, 0, 0, 0, WIB), Repeat: "yearly"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("parse = %+v, %v; want %+v", got, err, want)
	}
	got, err = parseReminderReply("**TEKS:** ingatkan follow up client X besok jam 9\nWAKTU: `2026-10-05`\nULANG: tidak", now)
	if err != nil || got.Text != "follow up client X" || !got.At.Equal(time.Date(2026, 10, 5, 9, 0, 0, 0, WIB)) || got.Repeat != "" {
		t.Errorf("parse tanpa jam = %+v, %v", got, err)
	}
	got, _ = parseReminderReply("TEKS: x\nWAKTU: 2026-10-05T8.30", now)
	if !got.At.Equal(time.Date(2026, 10, 5, 8, 30, 0, 0, WIB)) {
		t.Errorf("parse format T = %s", got.At)
	}
	for _, bad := range []string{"TEKS: x\nWAKTU: -", "TEKS: x\nWAKTU: ->", "TEKS: x\nWAKTU: besok", "maaf saya tidak paham"} {
		if _, err := parseReminderReply(bad, now); err == nil {
			t.Errorf("parse(%q) harus error", bad)
		}
	}
}

func TestParseLeads(t *testing.T) {
	cases := map[string][]int{
		"1 agustus 2027 perpanjang server, ingatkan juga H-30 dan H-7": {43200, 10080},
		"meeting jumat, ingatkan 2 jam sebelumnya":                     {120},
		"bayar pajak 1 des, seminggu sebelum dan sehari sebelumnya":    {10080, 1440},
		"besok jam 9 follow up":                                        nil,
	}
	for in, want := range cases {
		if got := ParseLeads(in); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseLeads(%q) = %v; want %v", in, got, want)
		}
	}
}

func TestCleanReminderText(t *testing.T) {
	cases := map[string]string{
		"bayar listrik tiap tanggal 25":                 "bayar listrik",
		"lusa sore meeting sama tim":                    "meeting sama tim",
		"perpanjang server, ingatkan juga H-30 dan H-7": "perpanjang server",
		"makan siang dengan klien jam 12":               "makan siang dengan klien",
		"demo ke klien, 1 jam sebelumnya juga":          "demo ke klien",
	}
	for in, want := range cases {
		if got := cleanReminderText(in); got != want {
			t.Errorf("clean(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestReminderContext(t *testing.T) {
	c := reminderContext(now)
	if !strings.Contains(c, "Sekarang: Minggu 2026-10-04 10:15 WIB") || !strings.Contains(c, "- Senin 2026-10-05 (besok)") ||
		!strings.Contains(c, "- Minggu 2026-10-11") {
		t.Errorf("context = %s", c)
	}
}

func TestTimeHint(t *testing.T) {
	for _, s := range []string{"besok follow up", "tiap senin cek backup", "1 agustus perpanjang", "nanti malam telpon"} {
		if !timeHint.MatchString(s) {
			t.Errorf("%q harus terdeteksi punya waktu", s)
		}
	}
	for _, s := range []string{"follow up client", "cek backup server"} {
		if timeHint.MatchString(s) {
			t.Errorf("%q tidak punya petunjuk waktu", s)
		}
	}
}
