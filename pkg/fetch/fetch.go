// Package fetch mengambil metadata dan isi dari URL yang disimpan user:
// YouTube, TikTok, Instagram, Threads, X, dan artikel web biasa.
//
// Paket ini mandiri (tidak mengimpor vaulty/internal) supaya bisa dites
// sendiri dan dipakai ulang oleh worker tanpa menyeret dependensi DB/queue.
// Semua akses jaringan dilindungi anti-SSRF: host diresolve lalu IP-nya
// dicek sebelum koneksi dibuka, termasuk setiap hop redirect.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Result adalah hasil pengambilan satu URL. Field yang tidak diketahui
// dibiarkan kosong; kegagalan non-fatal dicatat di Warnings supaya
// pipeline bisa lanjut dengan data parsial (status "partial" di item).
type Result struct {
	CanonicalURL string // URL setelah dibersihkan (tanpa utm_*, fbclid, dsb)
	Platform     string // "youtube","tiktok","instagram","threads","x","web"
	ContentType  string // "video","post","article","unknown"
	Title        string
	Description  string
	SiteName     string
	Author       string
	ThumbnailURL string
	Text         string // isi artikel (readability) atau caption post; sudah di-Clean
	Transcript   string // transkrip video bila berhasil (YouTube caption), "" bila tidak
	Language     string // kode bahasa bila diketahui ("id","en")
	Duration     int    // detik, 0 bila tidak diketahui
	PublishedAt  *time.Time
	Warnings     []string // hal yang gagal tapi tidak fatal, mis. "oembed tiktok gagal: 403"
}

// Options mengatur perilaku Fetcher. Nilai nol memakai default yang wajar.
type Options struct {
	Timeout      time.Duration // default 15s
	MaxBytes     int64         // default 5 MB untuk HTML
	UserAgent    string        // default UA browser desktop yang wajar
	AllowPrivate bool          // default false: blokir IP privat/loopback/link-local (anti-SSRF)
}

var (
	ErrBlocked    = errors.New("url menuju alamat yang tidak diizinkan")
	ErrInvalidURL = errors.New("url tidak valid")
	ErrTooLarge   = errors.New("konten terlalu besar")
)

const (
	defaultTimeout  = 15 * time.Second
	defaultMaxBytes = 5 << 20
	// UA menyerupai browser desktop karena banyak situs (dan oEmbed TikTok)
	// mengembalikan halaman kosong/403 untuk UA yang terlihat seperti bot.
	// Instagram/Threads memberi halaman login ke browser biasa, tetapi memberi OG lengkap
	// (teks post + gambar) ke crawler pratinjau link seperti yang dipakai WhatsApp/Facebook.
	linkPreviewUserAgent = "facebookexternalhit/1.1 (+http://www.facebook.com/externalhit_uatext.php)"
	defaultUserAgent     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	maxRedirects         = 5
	// Batas minimal panjang teks agar dianggap "article", bukan sekadar
	// halaman landing/login yang hanya punya beberapa kalimat.
	articleMinChars = 400
)

// Fetcher adalah klien HTTP yang sudah dipasangi guard SSRF dan batas ukuran.
type Fetcher struct {
	opts   Options
	client *http.Client

	// Endpoint oEmbed dibuat field supaya bisa dialihkan ke httptest.
	youtubeOEmbed string
	tiktokOEmbed  string
	xOEmbed       string
}

// New membuat Fetcher. Options kosong = default.
func New(opts Options) *Fetcher {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultMaxBytes
	}
	if opts.UserAgent == "" {
		opts.UserAgent = defaultUserAgent
	}
	client := &http.Client{
		Timeout:   opts.Timeout,
		Transport: newGuardedTransport(opts.AllowPrivate),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Batasi hop supaya redirect loop tidak menghabiskan waktu;
			// pengecekan SSRF per hop terjadi di DialContext transport.
			if len(via) >= maxRedirects {
				return fmt.Errorf("terlalu banyak redirect (>%d)", maxRedirects)
			}
			return nil
		},
	}
	return &Fetcher{
		opts:          opts,
		client:        client,
		youtubeOEmbed: "https://www.youtube.com/oembed",
		tiktokOEmbed:  "https://www.tiktok.com/oembed",
		xOEmbed:       "https://publish.twitter.com/oembed",
	}
}

