package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
