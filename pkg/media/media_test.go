package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSupported(t *testing.T) {
	for _, p := range []string{"youtube", "tiktok", "instagram"} {
		if !Supported(p) {
			t.Fatalf("%s harus didukung", p)
		}
	}
	for _, p := range []string{"web", "threads", "x", ""} {
		if Supported(p) {
			t.Fatalf("%s tidak boleh didukung", p)
		}
	}
}

func TestClassify(t *testing.T) {
	if !errors.Is(classify(errors.New("ERROR: [Instagram] abc: Requested content is not available, rate-limit reached or login required")), ErrLoginNeeded) {
		t.Fatal("harus ErrLoginNeeded")
	}
	if !errors.Is(classify(errors.New("ERROR: [Instagram] xyz: There is no video in this post")), ErrNoVideo) {
		t.Fatal("harus ErrNoVideo")
	}
	if !errors.Is(classify(errors.New("ERROR: Unsupported URL: https://www.tiktok.com/@a/photo/123")), ErrNoVideo) {
		t.Fatal("post foto TikTok harus ErrNoVideo")
	}
}

func TestTransient(t *testing.T) {
	if !transient(errors.New("yt-dlp: exit status 1: ERROR: [TikTok] 123: Unable to extract webpage video data; please report this issue")) {
		t.Fatal("halaman TikTok tanpa data harus dicoba ulang")
	}
	for _, m := range []string{"ERROR: [TikTok] 1: This video is private", "ERROR: Unsupported URL: https://x"} {
		if transient(errors.New(m)) {
			t.Fatalf("%q bukan error sementara", m)
		}
	}
	if transient(ErrToolMissing) {
		t.Fatal("alat hilang bukan error sementara")
	}
}

// yt-dlp palsu yang gagal "Unable to extract webpage video data" sampai percobaan ke-n,
// lalu mencetak metadata JSON. Menguji Probe mencoba ulang error sementara.
func fakeYtDlp(t *testing.T, failTimes int) (*Tool, string) {
	t.Helper()
	dir := t.TempDir()
	count := filepath.Join(dir, "count")
	script := filepath.Join(dir, "yt-dlp")
	body := `#!/bin/sh
n=$(cat "` + count + `" 2>/dev/null || echo 0)
n=$((n+1))
echo $n > "` + count + `"
if [ $n -le ` + strconv.Itoa(failTimes) + ` ]; then
  echo "ERROR: [TikTok] 1: Unable to extract webpage video data" >&2
  exit 1
fi
echo '{"duration":28.4,"title":"judul","description":"caption lengkap","uploader":"u","vcodec":"h264"}'
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := New(Options{YtDlpPath: script})
	tool.retryDelays = []time.Duration{time.Millisecond, time.Millisecond}
	return tool, count
}

func TestProbeMencobaUlangErrorSementara(t *testing.T) {
	tool, count := fakeYtDlp(t, 2)
	info, err := tool.Probe(context.Background(), "https://vt.tiktok.com/x")
	if err != nil {
		t.Fatal(err)
	}
	if info.Description != "caption lengkap" || info.Duration != 28 || !info.HasVideo {
		t.Fatalf("%+v", info)
	}
	if b, _ := os.ReadFile(count); strings.TrimSpace(string(b)) != "3" {
		t.Fatalf("harus 3 percobaan, dapat %s", b)
	}
}

func TestProbeMenyerahSetelahBatas(t *testing.T) {
	tool, count := fakeYtDlp(t, 5)
	if _, err := tool.Probe(context.Background(), "https://vt.tiktok.com/x"); err == nil {
		t.Fatal("harus gagal")
	}
	if b, _ := os.ReadFile(count); strings.TrimSpace(string(b)) != "3" {
		t.Fatalf("harus berhenti di 3 percobaan, dapat %s", b)
	}
}

// Tes ini membuat video sintetis 6 detik dengan ffmpeg (nada + pola warna), lalu
// memastikan audio dan frame bisa diambil. Dilewati bila ffmpeg tidak terpasang.
func TestAudioDanFrameDariVideoSintetis(t *testing.T) {
	tool := New(Options{})
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg tidak terpasang")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe tidak terpasang")
	}
	dir := t.TempDir()
	video := filepath.Join(dir, "in.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=10:duration=6",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=6",
		"-shortest", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", video)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("buat video uji: %v %s", err, out)
	}
	ctx := context.Background()
	d, err := tool.Duration(ctx, video)
	if err != nil || d < 5 || d > 7 {
		t.Fatalf("durasi %v %v", d, err)
	}
	audio, err := tool.ExtractAudio(ctx, video, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(audio); st.Size() < 1024 {
		t.Fatal("audio terlalu kecil")
	}
	frames, err := tool.ExtractFrames(ctx, video, dir, d, 4)
	if err != nil || len(frames) != 4 {
		t.Fatalf("frame %d %v", len(frames), err)
	}
}

// ToAAC diuji dengan nada sintetis 3 detik (wav); dilewati bila ffmpeg/ffprobe tidak ada.
func TestToAACMenghasilkanM4A(t *testing.T) {
	tool := New(Options{})
	if !tool.CanTranscode() {
		t.Skip("ffmpeg/ffprobe tidak terpasang")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.wav")
	if out, err := exec.Command("ffmpeg", "-y", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3", in).CombinedOutput(); err != nil {
		t.Fatalf("buat wav: %v %s", err, out)
	}
	out := filepath.Join(dir, "out.m4a")
	if err := tool.ToAAC(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	d, err := tool.Duration(context.Background(), out)
	if err != nil || d < 2.5 || d > 3.5 {
		t.Fatalf("durasi %v err %v", d, err)
	}
	codec, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=codec_name,channels", "-of", "default=nw=1:nk=1", out).Output()
	if err != nil || string(codec) != "aac\n1\n" {
		t.Fatalf("codec %q err %v", codec, err)
	}
}
