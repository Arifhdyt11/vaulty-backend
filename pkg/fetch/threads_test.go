package fetch

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestParseThreadsEmbedHanyaMediaPost(t *testing.T) {
	page := `<html><body>
<img class="img" src="https://scontent.cdninstagram.com/v/t51.82787-19/avatar.jpg?x=1" alt="akun"/>
<img src="https://scontent.fsrg2-1.fna.fbcdn.net/v/t1.6435-9/ikon.jpg"/>
<video controls="1"><source src="https://instagram.fcgk28-1.fna.fbcdn.net/o1/v/t16/f2/m84/AQ.mp4?a=1"/></video>
<img class="img" src="https://scontent.cdninstagram.com/v/t51.82787-15/foto1.jpg?b=2" height="300"/>
<img class="img" src="https://scontent.cdninstagram.com/v/t51.82787-15/foto1.jpg?b=2" height="300"/>
<img class="img" src="https://evil.example.com/v/t51.82787-15/foto2.jpg"/>
</body></html>`
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		t.Fatal(err)
	}
	m := parseThreadsEmbed(doc)
	if len(m.Images) != 1 || !strings.Contains(m.Images[0], "foto1.jpg") {
		t.Fatalf("gambar = %v, mau hanya foto1 (tanpa avatar, ikon, duplikat, host asing)", m.Images)
	}
	if len(m.Videos) != 1 || !strings.Contains(m.Videos[0], "AQ.mp4") {
		t.Fatalf("video = %v", m.Videos)
	}
}

func TestParseThreadsEmbedPostTulisanSaja(t *testing.T) {
	doc, _ := html.Parse(strings.NewReader(`<img src="https://scontent.cdninstagram.com/v/t51.2885-19/avatar.jpg"/><p>halo</p>`))
	if m := parseThreadsEmbed(doc); len(m.Images)+len(m.Videos) != 0 {
		t.Fatalf("post tulisan saja harus tanpa media, dapat %+v", m)
	}
}
