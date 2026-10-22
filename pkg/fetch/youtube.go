package fetch

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// captionTrack adalah satu track subtitle dari ytInitialPlayerResponse.
type captionTrack struct {
	BaseURL      string `json:"baseUrl"`
	LanguageCode string `json:"languageCode"`
	Kind         string `json:"kind"` // "asr" = otomatis
}

// playerInfo adalah hasil parse bagian yang kita butuhkan dari halaman watch.
type playerInfo struct {
	Description string
	Duration    int
	Tracks      []captionTrack
}

func (f *Fetcher) fetchYouTube(ctx context.Context, res *Result) error {
	res.ContentType = "video"
	res.SiteName = "YouTube"

	o, err := f.getOEmbed(ctx, f.youtubeOEmbed, url.Values{"url": {res.CanonicalURL}, "format": {"json"}})
	if err != nil {
		res.warn("oembed youtube gagal: %v", err)
	} else {
		res.Title = o.Title
		res.Author = o.AuthorName
		res.ThumbnailURL = o.ThumbnailURL
	}

	// Best-effort: halaman watch memberi deskripsi, durasi, dan daftar caption.
	// Struktur ytInitialPlayerResponse tidak resmi dan bisa berubah, jadi
	// semua kegagalan di sini hanya warning.
	p, err := f.getPage(ctx, res.CanonicalURL)
	if err != nil {
		res.warn("halaman youtube gagal: %v", err)
		return nil
	}
	if p.status >= 400 || !isHTML(p.contentType) {
		res.warn("halaman youtube status %d (%s)", p.status, p.contentType)
		return nil
	}
	if res.Title == "" || res.ThumbnailURL == "" {
		if m, perr := parseHTML(p.body, p.contentType, p.charset); perr == nil {
			applyMeta(res, m, p.finalURL)
		}
	}
	info, err := parsePlayerResponse(p.body)
	if err != nil {
		res.warn("parse ytInitialPlayerResponse gagal: %v", err)
		return nil
	}
	res.Description = info.Description
	res.Duration = info.Duration
	track := pickCaptionTrack(info.Tracks, "id", "en")
	if track == nil {
		res.warn("video tidak punya caption")
		return nil
	}
	if res.Language == "" {
		res.Language = langCode(track.LanguageCode)
	}
	transcript, err := f.downloadTranscript(ctx, track.BaseURL)
	if err != nil {
		res.warn("unduh caption gagal: %v", err)
		return nil
	}
	res.Transcript = transcript
	return nil
}

// parsePlayerResponse mencari `ytInitialPlayerResponse = {...}` di HTML
// dan mengambil field yang dibutuhkan. Objek JSON diambil dengan
// pencocokan kurung kurawal (sadar string) karena akhirnya tidak selalu `;`.
func parsePlayerResponse(body []byte) (*playerInfo, error) {
	const marker = "ytInitialPlayerResponse"
	idx := strings.Index(string(body), marker)
	if idx < 0 {
		return nil, errors.New("marker tidak ditemukan")
	}
	rest := body[idx+len(marker):]
	start := -1
	for i, c := range rest {
		if c == '{' {
			start = i
			break
		}
		// Hanya boleh ada whitespace dan '=' di antara marker dan objek;
		// kalau ada yang lain berarti marker ini bukan assignment.
		if c != ' ' && c != '=' && c != '\n' && c != '\t' && c != '\r' && c != ':' {
			return nil, errors.New("format assignment tidak dikenal")
		}
	}
	if start < 0 {
		return nil, errors.New("objek tidak ditemukan")
	}
	raw, ok := extractJSONObject(rest[start:])
	if !ok {
		return nil, errors.New("objek json tidak lengkap")
	}
	var pr struct {
		VideoDetails struct {
			ShortDescription string `json:"shortDescription"`
			LengthSeconds    string `json:"lengthSeconds"`
		} `json:"videoDetails"`
		Captions struct {
			Renderer struct {
				Tracks []captionTrack `json:"captionTracks"`
			} `json:"playerCaptionsTracklistRenderer"`
		} `json:"captions"`
	}
	if err := json.Unmarshal(raw, &pr); err != nil {
		return nil, err
	}
	info := &playerInfo{Description: pr.VideoDetails.ShortDescription, Tracks: pr.Captions.Renderer.Tracks}
	if n, err := strconv.Atoi(pr.VideoDetails.LengthSeconds); err == nil {
		info.Duration = n
	}
	return info, nil
}

// extractJSONObject mengembalikan byte objek JSON seimbang yang dimulai di b[0]=='{'.
func extractJSONObject(b []byte) ([]byte, bool) {
	depth := 0
	inStr := false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return b[:i+1], true
			}
		}
	}
	return nil, false
}

// pickCaptionTrack memilih track sesuai urutan bahasa prefer; dalam bahasa
// yang sama, caption manual didahulukan daripada ASR karena lebih akurat.
// Bila tidak ada yang cocok, pakai track pertama.
func pickCaptionTrack(tracks []captionTrack, prefer ...string) *captionTrack {
	if len(tracks) == 0 {
		return nil
	}
	for _, lang := range prefer {
		var asr *captionTrack
		for i := range tracks {
			t := &tracks[i]
			if langCode(t.LanguageCode) != lang || t.BaseURL == "" {
				continue
			}
			if t.Kind != "asr" {
				return t
			}
			if asr == nil {
				asr = t
			}
		}
		if asr != nil {
			return asr
		}
	}
	for i := range tracks {
		if tracks[i].BaseURL != "" {
			return &tracks[i]
		}
	}
	return nil
}

func (f *Fetcher) downloadTranscript(ctx context.Context, baseURL string) (string, error) {
	resp, err := f.do(ctx, baseURL, "text/xml,application/xml,*/*")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", errors.New("status " + strconv.Itoa(resp.StatusCode))
	}
	body, err := readLimited(resp.Body, 2<<20)
	if err != nil {
		return "", err
	}
	return parseTimedText(body)
}

// parseTimedText menggabungkan teks dari format timedtext YouTube. Dua
// bentuk didukung: <transcript><text ...>..</text></transcript> (default)
// dan srv3 <timedtext><body><p ...><s>..</s></p></body></timedtext>.
// Teks di dalamnya sering di-escape dua kali ("&amp;#39;"), karena itu
// di-unescape lagi setelah parser XML.
func parseTimedText(data []byte) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	dec.Strict = false
	var parts []string
	var cur strings.Builder
	depth := 0 // kedalaman di dalam <text>/<p>
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			parts = append(parts, html.UnescapeString(s))
		}
		cur.Reset()
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(parts) > 0 {
				break // ambil apa yang sudah ada
			}
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "text" || t.Name.Local == "p" {
				if depth == 0 {
					cur.Reset()
				}
				depth++
			} else if depth > 0 && t.Name.Local == "br" {
				cur.WriteByte(' ')
			}
		case xml.EndElement:
			if t.Name.Local == "text" || t.Name.Local == "p" {
				depth--
				if depth <= 0 {
					depth = 0
					flush()
				}
			}
		case xml.CharData:
			if depth > 0 {
				cur.Write(t)
			}
		}
	}
	if len(parts) == 0 {
		return "", errors.New("caption kosong")
	}
	return strings.Join(parts, " "), nil
}
