package extract

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func makeDocx(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"word/document.xml":   documentXML,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocx(t *testing.T) {
	doc := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>Judul </w:t></w:r><w:r><w:t xml:space="preserve">Dokumen</w:t></w:r></w:p>
<w:p><w:r><w:t>Baris</w:t><w:tab/><w:t>kedua</w:t></w:r></w:p>
<w:p/>
<w:p><w:r><w:t>Paragraf ketiga</w:t></w:r></w:p>
</w:body></w:document>`
	got, err := Text(makeDocx(t, doc), "application/octet-stream", "laporan.docx")
	if err != nil {
		t.Fatal(err)
	}
	want := "Judul Dokumen\nBaris kedua\n\nParagraf ketiga"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if _, err := Text([]byte("bukan zip"), "", "x.docx"); err == nil {
		t.Error("docx rusak harus error")
	}
}

func TestPlainText(t *testing.T) {
	got, err := Text([]byte("\xEF\xBB\xBFhalo\r\n\r\n\r\ndunia  \x00ok"), "text/plain", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got != "halo\n\ndunia ok" {
		t.Errorf("got %q", got)
	}
	// Ekstensi kode dikenali walau mime generik.
	if got, err := Text([]byte("package main"), "application/octet-stream", "main.go"); err != nil || got != "package main" {
		t.Errorf("go: %q %v", got, err)
	}
	if got, err := Text([]byte(`{"a":1}`), "application/json", ""); err != nil || got != `{"a":1}` {
		t.Errorf("json: %q %v", got, err)
	}
}

func TestUnsupported(t *testing.T) {
	if _, err := Text([]byte{0xFF, 0xD8}, "image/jpeg", "foto.jpg"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("want ErrUnsupported, got %v", err)
	}
	if IsSupported("image/png", "a.png") || IsSupported("application/octet-stream", "a.bin") {
		t.Error("biner tidak boleh didukung")
	}
	if !IsSupported("application/pdf", "") || !IsSupported("", "x.PDF") || !IsSupported("", "notes.md") || !IsSupported("text/csv", "") {
		t.Error("tipe teks harus didukung")
	}
}

func TestPDFBroken(t *testing.T) {
	if _, err := Text([]byte("%PDF-1.4 rusak"), "application/pdf", "a.pdf"); err == nil {
		t.Error("pdf rusak harus error, bukan panic")
	}
}

func TestCleanTruncates(t *testing.T) {
	long := strings.Repeat("é", MaxChars+500)
	got := Clean(long)
	if utf8.RuneCountInString(got) != MaxChars || !utf8.ValidString(got) {
		t.Errorf("len=%d valid=%v", utf8.RuneCountInString(got), utf8.ValidString(got))
	}
	if got := Clean("a" + string(rune(0x200B)) + "b" + string(rune(0x00A0)) + "c\td"); got != "ab c d" {
		t.Errorf("got %q", got)
	}
}
