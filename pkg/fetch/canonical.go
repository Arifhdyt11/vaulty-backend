package fetch

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Parameter pelacak yang dibuang dari semua URL. Dibuang supaya link yang
// sama dari sumber berbeda (WA, Telegram, share sheet) menghasilkan
// canonical_url identik dan cache fetch bisa kena.
var trackingParams = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "msclkid": true, "yclid": true, "ttclid": true,
	"igshid": true, "igsh": true, "mc_cid": true, "mc_eid": true, "_ga": true, "_gl": true,
	"ref_src": true, "ref_url": true, "si": true, "feature": true, "share_id": true,
	"mibextid": true, "cmpid": true, "spm": true, "_branch_match_id": true,
}

var youtubeIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,20}$`)

// Canonicalize membersihkan URL dan menebak platform. Murni (tanpa jaringan),
// sehingga aman dipanggil di jalur capture yang harus <200ms.
func Canonicalize(rawURL string) (canonical, platform string, err error) {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return "", "", ErrInvalidURL
	}
	// User sering menempel "example.com/artikel" tanpa skema.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, perr := url.Parse(raw)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", "", ErrInvalidURL
	}
	if strings.ContainsAny(u.Hostname(), " \t") || !strings.Contains(u.Hostname(), ".") && u.Hostname() != "localhost" {
		// Host tanpa titik hampir pasti salah ketik (kecuali localhost untuk dev).
		return "", "", ErrInvalidURL
	}
	u.Fragment = ""
	u.User = nil
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}
	bare := strings.TrimPrefix(host, "www.")
	bare = strings.TrimPrefix(bare, "m.")
	bare = strings.TrimPrefix(bare, "mobile.")

	switch {
	case bare == "youtube.com" || bare == "youtu.be" || bare == "music.youtube.com" || bare == "youtube-nocookie.com":
		if id := youtubeID(host, u); id != "" {
			return "https://www.youtube.com/watch?v=" + id, "youtube", nil
		}
		// Channel/playlist: tetap youtube tapi tidak diubah jadi watch.
		stripQuery(u, nil)
		return u.String(), "youtube", nil
	case bare == "tiktok.com" || strings.HasSuffix(bare, ".tiktok.com"):
		// vm.tiktok.com/XXXX adalah short link yang butuh redirect; biarkan apa
		// adanya, Fetch akan mengikuti redirect-nya.
		stripQuery(u, nil)
		u.Path = strings.TrimSuffix(u.Path, "/")
		return u.String(), "tiktok", nil
	case bare == "instagram.com":
		stripQuery(u, nil)
		return "https://www.instagram.com" + ensureTrailingSlash(u.Path), "instagram", nil
	case bare == "threads.net" || bare == "threads.com":
		stripQuery(u, nil)
		return "https://www.threads.net" + strings.TrimSuffix(u.Path, "/"), "threads", nil
	case bare == "x.com" || bare == "twitter.com" || bare == "fxtwitter.com" || bare == "vxtwitter.com":
		stripQuery(u, nil)
		return "https://x.com" + strings.TrimSuffix(u.Path, "/"), "x", nil
	}

	stripQuery(u, func(k string) bool {
		return trackingParams[k] || strings.HasPrefix(k, "utm_")
	})
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), "web", nil
}

// youtubeID mengambil ID video dari berbagai bentuk URL YouTube.
func youtubeID(host string, u *url.URL) string {
	var id string
	if strings.HasSuffix(host, "youtu.be") {
		id = strings.Trim(u.Path, "/")
	} else {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		switch {
		case u.Path == "/watch" || u.Path == "/watch/":
			id = u.Query().Get("v")
		case len(segs) >= 2 && (segs[0] == "shorts" || segs[0] == "embed" || segs[0] == "live" || segs[0] == "v"):
			id = segs[1]
		}
	}
	if i := strings.IndexAny(id, "?&/"); i >= 0 {
		id = id[:i]
	}
	if !youtubeIDRe.MatchString(id) {
		return ""
	}
	return id
}

// stripQuery membuang query. Bila drop nil semua dibuang; bila tidak,
// hanya key yang drop(key)==true. Sisanya diurutkan supaya stabil.
func stripQuery(u *url.URL, drop func(string) bool) {
	if drop == nil {
		u.RawQuery = ""
		return
	}
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		if drop(strings.ToLower(k)) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		for _, v := range q[k] {
			if b.Len() > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			if v != "" {
				b.WriteByte('=')
				b.WriteString(url.QueryEscape(v))
			}
		}
	}
	u.RawQuery = b.String()
}

func ensureTrailingSlash(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasSuffix(p, "/") {
		return p + "/"
	}
	return p
}
