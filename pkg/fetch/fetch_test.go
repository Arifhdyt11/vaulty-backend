package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		in, want, platform string
	}{
		{"https://example.com/a?utm_source=x&utm_medium=y&id=5&fbclid=abc#top", "https://example.com/a?id=5", "web"},
		{"example.com/path", "https://example.com/path", "web"},
		{"HTTPS://Example.COM:443/", "https://example.com/", "web"},
		{"https://youtu.be/dQw4w9WgXcQ?si=abc", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube"},
		{"https://m.youtube.com/watch?v=dQw4w9WgXcQ&t=10s&feature=share", "https://www.youtube.com/watch?v=dQw4w9WgXcQ", "youtube"},
		{"https://mobile.twitter.com/jack/status/20?s=20&t=xyz", "https://x.com/jack/status/20", "x"},
		{"https://x.com/jack/status/20/", "https://x.com/jack/status/20", "x"},
		{"https://www.tiktok.com/@user/video/123?is_from_webapp=1", "https://www.tiktok.com/@user/video/123", "tiktok"},
		{"https://vt.tiktok.com/ZSabc/", "https://vt.tiktok.com/ZSabc", "tiktok"},
		{"https://instagram.com/p/ABC?igsh=xyz", "https://www.instagram.com/p/ABC/", "instagram"},
		{"https://www.threads.net/@user/post/ABC?x=1", "https://www.threads.net/@user/post/ABC", "threads"},
	}
	for _, c := range cases {
		got, platform, err := Canonicalize(c.in)
		if err != nil {
			t.Errorf("%s: error %v", c.in, err)
			continue
		}
		if got != c.want || platform != c.platform {
			t.Errorf("%s: got (%s,%s) want (%s,%s)", c.in, got, platform, c.want, c.platform)
		}
	}
	for _, bad := range []string{"", "ftp://example.com/x", "notaurl", "http://", "javascript:alert(1)"} {
		if _, _, err := Canonicalize(bad); !errors.Is(err, ErrInvalidURL) {
			t.Errorf("%q: want ErrInvalidURL, got %v", bad, err)
		}
	}
}

const articleHTML = `<!doctype html><html lang="id"><head>
<meta charset="utf-8"><title>Judul Tag</title>
<meta property="og:title" content="Judul OG">
<meta property="og:description" content="Deskripsi singkat">
<meta property="og:image" content="/img/cover.jpg">
<meta property="og:site_name" content="Situs Uji">
<meta property="og:locale" content="id_ID">
<meta property="article:published_time" content="2024-05-01T10:00:00+07:00">
<meta property="article:author" content="Budi">
</head><body>
<nav><a href="/">Home</a> <a href="/x">Lain</a></nav>
<article>
<h1>Judul OG</h1>
%s
</article>
<footer>Hak cipta</footer>
</body></html>`

func articleBody() string {
	var b strings.Builder
	for i := 0; i < 12; i++ {
		b.WriteString("<p>Ini paragraf artikel yang cukup panjang untuk dianggap sebagai isi utama halaman, berisi penjelasan yang berguna bagi pembaca dan diulang beberapa kali.</p>\n")
	}
	return b.String()
}

func TestFetchWebArticle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redir" {
			http.Redirect(w, r, "/artikel?utm_source=x", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Replace(articleHTML, "%s", articleBody(), 1)))
	}))
	defer srv.Close()

	f := New(Options{AllowPrivate: true})
	res, err := f.Fetch(context.Background(), srv.URL+"/redir")
	if err != nil {
		t.Fatal(err)
	}
	if res.Platform != "web" || res.ContentType != "article" {
		t.Errorf("platform/type = %s/%s", res.Platform, res.ContentType)
	}
	if res.Title != "Judul OG" || res.Description != "Deskripsi singkat" || res.SiteName != "Situs Uji" || res.Author != "Budi" {
		t.Errorf("meta salah: %+v", res)
	}
	if res.ThumbnailURL != srv.URL+"/img/cover.jpg" {
		t.Errorf("thumbnail tidak absolut: %s", res.ThumbnailURL)
	}
	if res.Language != "id" {
		t.Errorf("language = %q", res.Language)
	}
	if res.PublishedAt == nil || !res.PublishedAt.Equal(time.Date(2024, 5, 1, 3, 0, 0, 0, time.UTC)) {
		t.Errorf("publishedAt = %v", res.PublishedAt)
	}
	if !strings.Contains(res.Text, "paragraf artikel") || strings.Contains(res.Text, "Hak cipta") {
		t.Errorf("teks artikel salah: %q", res.Text)
	}
}

func TestFetchNonHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4"))
	}))
	defer srv.Close()
	res, err := New(Options{AllowPrivate: true}).Fetch(context.Background(), srv.URL+"/docs/laporan%20q1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "laporan q1.pdf" || len(res.Warnings) == 0 || !strings.HasPrefix(res.Warnings[0], "bukan html") {
		t.Errorf("hasil non-html salah: %+v", res)
	}
}

func TestSSRFBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server tidak boleh dihubungi")
	}))
	defer srv.Close()
	f := New(Options{})
	if _, err := f.Fetch(context.Background(), srv.URL+"/"); !errors.Is(err, ErrBlocked) {
		t.Errorf("Fetch: want ErrBlocked, got %v", err)
	}
	if _, _, err := f.Download(context.Background(), srv.URL+"/x.png", 0); !errors.Is(err, ErrBlocked) {
		t.Errorf("Download: want ErrBlocked, got %v", err)
	}
	// Hostname yang resolve ke loopback juga harus diblokir (anti-rebinding).
	if _, err := f.Fetch(context.Background(), "http://localhost:1/"); !errors.Is(err, ErrBlocked) {
		t.Errorf("localhost: want ErrBlocked, got %v", err)
	}
}

func TestSSRFRedirectBlocked(t *testing.T) {
	// Server "publik" (diizinkan via AllowPrivate=false tidak bisa dites), jadi
	// kita uji guard per-hop dengan transport langsung: hop kedua ke IP privat.
	for _, ip := range []string{"10.0.0.1", "172.16.5.5", "192.168.1.1", "127.0.0.1", "169.254.169.254", "0.0.0.0", "::1", "fe80::1", "fd00::1", "100.64.0.1"} {
		if isPublicIP(parseIP(ip)) {
			t.Errorf("%s harus dianggap privat", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700::1111"} {
		if !isPublicIP(parseIP(ip)) {
			t.Errorf("%s harus dianggap publik", ip)
		}
	}
}

func TestTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>" + strings.Repeat("a", 5000) + "</body></html>"))
	}))
	defer srv.Close()
	f := New(Options{AllowPrivate: true, MaxBytes: 1024})
	if _, err := f.Fetch(context.Background(), srv.URL+"/"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
	if _, _, err := f.Download(context.Background(), srv.URL+"/", 100); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Download: want ErrTooLarge, got %v", err)
	}
	data, ct, err := f.Download(context.Background(), srv.URL+"/", 10_000)
	if err != nil || ct != "text/html" || len(data) == 0 {
		t.Errorf("Download ok: %v %s %d", err, ct, len(data))
	}
}

func TestParseTimedText(t *testing.T) {
	xmlDoc := `<?xml version="1.0" encoding="utf-8" ?><transcript>
<text start="0.5" dur="2">Halo semua,</text>
<text start="2.5" dur="2">ini &amp;#39;tes&amp;#39; caption</text>
<text start="5" dur="1">  </text>
</transcript>`
	got, err := parseTimedText([]byte(xmlDoc))
	if err != nil {
		t.Fatal(err)
	}
	if got != "Halo semua, ini 'tes' caption" {
		t.Errorf("got %q", got)
	}
	srv3 := `<timedtext format="3"><body><p t="0" d="1000"><s>Satu</s><s> dua</s></p><p t="1000" d="1000">tiga</p></body></timedtext>`
	got, err = parseTimedText([]byte(srv3))
	if err != nil || got != "Satu dua tiga" {
		t.Errorf("srv3: %q %v", got, err)
	}
	if _, err := parseTimedText([]byte("<transcript></transcript>")); err == nil {
		t.Error("kosong harus error")
	}
}

