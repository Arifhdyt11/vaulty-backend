package fetch

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

// meta adalah ringkasan tag yang kita pedulikan dari sebuah halaman HTML.
type meta struct {
	title      string            // <title>
	lang       string            // <html lang>
	props      map[string]string // og:*, twitter:*, article:*, name=description/author
	paragraphs []string          // teks <p> sebagai fallback readability
	doc        *html.Node
}

// parseHTML mem-parse body dengan menghormati charset dari header/meta.
// html.Parse toleran terhadap HTML rusak, jadi tidak perlu recover.
func parseHTML(body []byte, contentType, cs string) (*meta, error) {
	ct := contentType
	if cs != "" {
		ct += "; charset=" + cs
	}
	// charset.NewReader mendeteksi dari header, BOM, lalu <meta charset>;
	// bila tidak tahu, jatuh ke UTF-8 (asumsi yang paling sering benar).
	r, err := charset.NewReader(bytes.NewReader(body), ct)
	if err != nil {
		r = bytes.NewReader(body)
	}
	doc, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	m := &meta{props: map[string]string{}, doc: doc}
	m.walk(doc)
	return m, nil
}

func (m *meta) walk(n *html.Node) {
	if n.Type == html.ElementNode {
		switch n.DataAtom {
		case atom.Html:
			if v := attr(n, "lang"); v != "" {
				m.lang = v
			}
		case atom.Title:
			if m.title == "" {
				m.title = strings.TrimSpace(textOf(n))
			}
		case atom.Meta:
			key := strings.ToLower(attr(n, "property"))
			if key == "" {
				key = strings.ToLower(attr(n, "name"))
			}
			if content := strings.TrimSpace(attr(n, "content")); key != "" && content != "" {
				// Tag pertama menang: biasanya yang paling akurat; duplikat
				// sering berasal dari plugin/template yang mengisi generik.
				if _, exists := m.props[key]; !exists {
					m.props[key] = content
				}
			}
		case atom.P:
			if t := strings.TrimSpace(textOf(n)); t != "" {
				m.paragraphs = append(m.paragraphs, t)
			}
		case atom.Script, atom.Style, atom.Noscript, atom.Template:
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		m.walk(c)
	}
}

// first mengembalikan properti pertama yang terisi dari daftar kunci.
func (m *meta) first(keys ...string) string {
	for _, k := range keys {
		if v := m.props[k]; v != "" {
			return v
		}
	}
	return ""
}

func attr(n *html.Node, name string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var rec func(*html.Node)
	rec = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			return
		}
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Script, atom.Style, atom.Noscript, atom.Template:
				return
			case atom.Br:
				b.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
		// Elemen blok diakhiri newline supaya paragraf tidak menempel
		// (penting untuk html oEmbed X yang menaruh caption dan atribusi
		// berdampingan).
		if n.Type == html.ElementNode && isBlock(n.DataAtom) {
			b.WriteByte('\n')
		}
	}
	rec(n)
	return b.String()
}

// applyMeta mengisi Result dari OG/Twitter/meta standar tanpa menimpa
// nilai yang sudah ada (mis. dari oEmbed yang lebih akurat).
func applyMeta(res *Result, m *meta, pageURL *url.URL) {
	setIfEmpty(&res.Title, m.first("og:title", "twitter:title"), m.title)
	setIfEmpty(&res.Description, m.first("og:description", "twitter:description", "description"))
	setIfEmpty(&res.SiteName, m.first("og:site_name"))
	setIfEmpty(&res.Author, m.first("article:author", "author", "twitter:creator"))
	if res.ThumbnailURL == "" {
		if img := m.first("og:image", "og:image:url", "og:image:secure_url", "twitter:image", "twitter:image:src"); img != "" {
			res.ThumbnailURL = absoluteURL(pageURL, img)
		}
	}
	if res.Language == "" {
		res.Language = langCode(m.first("og:locale"))
		if res.Language == "" {
			res.Language = langCode(m.lang)
		}
	}
	if res.PublishedAt == nil {
		if t := parseTime(m.first("article:published_time", "og:published_time", "date", "pubdate", "datepublished")); t != nil {
			res.PublishedAt = t
		}
	}
	if res.SiteName == "" && pageURL != nil {
		res.SiteName = strings.TrimPrefix(pageURL.Hostname(), "www.")
	}
}

func setIfEmpty(dst *string, candidates ...string) {
	if *dst != "" {
		return
	}
	for _, c := range candidates {
		if c = strings.TrimSpace(c); c != "" {
			*dst = c
			return
		}
	}
}

