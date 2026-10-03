package extract

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestDocx(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte(`<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>Halo</w:t></w:r><w:r><w:t xml:space="preserve"> Vaulty</w:t></w:r></w:p><w:p><w:r><w:t>baris dua</w:t></w:r></w:p></w:body></w:document>`))
	zw.Close()

	got, err := Text(buf.Bytes(), "", "catatan.docx")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Halo Vaulty\nbaris dua" {
		t.Fatalf("got %q", got)
	}
}

func TestTextAndUnknown(t *testing.T) {
	if got, _ := Text([]byte("SELECT 1;\x00"), "", "q.sql"); got != "SELECT 1;" {
		t.Fatalf("sql: got %q", got)
	}
	if got, _ := Text([]byte{0xff, 0xd8, 0xff}, "image/jpeg", "foto.jpg"); got != "" {
		t.Fatalf("jpg harus kosong, got %q", got)
	}
}

func TestCleanTruncates(t *testing.T) {
	if n := len([]rune(Clean(strings.Repeat("a", MaxChars+10)))); n != MaxChars {
		t.Fatalf("len = %d", n)
	}
	if got := Clean("ok\xffok"); got != "okok" {
		t.Fatalf("utf8: %q", got)
	}
}
