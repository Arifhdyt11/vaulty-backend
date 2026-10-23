package service

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"vaulty/internal/model"
	"vaulty/internal/repository"
)

func sp(s string) *string { return &s }

func TestPilihBabItemBaruDuluLaluItemLama(t *testing.T) {
	now := at("2026-10-23T08:00:00+07:00")
	cands := []repository.BriefingCandidate{
		{ID: 1, CreatedAt: now.Add(-2 * time.Hour)},                       // baru, tanpa ringkasan tetap boleh
		{ID: 2, CreatedAt: now.Add(-20 * time.Hour), HasSummary: true},    // baru
		{ID: 3, CreatedAt: now.Add(-5 * time.Hour), HasSummary: true},     // baru tapi sudah pernah jadi bab
		{ID: 4, CreatedAt: now.AddDate(0, 0, -30), HasSummary: true},      // lama, tampil 20 hari lalu → boleh
		{ID: 5, CreatedAt: now.AddDate(0, 0, -30), HasSummary: true},      // lama, tampil 3 hari lalu → tidak
		{ID: 6, CreatedAt: now.AddDate(0, 0, -10), HasSummary: false},     // lama tanpa ringkasan → tidak
		{ID: 7, CreatedAt: now.AddDate(0, 0, -60), HasSummary: true},      // lama, belum pernah tampil
		{ID: 8, CreatedAt: now.Add(-25 * time.Hour), HasSummary: true},    // lewat 24 jam → masuk kelompok lama
		{ID: 9, CreatedAt: now.AddDate(0, 0, -40), HasSummary: true},      // tampil tepat 14 hari lalu → boleh
		{ID: 10, CreatedAt: now.AddDate(0, 0, -40), HasSummary: true},     // tampil 13 hari lalu → tidak
		{ID: 11, CreatedAt: now.Add(-30 * time.Minute), HasSummary: true}, // baru, paling baru
		{ID: 12, CreatedAt: now.Add(-23 * time.Hour), HasSummary: false},  // baru tanpa ringkasan
		{ID: 13, CreatedAt: now.AddDate(0, 0, -100), HasSummary: true},    // lama
		{ID: 14, CreatedAt: now.AddDate(0, 0, -200), HasSummary: true},    // lama
		{ID: 15, CreatedAt: now.AddDate(0, 0, -300), HasSummary: true},    // lama
	}
	shown := map[int64]string{3: "2026-10-22", 4: "2026-10-03", 5: "2026-10-20", 9: "2026-10-09", 10: "2026-10-10"}
	date := "2026-10-23"

	got := pickBriefingItems(cands, shown, now, date, 3)
	if !reflect.DeepEqual(got, []int64{11, 1, 2}) {
		t.Fatalf("item baru harus didahulukan, terbaru dulu: %v", got)
	}

	all := pickBriefingItems(cands, shown, now, date, 50)
	if !reflect.DeepEqual(all[:4], []int64{11, 1, 2, 12}) {
		t.Fatalf("empat bab awal harus item baru: %v", all)
	}
	allowedOld := map[int64]bool{4: true, 7: true, 8: true, 9: true, 13: true, 14: true, 15: true}
	if len(all) != 4+len(allowedOld) {
		t.Fatalf("jumlah bab salah: %v", all)
	}
	for _, id := range all[4:] {
		if !allowedOld[id] {
			t.Fatalf("item %d tidak boleh terpilih: %v", id, all)
		}
	}

	// Deterministik per tanggal: GET berulang hari yang sama memberi urutan sama.
	if again := pickBriefingItems(cands, shown, now, date, 50); !reflect.DeepEqual(again, all) {
		t.Fatalf("urutan harus stabil: %v vs %v", again, all)
	}
	// Tanggal lain (tanpa item baru) → urutan item lama berubah untuk minimal satu dari beberapa hari.
	old := []repository.BriefingCandidate{}
	for _, c := range cands {
		if allowedOld[c.ID] {
			old = append(old, c)
		}
	}
	base := pickBriefingItems(old, shown, now, date, 7)
	changed := false
	for d := 1; d <= 5 && !changed; d++ {
		next := now.AddDate(0, 0, d)
		if !reflect.DeepEqual(pickBriefingItems(old, shown, next, next.Format(dateLayout), 7), base) {
			changed = true
		}
	}
	if !changed {
		t.Fatal("urutan item lama harus berganti antar tanggal")
	}

	if got := pickBriefingItems(nil, nil, now, date, 5); len(got) != 0 {
		t.Fatalf("tanpa item harus kosong: %v", got)
	}
}

