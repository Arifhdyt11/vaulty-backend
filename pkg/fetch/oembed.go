package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// oembed adalah subset respons oEmbed yang dipakai semua platform.
type oembed struct {
	Title        string `json:"title"`
	AuthorName   string `json:"author_name"`
	AuthorURL    string `json:"author_url"`
	ProviderName string `json:"provider_name"`
	ThumbnailURL string `json:"thumbnail_url"`
	HTML         string `json:"html"`
}

// getOEmbed memanggil endpoint oEmbed resmi. oEmbed dipilih sebagai jalur
// utama karena stabil dan tidak butuh login, berbeda dengan scraping halaman.
func (f *Fetcher) getOEmbed(ctx context.Context, endpoint string, params url.Values) (*oembed, error) {
	resp, err := f.do(ctx, endpoint+"?"+params.Encode(), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%d", resp.StatusCode)
	}
	body, err := readLimited(resp.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	var o oembed
	if err := json.Unmarshal(body, &o); err != nil {
		return nil, fmt.Errorf("json: %v", err)
	}
	return &o, nil
}

func (f *Fetcher) fetchTikTok(ctx context.Context, res *Result) error {
	res.ContentType = "video"
	res.SiteName = "TikTok"
	o, err := f.getOEmbed(ctx, f.tiktokOEmbed, url.Values{"url": {res.CanonicalURL}})
	if err != nil {
		res.warn("oembed tiktok gagal: %v", err)
		f.tryOG(ctx, res, res.CanonicalURL)
	} else {
		// Di TikTok "title" oEmbed adalah caption lengkap, jadi dipakai juga
		// sebagai Text supaya LLM punya bahan ringkasan.
		res.Title = o.Title
		res.Text = o.Title
		res.Author = o.AuthorName
		res.ThumbnailURL = o.ThumbnailURL
	}
	setIfEmpty(&res.Text, res.Description)
	if res.Title == "" {
		res.Title = "TikTok video"
	}
	return nil
}

func (f *Fetcher) fetchX(ctx context.Context, res *Result) error {
	res.ContentType = "post"
	res.SiteName = "X"
	o, err := f.getOEmbed(ctx, f.xOEmbed, url.Values{"url": {res.CanonicalURL}, "omit_script": {"true"}})
	if err != nil {
		res.warn("oembed x gagal: %v", err)
	} else {
		res.Author = o.AuthorName
		// html oEmbed berbentuk <blockquote><p>isi</p>&mdash; Nama (@handle) tanggal</blockquote>.
		res.Text = stripHTML(o.HTML)
		if first := firstLine(res.Text); first != "" {
			res.Title = truncate(first, 120)
		}
	}
	// OG X nyaris selalu kosong untuk non-browser, tapi murah untuk dicoba
	// karena kadang memberi og:image.
	f.tryOG(ctx, res, res.CanonicalURL)
	if res.Title == "" {
		if res.Author != "" {
			res.Title = "Post dari " + res.Author
		} else {
			res.Title = "X post"
		}
	}
	setIfEmpty(&res.Text, res.Description)
	return nil
}

func firstLine(s string) string {
	for _, line := range strings.Split(Clean(s), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
