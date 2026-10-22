// Package extract mengambil teks polos dari file yang diunggah user
// (PDF, DOCX, teks/markdown/kode) supaya bisa diringkas dan diindeks.
//
// Paket ini sengaja tidak menangani gambar/audio: itu jalur vision/STT di
// worker lain. Fungsi di sini murni (tanpa I/O), jadi mudah dites.
package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// MaxChars adalah batas panjang hasil Clean. 200k karakter sudah jauh di
// atas kebutuhan ringkasan dan mencegah dokumen raksasa membebani DB/LLM.
const MaxChars = 200_000

var ErrUnsupported = errors.New("tipe file tidak didukung")

// Rune khusus ditulis sebagai konstanta numerik supaya tidak bergantung
// pada editor/shell yang mungkin mengubah escape \uXXXX.
const (
	nbsp rune = 0x00A0 // non-breaking space
	bom  rune = 0xFEFF // byte order mark
	zwj  rune = 0x200D // zero-width joiner (emoji gabungan)
)

// Ekstensi yang diperlakukan sebagai teks polos. Kode sumber dimasukkan
// karena developer sering menyimpan snippet/gist.
var textExt = map[string]bool{
	".txt": true, ".text": true, ".md": true, ".markdown": true, ".mdx": true, ".rst": true,
	".json": true, ".jsonl": true, ".csv": true, ".tsv": true, ".xml": true, ".yaml": true, ".yml": true,
	".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".log": true, ".env": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".svg": true,
	".go": true, ".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".jsx": true,
	".java": true, ".kt": true, ".kts": true, ".swift": true, ".dart": true, ".rb": true, ".rs": true,
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".hpp": true, ".cs": true, ".php": true,
	".sh": true, ".bash": true, ".zsh": true, ".ps1": true, ".sql": true, ".lua": true, ".r": true,
	".scala": true, ".ex": true, ".exs": true, ".erl": true, ".hs": true, ".clj": true, ".vue": true,
	".svelte": true, ".graphql": true, ".proto": true, ".tf": true, ".dockerfile": true, ".makefile": true,
}

var textMime = map[string]bool{
	"application/json": true, "application/ld+json": true, "application/xml": true,
	"application/x-yaml": true, "application/yaml": true, "application/toml": true,
	"application/javascript": true, "application/x-javascript": true, "application/typescript": true,
	"application/x-sh": true, "application/x-httpd-php": true, "application/sql": true,
	"application/csv": true, "application/x-ndjson": true, "image/svg+xml": true,
}

const (
	mimePDF  = "application/pdf"
	mimeDocx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
)

type kind int

const (
	kindUnsupported kind = iota
	kindPDF
	kindDocx
	kindText
)

// detect menggabungkan MIME dan ekstensi. Ekstensi tetap dicek karena klien
// mobile sering mengirim "application/octet-stream" untuk file apa pun.
func detect(mimeType, filename string) kind {
	mt := strings.ToLower(strings.TrimSpace(mimeType))
	if parsed, _, err := mime.ParseMediaType(mt); err == nil {
		mt = parsed
	}
	ext := strings.ToLower(path.Ext(filename))
	switch {
	case mt == mimePDF || ext == ".pdf":
		return kindPDF
	case mt == mimeDocx || ext == ".docx":
		return kindDocx
	case strings.HasPrefix(mt, "text/") || textMime[mt] || textExt[ext]:
		return kindText
	case ext == "" && strings.EqualFold(path.Base(filename), "Dockerfile"), strings.EqualFold(path.Base(filename), "Makefile"):
		return kindText
	}
	return kindUnsupported
}

// IsSupported melaporkan apakah Text bisa menangani kombinasi mime/nama ini.
func IsSupported(mimeType, filename string) bool {
	return detect(mimeType, filename) != kindUnsupported
}

// Text mengekstrak teks dari data. Hasil sudah melalui Clean.
// PDF hasil scan (tanpa layer teks) mengembalikan "" tanpa error; pemanggil
// yang memutuskan apakah perlu OCR.
func Text(data []byte, mimeType, filename string) (string, error) {
	var (
		out string
		err error
	)
	switch detect(mimeType, filename) {
	case kindPDF:
		out, err = fromPDF(data)
	case kindDocx:
		out, err = fromDocx(data)
	case kindText:
		out = fromText(data)
	default:
		return "", ErrUnsupported
	}
	if err != nil {
		return "", err
	}
	return Clean(out), nil
}

// fromPDF memakai ledongthuc/pdf per halaman. Library ini panik pada PDF
// rusak/terenkripsi, jadi dibungkus recover agar worker tidak mati.
func fromPDF(data []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("pdf rusak: %v", r)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("pdf: %w", err)
	}
	var b strings.Builder
	fonts := map[string]*pdf.Font{}
	n := r.NumPage()
	for i := 1; i <= n; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		pageText, perr := p.GetPlainText(fonts)
		if perr != nil {
			// Satu halaman gagal jangan membatalkan sisanya.
			continue
		}
		b.WriteString(pageText)
		b.WriteString("\n\n")
		if b.Len() > MaxChars*2 {
			break // sisanya toh akan dipotong Clean
		}
	}
	return b.String(), nil
}

// fromDocx membaca word/document.xml dari zip dan menyusun paragraf.
// Hanya <w:t> yang diambil; <w:p> jadi newline, <w:tab> jadi tab, <w:br>
// jadi newline. Header/footer/catatan kaki diabaikan karena jarang berisi
// konten utama.
func fromDocx(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("docx bukan zip: %w", err)
	}
	var docXML io.ReadCloser
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			docXML, err = f.Open()
			if err != nil {
				return "", fmt.Errorf("docx: %w", err)
			}
			break
		}
	}
	if docXML == nil {
		return "", errors.New("docx tanpa word/document.xml")
	}
	defer docXML.Close()

	dec := xml.NewDecoder(io.LimitReader(docXML, 64<<20))
	var b strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if b.Len() > 0 {
				break
			}
			return "", fmt.Errorf("docx xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				b.WriteByte('\t')
			case "br", "cr":
				b.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				b.WriteByte('\n')
			case "tc": // sel tabel dipisah tab supaya baris tabel tetap terbaca
				b.WriteByte('\t')
			}
		case xml.CharData:
			if inText {
				b.Write(t)
			}
		}
	}
	return b.String(), nil
}

// fromText menerima byte apa adanya, membuang BOM, dan mengganti byte
// non-UTF-8 supaya hasilnya aman disimpan di kolom text Postgres.
func fromText(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return string(data)
	}
	return strings.ToValidUTF8(string(data), string(utf8.RuneError))
}

// Clean menormalkan whitespace, membuang karakter kontrol (kecuali newline
// dan tab), merapatkan baris kosong beruntun, lalu memotong ke MaxChars.
func Clean(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == nbsp:
			return ' '
		case r == bom:
			return -1
		case unicode.IsControl(r) || (unicode.Is(unicode.Cf, r) && r != zwj):
			// Cf (format chars, mis. zero-width space) dibuang karena hanya
			// mengacaukan tokenisasi; ZWJ dipertahankan untuk emoji gabungan.
			return -1
		}
		return r
	}, s)

	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	s = strings.TrimSpace(strings.Join(out, "\n"))

	// Potong di batas rune, bukan byte, supaya tidak menghasilkan UTF-8 cacat.
	if utf8.RuneCountInString(s) > MaxChars {
		n := 0
		for i := range s {
			if n == MaxChars {
				s = s[:i]
				break
			}
			n++
		}
	}
	return s
}