func TestJumlahBabSesuaiPlan(t *testing.T) {
	if RulesFor("free").BriefingChapters != 3 || RulesFor("pro").BriefingChapters != 5 {
		t.Fatal("Free 3 bab, Pro 5 bab")
	}
}

func TestLabelBab(t *testing.T) {
	yt, th := "youtube", "threads"
	cases := []struct {
		it   model.Item
		want string
	}{
		{model.Item{Kind: "url", Platform: &yt, Tags: []string{"desain-ui"}, AutoTags: []string{"produktivitas"}}, "Desain ui"},
		{model.Item{Kind: "url", Platform: &yt, AutoTags: []string{"resep"}}, "Resep"},
		{model.Item{Kind: "url", Platform: &yt}, "YouTube"},
		{model.Item{Kind: "url", Platform: &yt, AutoTags: []string{"youtube", "sejarah"}}, "YouTube"},
		{model.Item{Kind: "url", Platform: &th, Tags: []string{"  "}}, "Threads"},
		{model.Item{Kind: "url"}, "Web"},
		{model.Item{Kind: "text"}, "Catatan"},
		{model.Item{Kind: "image"}, "Gambar"},
	}
	for _, c := range cases {
		if got := briefingLabel(c.it); got != c.want {
			t.Errorf("label %+v = %q, mau %q", c.it.Tags, got, c.want)
		}
	}
	for p, want := range map[string]string{"tiktok": "TikTok", "instagram": "Instagram", "web": "Web"} {
		p := p
		if got := briefingLabel(model.Item{Kind: "url", Platform: &p}); got != want {
			t.Errorf("platform %s = %q", p, got)
		}
	}
}

func TestKutipanBab(t *testing.T) {
	long := strings.Repeat("poin yang sangat panjang sekali ", 5)
	it := model.Item{KeyPoints: []string{long, "12 widget iOS yang rapi dan minimal."}}
	if got := briefingQuote(it); got != "12 widget iOS yang rapi dan minimal" {
		t.Fatalf("pakai poin pertama yang muat: %q", got)
	}
	it = model.Item{KeyPoints: []string{long}, Summary: sp("Video ini membahas cara menata meja kerja minimalis supaya fokus seharian tanpa gangguan visual yang berlebihan. Kalimat kedua.")}
	got := briefingQuote(it)
	if n := len([]rune(got)); n > 90 || !strings.HasPrefix(got, "Video ini membahas") || !strings.HasSuffix(got, "…") {
		t.Fatalf("potongan ringkasan: %q (%d)", got, n)
	}
	if got := briefingQuote(model.Item{Summary: sp("Ringkas saja. Lalu lain.")}); got != "Ringkas saja" {
		t.Fatalf("kalimat pertama: %q", got)
	}
	if got := briefingQuote(model.Item{Body: "beli susu besok"}); got != "beli susu besok" {
		t.Fatalf("catatan: %q", got)
	}
}

func TestSalamPerJam(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	cases := map[int]string{5: "pagi", 10: "pagi", 11: "siang", 14: "siang", 15: "sore", 17: "sore", 18: "malam", 23: "malam", 2: "malam"}
	for h, part := range cases {
		now := time.Date(2026, 10, 23, h, 30, 0, 0, loc)
		if got := briefingGreeting(now, "Arif Hidayat"); got != "Selamat "+part+", Arif" {
			t.Errorf("jam %d: %q", h, got)
		}
	}
	if got := briefingGreeting(time.Date(2026, 10, 23, 8, 0, 0, 0, loc), ""); got != "Selamat pagi" {
		t.Errorf("tanpa nama: %q", got)
	}
	if got := briefingOpening("Selamat pagi, Arif"); got != "Selamat pagi, Arif! Ini siaran Vee hari ini." {
		t.Errorf("pembuka: %q", got)
	}
}

