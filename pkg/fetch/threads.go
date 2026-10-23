package fetch

import (
	"context"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// mobileSafariUserAgent: halaman /embed/ Threads dirender di server untuk Safari iPhone dan
// memuat media post (gambar & video). Halaman post biasa hanya memberi og:image berupa kartu
// pratinjau buatan Threads (nama akun + tulisan caption), bukan foto post-nya.
const mobileSafariUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1"

// ThreadsMedia adalah media asli sebuah post Threads.
type ThreadsMedia struct {
	Images []string // gambar post (bukan avatar/ikon), urut seperti di post
	Videos []string // video .mp4 post
}

// ThreadsMedia mengambil media post Threads dari halaman embed. rawURL boleh link /share/
// (diikuti redirect-nya ke /@user/post/ID). Post yang hanya tulisan menghasilkan media kosong.
func (f *Fetcher) ThreadsMedia(ctx context.Context, rawURL string) (ThreadsMedia, error) {
	post, err := f.threadsPostURL(ctx, rawURL)
	if err != nil {
		return ThreadsMedia{}, err
	}
	p, err := f.getPageUA(ctx, post+"/embed/", mobileSafariUserAgent)
	if err != nil {
		return ThreadsMedia{}, err
	}
	doc, err := html.Parse(strings.NewReader(string(p.body)))
	if err != nil {
		return ThreadsMedia{}, err
	}
	return parseThreadsEmbed(doc), nil
}

// threadsPostURL mengubah link /share/ menjadi URL post (tanpa query) dengan mengikuti redirect.
func (f *Fetcher) threadsPostURL(ctx context.Context, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", ErrInvalidURL
	}
	if !strings.Contains(u.Path, "/post/") {
		p, err := f.getPageUA(ctx, rawURL, linkPreviewUserAgent)
		if err != nil {
			return "", err
		}
		u = p.finalURL
	}
	if u == nil || !strings.Contains(u.Path, "/post/") {
		return "", ErrInvalidURL
	}
	return u.Scheme + "://" + u.Host + strings.TrimSuffix(u.Path, "/"), nil
}

func parseThreadsEmbed(doc *html.Node) ThreadsMedia {
	var m ThreadsMedia
	seen := map[string]bool{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Img:
				// Gambar post di CDN Instagram berpola /t51.<id>-15/; avatar -19/, ikon t1.*.
				if src := attr(n, "src"); isThreadsCDN(src) && strings.Contains(src, "/t51.") && strings.Contains(src, "-15/") && !seen[src] {
					seen[src] = true
					m.Images = append(m.Images, src)
				}
			case atom.Source, atom.Video:
				if src := attr(n, "src"); isThreadsCDN(src) && strings.Contains(strings.SplitN(src, "?", 2)[0], ".mp4") && !seen[src] {
					seen[src] = true
					m.Videos = append(m.Videos, src)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return m
}

// isThreadsCDN membatasi media ke CDN Meta supaya URL dari halaman tidak bisa diarahkan ke host lain.
func isThreadsCDN(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	h := u.Hostname()
	return strings.HasSuffix(h, ".cdninstagram.com") || strings.HasSuffix(h, ".fbcdn.net")
}
