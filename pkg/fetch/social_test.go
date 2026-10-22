package fetch

import "testing"

func TestSocialCaptionBuangStatistik(t *testing.T) {
	got := socialCaption(`335 likes, 58 comments - ui.tayyab on June 3, 2026: "Recently designed a marketplace app"`)
	if got != "Recently designed a marketplace app" {
		t.Fatalf("%q", got)
	}
	if socialCaption("Teks biasa: tanpa statistik") != "Teks biasa: tanpa statistik" {
		t.Fatal("teks biasa tidak boleh dipotong")
	}
}

func TestSocialAuthor(t *testing.T) {
	if a := socialAuthor(". (@leykopin) di Threads"); a != "(@leykopin)" {
		t.Fatalf("%q", a)
	}
	if a := socialAuthor(`Tayyab on Instagram: "Recently"`); a != "Tayyab" {
		t.Fatalf("%q", a)
	}
	if socialAuthor("Judul biasa") != "" {
		t.Fatal("tanpa platform harus kosong")
	}
}

func TestGenericSocialTitle(t *testing.T) {
	for _, s := range []string{"Threads", "Instagram post", "Log in • Instagram", ""} {
		if !isGenericSocialTitle(s, map[bool]string{true: "Threads", false: "Instagram"}[s == "Threads"]) {
			t.Fatalf("harus generik: %q", s)
		}
	}
}
