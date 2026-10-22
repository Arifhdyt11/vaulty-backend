package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"vaulty/internal/model"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestBuildChunksRingkasanDanIsi(t *testing.T) {
	title, sum := "Judul", "Ringkasan singkat."
	long := strings.Repeat("Kalimat panjang untuk menguji pemotongan chunk. ", 120)
	it := model.Item{Title: &title, Summary: &sum, UserNote: "catatan", KeyPoints: []string{"a", "b"}, ContentText: &long}
	chunks := BuildChunks(it)
	if len(chunks) < 3 {
		t.Fatalf("chunk terlalu sedikit: %d", len(chunks))
	}
	if chunks[0].Source != "summary" || !strings.Contains(chunks[0].Text, "Ringkasan") {
		t.Fatalf("chunk pertama harus ringkasan: %+v", chunks[0])
	}
	for _, c := range chunks[1:] {
		if n := len([]rune(c.Text)); n > 950 {
			t.Fatalf("chunk terlalu panjang: %d", n)
		}
	}
}

func TestBuildChunksTeksPendek(t *testing.T) {
	it := model.Item{Body: "beli susu besok"}
	chunks := BuildChunks(it)
	if len(chunks) != 1 {
		t.Fatalf("teks pendek harus 1 chunk, dapat %d", len(chunks))
	}
}

func TestParseCitationsValidasiChunkID(t *testing.T) {
	hits := []model.ChunkHit{
		{Chunk: model.Chunk{ID: 10, ItemID: 1, Text: "resep ayam goreng dengan bawang putih"}, Item: model.Item{ID: 1}},
		{Chunk: model.Chunk{ID: 11, ItemID: 2, Text: "tips negosiasi gaji"}, Item: model.Item{ID: 2}},
	}
	content := "Bahannya ayam dan bawang putih.\nCITATIONS: [{\"chunk_id\": 10, \"quote\": \"ayam goreng\"}, {\"chunk_id\": 99, \"quote\": \"palsu\"}]"
	answer, cits := parseCitations(content, hits)
	if answer != "Bahannya ayam dan bawang putih." {
		t.Fatalf("jawaban salah: %q", answer)
	}
	if len(cits) != 1 || cits[0].ChunkID != 10 || cits[0].ItemID != 1 {
		t.Fatalf("citation harus hanya chunk 10: %+v", cits)
	}
}

func TestParseCitationsTanpaBaris(t *testing.T) {
	answer, cits := parseCitations("Tidak ada di simpananmu.", nil)
	if answer != "Tidak ada di simpananmu." || len(cits) != 0 {
		t.Fatalf("%q %+v", answer, cits)
	}
}

func TestParseCitationsMarkdownBold(t *testing.T) {
	hits := []model.ChunkHit{{Chunk: model.Chunk{ID: 5, ItemID: 3}, Item: model.Item{ID: 3}}}
	answer, cits := parseCitations("Jawab.\n**CITATIONS:** [{\"chunk_id\": \"5\"}]", hits)
	if answer != "Jawab." || len(cits) != 1 {
		t.Fatalf("%q %+v", answer, cits)
	}
}