func TestWaktuDisimpanRelatif(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	now := time.Date(2026, 10, 23, 8, 0, 0, 0, loc)
	cases := []struct {
		created time.Time
		want    string
	}{
		{now.Add(-time.Hour), "hari ini"},
		{time.Date(2026, 10, 22, 23, 50, 0, 0, loc), "kemarin"}, // beda tanggal lokal walau < 24 jam
		{now.AddDate(0, 0, -2), "kemarin lusa"},
		{now.AddDate(0, 0, -4), "empat hari lalu"},
		{now.AddDate(0, 0, -8), "seminggu lalu"},
		{now.AddDate(0, 0, -15), "dua minggu lalu"},
		{now.AddDate(0, 0, -40), "sebulan lalu"},
		{now.AddDate(0, 0, -95), "tiga bulan lalu"},
		{now.AddDate(-1, 0, -10), "setahun lalu"},
	}
	for _, c := range cases {
		if got := relativeSaved(c.created.UTC(), now, loc); got != c.want {
			t.Errorf("%s: %q, mau %q", c.created, got, c.want)
		}
	}
}

func TestBahanNaskahBerisiInfoYangBenar(t *testing.T) {
	tt := "tiktok"
	content := strings.Repeat("x", 3000)
	it := model.Item{Kind: "url", Platform: &tt, Title: sp("Widget Apps untuk produktivitas"), UserNote: "buat setup HP baru",
		Summary: sp("Kumpulan widget iOS minimalis."), KeyPoints: []string{"12 widget iOS", "gratis semua"}, ContentText: &content}
	in := briefingScriptInput(it, "Selamat pagi, Arif! Ini siaran Vee hari ini.", "kemarin")
	for _, want := range []string{"PEMBUKA: Selamat pagi, Arif! Ini siaran Vee hari ini.", "DISIMPAN: kemarin", "JENIS: video TikTok",
		"JUDUL: Widget Apps untuk produktivitas", "ALASAN USER: buat setup HP baru", "RINGKASAN: Kumpulan widget iOS minimalis.",
		"- 12 widget iOS", "- gratis semua", "AWAL ISI:"} {
		if !strings.Contains(in, want) {
			t.Errorf("bahan naskah tidak memuat %q", want)
		}
	}
	if strings.Count(in, "x") > 1501 {
		t.Errorf("isi harus dipotong ≤ 1500 karakter")
	}
	// Bab selain pertama: tanpa pembuka; tanpa catatan user: tanpa ALASAN USER.
	in2 := briefingScriptInput(model.Item{Kind: "text", Body: "beli susu"}, "", "dua minggu lalu")
	if strings.Contains(in2, "PEMBUKA") || strings.Contains(in2, "ALASAN USER") || !strings.Contains(in2, "catatan yang kamu tulis") || !strings.Contains(in2, "AWAL ISI:\nbeli susu") {
		t.Errorf("bahan naskah catatan salah: %q", in2)
	}
	for _, rule := range []string{"45-75 kata", "tanpa markdown, emoji, daftar", "DISIMPAN", "ALASAN USER", "PEMBUKA"} {
		if !strings.Contains(briefingScriptPrompt, rule) {
			t.Errorf("prompt tidak memuat aturan %q", rule)
		}
	}
}

func TestBersihkanNaskahDanPembuka(t *testing.T) {
	raw := "**Naskah:**\n- Kemarin kamu menyimpan video 🎬 TikTok tentang `widget`.\n# Coba pasang satu malam ini! ✨"
	got := cleanScript(raw)
	if got != "Kemarin kamu menyimpan video TikTok tentang widget. Coba pasang satu malam ini!" {
		t.Fatalf("bersih: %q", got)
	}
	opening := "Selamat pagi, Arif! Ini siaran Vee hari ini."
	if got := withOpening("Kemarin kamu menyimpan…", opening); !strings.HasPrefix(got, opening) {
		t.Fatalf("pembuka harus ditambahkan: %q", got)
	}
	if got := withOpening("Selamat pagi Arif! Ini siaran Vee. Kemarin…", opening); strings.Count(got, "Selamat pagi") != 1 {
		t.Fatalf("pembuka tidak boleh ganda: %q", got)
	}
	fb := fallbackScript(model.Item{Kind: "text", Body: "beli susu", Title: sp("Belanja"), UserNote: "penting"}, opening, "kemarin")
	if !strings.HasPrefix(fb, opening+" Kemarin kamu menyimpan catatan") || !strings.Contains(fb, "penting") {
		t.Fatalf("naskah cadangan: %q", fb)
	}
}
