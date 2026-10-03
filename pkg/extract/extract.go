// Package extract mengambil teks dari file upload supaya dokumen bisa dicari.
package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// MaxChars membatasi teks yang disimpan (batas tsvector Postgres 1MB).
const MaxChars = 200_000

// Text mengembalikan teks dari file. Format yang tidak dikenali menghasilkan "" tanpa error,
// karena dokumen tetap bisa dicari lewat judul, deskripsi, dan nama file.
func Text(data []byte, mime, filename string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	var (
		text string
		err  error
	)
	switch {
	case ext == ".pdf" || mime == "application/pdf":
		text, err = pdfText(data)
	case ext == ".docx":
		text, err = docxText(data)
	case isText(mime, ext):
		text = string(data)
	default:
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return Clean(text), nil
}

var textExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".tsv": true, ".json": true,
	".yaml": true, ".yml": true, ".toml": true, ".xml": true, ".html": true, ".htm": true,
	".sql": true, ".sh": true, ".bash": true, ".zsh": true, ".env.example": true, ".ini": true,
	".conf": true, ".log": true, ".go": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true,
	".py": true, ".php": true, ".rb": true, ".java": true, ".kt": true, ".swift": true, ".rs": true,
	".c": true, ".h": true, ".cpp": true, ".cs": true, ".css": true, ".scss": true, ".vue": true,
	".dockerfile": true, ".tf": true,
}

func isText(mime, ext string) bool {
	return strings.HasPrefix(mime, "text/") || textExts[ext] ||
		mime == "application/json" || mime == "application/xml"
}

func pdfText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	plain, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(plain, MaxChars*4))
	return string(b), err
}

// docxText membaca word/document.xml dan mengambil isi elemen <w:t>.
func docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		var sb strings.Builder
		dec := xml.NewDecoder(io.LimitReader(rc, 50<<20))
		inText := false
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				inText = t.Name.Local == "t"
				if t.Name.Local == "tab" {
					sb.WriteByte('\t')
				}
			case xml.EndElement:
				switch t.Name.Local {
				case "t":
					inText = false
				case "p":
					sb.WriteByte('\n')
				}
			case xml.CharData:
				if inText {
					sb.Write(t)
				}
			}
		}
		return sb.String(), nil
	}
	return "", nil
}

// Clean membuang karakter yang ditolak Postgres (NUL, UTF-8 rusak) dan memotong ke MaxChars.
func Clean(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.ReplaceAll(s, "\x00", "")
	if utf8.RuneCountInString(s) > MaxChars {
		s = string([]rune(s)[:MaxChars])
	}
	return strings.TrimSpace(s)
}