func TestHoldbackTidakBocorkanMarker(t *testing.T) {
	var got strings.Builder
	h := &holdback{marker: "CITATIONS:", emit: func(s string) error { got.WriteString(s); return nil }}
	for _, d := range []string{"Jawaban ", "lengkap di ", "sini.\nCIT", "ATIONS: [{\"chunk_id\":1}]"} {
		if err := h.Write(d); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(got.String(), "CITATIONS") {
		t.Fatalf("marker bocor: %q", got.String())
	}
	if err := h.Flush("Jawaban lengkap di sini."); err != nil {
		t.Fatal(err)
	}
	if got.String() != "Jawaban lengkap di sini." {
		t.Fatalf("hasil akhir: %q", got.String())
	}
}

func TestNextNotifyBertahap(t *testing.T) {
	event := at("2026-12-01T09:00:00Z")
	now := at("2026-10-22T00:00:00Z")
	n, kind, ok := NextNotify(event, []int32{43200, 1440}, now) // H-30, H-1
	if !ok || kind != "lead" || !n.Equal(at("2026-11-01T09:00:00Z")) {
		t.Fatalf("H-30 salah: %v %s %v", n, kind, ok)
	}
	n, kind, _ = NextNotify(event, []int32{43200, 1440}, at("2026-11-15T00:00:00Z"))
	if kind != "lead" || !n.Equal(at("2026-11-30T09:00:00Z")) {
		t.Fatalf("H-1 salah: %v %s", n, kind)
	}
	n, kind, _ = NextNotify(event, []int32{43200, 1440}, at("2026-11-30T10:00:00Z"))
	if kind != "event" || !n.Equal(event) {
		t.Fatalf("event salah: %v %s", n, kind)
	}
}

func TestAfterSendBerulang(t *testing.T) {
	r := model.Reminder{RemindAt: at("2026-10-26T08:00:00Z"), Repeat: "weekly", NotifyKind: "event"}
	status, remindAt, notifyAt, kind := afterSend(r, at("2026-10-26T08:00:05Z"))
	if status != "pending" || kind != "event" || !remindAt.Equal(at("2026-11-02T08:00:00Z")) || !notifyAt.Equal(remindAt) {
		t.Fatalf("%s %v %v %s", status, remindAt, notifyAt, kind)
	}
	once := model.Reminder{RemindAt: at("2026-10-26T08:00:00Z"), NotifyKind: "event"}
	status, _, _, _ = afterSend(once, at("2026-10-26T08:00:05Z"))
	if status != "sent" {
		t.Fatalf("sekali: %s", status)
	}
}

func TestNextOccurrenceBulanClamp(t *testing.T) {
	got := NextOccurrence(at("2027-01-31T09:00:00Z"), "monthly")
	if got.Month() != time.February || got.Day() != 28 {
		t.Fatalf("31 Jan + 1 bulan = %v", got)
	}
}

func TestPeriodZonaWaktu(t *testing.T) {
	u := model.User{Timezone: "Asia/Jakarta"}
	key, reset := Period(u, "day", at("2026-10-22T20:00:00Z")) // 03:00 WIB 23 Okt
	if key != "2026-10-23" {
		t.Fatalf("period hari: %s", key)
	}
	if !reset.Equal(at("2026-10-23T17:00:00Z")) {
		t.Fatalf("reset: %v", reset)
	}
	key, _ = Period(u, "month", at("2026-10-31T18:00:00Z")) // 1 Nov WIB
	if key != "2026-11" {
		t.Fatalf("period bulan: %s", key)
	}
}

func TestSearchQueryBuangKataTanya(t *testing.T) {
	q := searchQuery("apa yang pernah saya simpan tentang resep ayam?")
	if q != "resep or ayam" {
		t.Fatalf("%q", q)
	}
}

func TestDetectSecret(t *testing.T) {
	if detectSecret("catatan biasa tentang resep") != "" {
		t.Fatal("false positive")
	}
	if detectSecret("api_key=abcd1234") == "" {
		t.Fatal("harus terdeteksi")
	}
}

func TestNormalizeTags(t *testing.T) {
	got := normalizeTags([]string{"#Resep", "resep", " Tips Kerja ", ""})
	if len(got) != 2 || got[0] != "resep" || got[1] != "tips-kerja" {
		t.Fatalf("%v", got)
	}
}

func TestFinalStatus(t *testing.T) {
	ok := model.Item{Ingest: model.Ingest{Fetch: model.Stage{Status: "done"}, Transcribe: model.Stage{Status: "skipped"}, Understand: model.Stage{Status: "done"}, Index: model.Stage{Status: "done"}}}
	if finalStatus(ok) != "ready" {
		t.Fatal("ready")
	}
	partial := ok
	partial.Ingest.Understand.Status = "failed"
	if finalStatus(partial) != "partial" {
		t.Fatal("partial")
	}
	failed := model.Item{Kind: "url", Ingest: model.Ingest{Fetch: model.Stage{Status: "failed"}, Transcribe: model.Stage{Status: "skipped"}, Understand: model.Stage{Status: "skipped"}, Index: model.Stage{Status: "skipped"}}}
	// URL tetap tersimpan walau fetch gagal → partial (app menawarkan screenshot).
	if finalStatus(failed) != "partial" {
		t.Fatal("partial")
	}
}

func TestTranscribeVideoFreeDilewati(t *testing.T) {
	s := &IngestService{aiOn: true}
	src := "https://www.youtube.com/watch?v=jNQXAC9IVRw"
	platform := "youtube"
	st := s.transcribeVideo(context.Background(), model.User{Plan: "free"}, &model.Item{Kind: "url", SourceURL: &src, Platform: &platform})
	if st.Status != "skipped" || !strings.Contains(st.Error, "Pro") {
		t.Fatalf("%+v", st)
	}
}

func TestTranscribeVideoTanpaAlatDilewati(t *testing.T) {
	s := &IngestService{aiOn: true, maxVideoMin: 30}
	src := "https://vt.tiktok.com/x"
	platform := "tiktok"
	st := s.transcribeVideo(context.Background(), model.User{Plan: "pro"}, &model.Item{Kind: "url", SourceURL: &src, Platform: &platform})
	if st.Status != "skipped" || !strings.Contains(st.Error, "yt-dlp") {
		t.Fatalf("%+v", st)
	}
}
