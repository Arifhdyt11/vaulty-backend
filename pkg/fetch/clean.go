package fetch

import "strings"

// Clean merapikan whitespace: CRLF → LF, spasi/tab beruntun → satu spasi,
// trim tiap baris, dan baris kosong beruntun → satu baris kosong.
// Dipakai sebelum teks masuk ke LLM/embedding supaya token tidak terbuang
// untuk whitespace dan hasil cache per canonical_url stabil.
func Clean(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
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
	return strings.TrimSpace(strings.Join(out, "\n"))
}