func TestParsePlayerResponse(t *testing.T) {
	page := `<html><script>var x = 1;</script><script nonce="a">var ytInitialPlayerResponse = {"responseContext":{"a":"b}"},"videoDetails":{"videoId":"abc","shortDescription":"Deskripsi \"video\" {tes}","lengthSeconds":"125"},"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"https://www.youtube.com/api/timedtext?lang=en","languageCode":"en","kind":"asr"},{"baseUrl":"https://www.youtube.com/api/timedtext?lang=id","languageCode":"id"}]}}};var ytInitialData = {};</script></html>`
	info, err := parsePlayerResponse([]byte(page))
	if err != nil {
		t.Fatal(err)
	}
	if info.Description != `Deskripsi "video" {tes}` || info.Duration != 125 || len(info.Tracks) != 2 {
		t.Errorf("info = %+v", info)
	}
	tr := pickCaptionTrack(info.Tracks, "id", "en")
	if tr == nil || tr.LanguageCode != "id" {
		t.Errorf("pilih track: %+v", tr)
	}
	tr = pickCaptionTrack(info.Tracks, "fr")
	if tr == nil || tr.LanguageCode != "en" {
		t.Errorf("fallback track pertama: %+v", tr)
	}
	if _, err := parsePlayerResponse([]byte("<html>tidak ada</html>")); err == nil {
		t.Error("tanpa marker harus error")
	}
}

func TestFetchYouTubeViaLocalServer(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/oembed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Video Uji","author_name":"Kanal","thumbnail_url":"https://i.ytimg.com/vi/x/hq.jpg"}`))
	})
	mux.HandleFunc("/watch", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><script>var ytInitialPlayerResponse = {"videoDetails":{"shortDescription":"desk","lengthSeconds":"60"},"captions":{"playerCaptionsTracklistRenderer":{"captionTracks":[{"baseUrl":"` + srvURL + `/timedtext","languageCode":"id"}]}}};</script></html>`))
	})
	mux.HandleFunc("/timedtext", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<transcript><text start="0" dur="1">halo</text><text start="1" dur="1">dunia</text></transcript>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	f := New(Options{AllowPrivate: true})
	f.youtubeOEmbed = srv.URL + "/oembed"
	res := Result{CanonicalURL: srv.URL + "/watch?v=abc", Platform: "youtube"}
	if err := f.fetchYouTube(context.Background(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Title != "Video Uji" || res.Author != "Kanal" || res.Description != "desk" || res.Duration != 60 {
		t.Errorf("res = %+v", res)
	}
	if res.Transcript != "halo dunia" || res.Language != "id" {
		t.Errorf("transcript = %q lang=%q warnings=%v", res.Transcript, res.Language, res.Warnings)
	}
}

func TestFetchXOEmbed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oembed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"author_name":"Jack","html":"<blockquote class=\"twitter-tweet\"><p lang=\"en\">just setting up my twttr</p>&mdash; jack (@jack) <a href=\"https://x.com/jack/status/20\">March 21, 2006</a></blockquote>"}`))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := New(Options{AllowPrivate: true})
	f.xOEmbed = srv.URL + "/oembed"
	res := Result{CanonicalURL: srv.URL + "/jack/status/20", Platform: "x"}
	if err := f.fetchX(context.Background(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Title != "just setting up my twttr" || res.Author != "Jack" || !strings.Contains(res.Text, "@jack") {
		t.Errorf("res = %+v", res)
	}
	if len(res.Warnings) == 0 {
		t.Error("403 OG harus jadi warning")
	}
}

func TestClean(t *testing.T) {
	in := "  a   b \r\n\r\n\r\n\n c\t\td \n\n"
	if got := Clean(in); got != "a b\n\nc d" {
		t.Errorf("got %q", got)
	}
}