// absoluteURL membuat ref absolut terhadap base; URL relatif seperti
// "/img/cover.jpg" umum di situs berita.
func absoluteURL(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if base == nil {
		return ref
	}
	u, err := base.Parse(ref)
	if err != nil {
		return ref
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

// langCode memetakan "id_ID"/"en-US"/"id" → "id"/"en".
func langCode(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	if i := strings.IndexAny(s, "_-"); i > 0 {
		s = s[:i]
	}
	if len(s) < 2 || len(s) > 3 {
		return ""
	}
	return s
}

var timeLayouts = []string{
	time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05Z0700", "2006-01-02T15:04:05",
	"2006-01-02 15:04:05", "2006-01-02", time.RFC1123, time.RFC1123Z, time.RFC822, time.RFC822Z,
}

func parseTime(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

// fetchWeb: jalur artikel umum (readability).
func (f *Fetcher) fetchWeb(ctx context.Context, res *Result) error {
	p, err := f.getPage(ctx, res.CanonicalURL)
	if err != nil {
		return err
	}
	if p.status >= 400 {
		// Halaman error masih bisa punya <title>, tapi isinya bukan artikel.
		res.warn("status %d", p.status)
	}
	if !isHTML(p.contentType) {
		res.Title = fileNameFromURL(p.finalURL)
		res.SiteName = strings.TrimPrefix(p.finalURL.Hostname(), "www.")
		res.warn("bukan html: %s", p.contentType)
		return nil
	}
	m, err := parseHTML(p.body, p.contentType, p.charset)
	if err != nil {
		res.warn("parse html gagal: %v", err)
		return nil
	}
	applyMeta(res, m, p.finalURL)
	res.Text = f.extractArticle(res, m, p.finalURL)
	if len(res.Text) > articleMinChars {
		res.ContentType = "article"
	}
	return nil
}

// extractArticle menjalankan readability; bila gagal/kosong, pakai gabungan <p>.
// Meta sudah diambil sebelumnya karena readability memodifikasi DOM.
func (f *Fetcher) extractArticle(res *Result, m *meta, pageURL *url.URL) (text string) {
	defer func() {
		// readability pernah panik pada DOM aneh; jangan sampai menjatuhkan worker.
		if r := recover(); r != nil {
			res.warn("readability panic: %v", r)
			text = strings.Join(m.paragraphs, "\n\n")
		}
	}()
	art, err := readability.FromDocument(m.doc, pageURL)
	if err != nil {
		res.warn("readability gagal: %v", err)
		return strings.Join(m.paragraphs, "\n\n")
	}
	setIfEmpty(&res.Title, art.Title)
	setIfEmpty(&res.Author, art.Byline)
	setIfEmpty(&res.SiteName, art.SiteName)
	setIfEmpty(&res.Description, art.Excerpt)
	if res.ThumbnailURL == "" && art.Image != "" {
		res.ThumbnailURL = absoluteURL(pageURL, art.Image)
	}
	if res.Language == "" {
		res.Language = langCode(art.Language)
	}
	if res.PublishedAt == nil && art.PublishedTime != nil {
		t := art.PublishedTime.UTC()
		res.PublishedAt = &t
	}
	txt := strings.TrimSpace(art.TextContent)
	fallback := strings.Join(m.paragraphs, "\n\n")
	// Readability kadang hanya menangkap sidebar pendek; kalau <p> jauh lebih
	// kaya, pakai itu saja.
	if len(txt) < articleMinChars && len(fallback) > len(txt) {
		return fallback
	}
	return txt
}

// fetchSocialOG: Instagram/Threads. Halaman diminta dengan User-Agent crawler pratinjau
// link karena browser biasa hanya mendapat halaman login. Bila tetap tidak ada teks post,
// judul dikosongkan supaya tahap fetch dianggap gagal dan app menawarkan screenshot.
func (f *Fetcher) fetchSocialOG(ctx context.Context, res *Result, sourceURL string) error {
	res.ContentType = "post"
	res.SiteName = platformSiteName(res.Platform)
	// Pakai URL asli (mis. link /share/ Threads) supaya redirect ke post sebenarnya diikuti.
	f.tryOGUA(ctx, res, sourceURL, linkPreviewUserAgent)
	if res.Description == "" && res.Title == "" {
		f.tryOGUA(ctx, res, res.CanonicalURL, linkPreviewUserAgent)
	}
	if res.Platform == "threads" {
		f.applyThreadsMedia(ctx, res, sourceURL)
	}
	res.Description = socialCaption(res.Description)
	if res.Description == "" {
		if isGenericSocialTitle(res.Title, res.SiteName) {
			res.Title = ""
		}
		res.warn("teks post %s tidak bisa diambil (mungkin privat); tambahkan screenshot", res.SiteName)
		return nil
	}
	// og:title Instagram/Threads berbentuk "Nama (@user) on Threads: ..." dan tidak enak
	// dijadikan judul. Simpan nama akunnya sebagai author; judul dibuat Vee dari isi post.
	setIfEmpty(&res.Author, socialAuthor(res.Title))
	res.Title = ""
	setIfEmpty(&res.Text, res.Description)
	return nil
}

// applyThreadsMedia mengganti og:image Threads (kartu teks caption) dengan media asli post:
// gambar pertama, atau video untuk diambil frame-nya. Post yang hanya tulisan tidak diberi
// thumbnail supaya brankas tidak penuh tangkapan tulisan.
func (f *Fetcher) applyThreadsMedia(ctx context.Context, res *Result, sourceURL string) {
	res.ThumbnailURL = ""
	m, err := f.ThreadsMedia(ctx, sourceURL)
	if err != nil {
		res.warn("media threads gagal: %v", err)
		return
	}
	switch {
	case len(m.Images) > 0:
		res.ThumbnailURL = m.Images[0]
	case len(m.Videos) > 0:
		res.PreviewVideoURL = m.Videos[0]
	}
}

// socialAuthor mengambil bagian sebelum " on <Platform>" / " di <Platform>".
func socialAuthor(title string) string {
	for _, sep := range []string{" on Threads", " di Threads", " on Instagram", " di Instagram"} {
		if i := strings.Index(title, sep); i > 0 {
			return strings.Trim(strings.TrimSpace(title[:i]), ". ")
		}
	}
	return ""
}

// socialCaption membuang prefiks statistik Instagram ("335 likes, 58 comments - user on June 3, 2026: ").
func socialCaption(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ": "); i > 0 && i < 120 && (strings.Contains(s[:i], " likes") || strings.Contains(s[:i], " comments") || strings.Contains(s[:i], " suka")) {
		s = strings.TrimSpace(s[i+2:])
	}
	return strings.Trim(s, "\"“” ")
}

func isGenericSocialTitle(title, site string) bool {
	t := strings.TrimSpace(strings.ToLower(title))
	return t == "" || t == strings.ToLower(site) || t == strings.ToLower(site)+" post" || strings.HasPrefix(t, "log in") || strings.HasPrefix(t, "masuk")
}

// tryOG best-effort: ambil halaman dan isi meta. Tidak pernah mengembalikan error.
func (f *Fetcher) tryOG(ctx context.Context, res *Result, pageURL string) {
	f.tryOGUA(ctx, res, pageURL, f.opts.UserAgent)
}

func (f *Fetcher) tryOGUA(ctx context.Context, res *Result, pageURL, ua string) {
	p, err := f.getPageUA(ctx, pageURL, ua)
	if err != nil {
		res.warn("ambil halaman %s gagal: %v", res.Platform, err)
		return
	}
	if p.status >= 400 {
		res.warn("halaman %s status %d", res.Platform, p.status)
	}
	if !isHTML(p.contentType) {
		res.warn("halaman %s bukan html: %s", res.Platform, p.contentType)
		return
	}
	m, err := parseHTML(p.body, p.contentType, p.charset)
	if err != nil {
		res.warn("parse html %s gagal: %v", res.Platform, err)
		return
	}
	applyMeta(res, m, p.finalURL)
}

func platformSiteName(platform string) string {
	switch platform {
	case "youtube":
		return "YouTube"
	case "tiktok":
		return "TikTok"
	case "instagram":
		return "Instagram"
	case "threads":
		return "Threads"
	case "x":
		return "X"
	}
	return ""
}

// stripHTML mengambil teks polos dari potongan HTML (oEmbed X/TikTok).
func stripHTML(s string) string {
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, DataAtom: atom.Body, Data: "body"})
	if err != nil {
		return s
	}
	var b strings.Builder
	for _, n := range nodes {
		b.WriteString(textOf(n))
		b.WriteByte('\n')
	}
	return b.String()
}

func isBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.Blockquote, atom.Li, atom.Ul, atom.Ol, atom.H1, atom.H2, atom.H3,
		atom.H4, atom.H5, atom.H6, atom.Tr, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Pre:
		return true
	}
	return false
}