// Fetch mengambil metadata+isi dari rawURL sesuai platformnya.
// Error hanya dikembalikan bila URL tidak valid, diblokir, terlalu besar,
// atau (untuk web) halaman sama sekali tidak bisa diambil. Kegagalan
// parsial masuk ke Result.Warnings.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Result, error) {
	canonical, platform, err := Canonicalize(rawURL)
	if err != nil {
		return Result{}, err
	}
	res := Result{CanonicalURL: canonical, Platform: platform, ContentType: "unknown"}

	switch platform {
	case "youtube":
		err = f.fetchYouTube(ctx, &res)
	case "tiktok":
		err = f.fetchTikTok(ctx, &res, rawURL)
	case "x":
		err = f.fetchX(ctx, &res)
	case "instagram", "threads":
		err = f.fetchSocialOG(ctx, &res, rawURL)
	default:
		err = f.fetchWeb(ctx, &res)
	}
	if err != nil {
		return Result{}, err
	}
	res.Title = Clean(res.Title)
	res.Description = Clean(res.Description)
	res.Text = Clean(res.Text)
	res.Transcript = Clean(res.Transcript)
	res.Author = strings.TrimSpace(res.Author)
	return res, nil
}

// Download mengunduh resource biner (thumbnail, gambar) dengan guard SSRF
// dan batas ukuran. maxBytes <= 0 memakai Options.MaxBytes.
func (f *Fetcher) Download(ctx context.Context, rawURL string, maxBytes int64) ([]byte, string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", ErrInvalidURL
	}
	if maxBytes <= 0 {
		maxBytes = f.opts.MaxBytes
	}
	resp, err := f.do(ctx, u.String(), "image/*,*/*;q=0.8")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("download %s: status %d", u.Host, resp.StatusCode)
	}
	data, err := readLimited(resp.Body, maxBytes)
	if err != nil {
		return nil, "", err
	}
	ct := resp.Header.Get("Content-Type")
	if mt, _, perr := mime.ParseMediaType(ct); perr == nil {
		ct = mt
	}
	return data, ct, nil
}

// page adalah hasil mentah satu GET HTML.
type page struct {
	body        []byte
	contentType string // media type saja, tanpa parameter
	charset     string
	finalURL    *url.URL
	status      int
}

// do membuat request GET dengan header standar. Error jaringan yang
// berasal dari guard SSRF dipetakan ke ErrBlocked supaya pemanggil bisa
// membedakannya dari gangguan jaringan biasa.
func (f *Fetcher) do(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	return f.doUA(ctx, rawURL, accept, f.opts.UserAgent)
}

// doUA sama dengan do, dengan User-Agent tertentu (mis. crawler pratinjau link).
func (f *Fetcher) doUA(ctx context.Context, rawURL, accept, ua string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, ErrInvalidURL
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "id-ID,id;q=0.9,en-US;q=0.8,en;q=0.7")
	resp, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, ErrBlocked
		}
		return nil, err
	}
	return resp, nil
}

// getPage mengambil halaman dan membaca body dengan batas ukuran.
func (f *Fetcher) getPage(ctx context.Context, rawURL string) (*page, error) {
	return f.getPageUA(ctx, rawURL, f.opts.UserAgent)
}

func (f *Fetcher) getPageUA(ctx context.Context, rawURL, ua string) (*page, error) {
	resp, err := f.doUA(ctx, rawURL, "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8", ua)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := readLimited(resp.Body, f.opts.MaxBytes)
	if err != nil {
		return nil, err
	}
	p := &page{body: body, finalURL: resp.Request.URL, status: resp.StatusCode}
	if mt, params, perr := mime.ParseMediaType(resp.Header.Get("Content-Type")); perr == nil {
		p.contentType = mt
		p.charset = params["charset"]
	}
	return p, nil
}

// readLimited membaca paling banyak max byte; lebih dari itu → ErrTooLarge.
// Dibaca max+1 supaya bisa membedakan "pas" dengan "kelebihan".
func readLimited(r io.Reader, max int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, ErrTooLarge
	}
	return data, nil
}

func isHTML(ct string) bool {
	return ct == "" || ct == "text/html" || ct == "application/xhtml+xml"
}

// fileNameFromURL dipakai sebagai judul bila URL menunjuk file (gambar/PDF).
func fileNameFromURL(u *url.URL) string {
	name := path.Base(u.Path)
	if name == "/" || name == "." || name == "" {
		return u.Host
	}
	if dec, err := url.PathUnescape(name); err == nil {
		name = dec
	}
	return name
}

func (r *Result) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}
