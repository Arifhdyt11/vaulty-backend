// Package media: unduh video dari platform (yt-dlp) lalu ambil audio dan frame (ffmpeg)
// supaya isi video bisa ditranskrip dan dibaca model vision (ADR-015).
// Paket ini memanggil binary eksternal; tidak ada dependency Go tambahan.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	ErrToolMissing = errors.New("yt-dlp atau ffmpeg tidak terpasang")
	ErrTooLong     = errors.New("video melebihi durasi maksimum")
	ErrNoVideo     = errors.New("tidak ada video di link ini")
	ErrLoginNeeded = errors.New("platform meminta login untuk video ini")
)

// Platform yang boleh diunduh. Daftar tertutup supaya yt-dlp tidak dipakai untuk
// mengakses URL sembarang (pengganti guard SSRF di pkg/fetch).
var allowedPlatforms = map[string]bool{"youtube": true, "tiktok": true, "instagram": true}

func Supported(platform string) bool { return allowedPlatforms[platform] }

type Options struct {
	YtDlpPath    string        // default "yt-dlp"
	FFmpegPath   string        // default "ffmpeg"
	FFprobePath  string        // default "ffprobe"
	MaxHeight    int           // resolusi unduhan; default 480 (cukup untuk membaca teks layar)
	MaxFileBytes int64         // default 200 MB
	Timeout      time.Duration // default 6 menit untuk unduh
}

type Tool struct{ opts Options }

func New(opts Options) *Tool {
	if opts.YtDlpPath == "" {
		opts.YtDlpPath = "yt-dlp"
	}
	if opts.FFmpegPath == "" {
		opts.FFmpegPath = "ffmpeg"
	}
	if opts.FFprobePath == "" {
		opts.FFprobePath = "ffprobe"
	}
	if opts.MaxHeight == 0 {
		opts.MaxHeight = 480
	}
	if opts.MaxFileBytes == 0 {
		opts.MaxFileBytes = 200 << 20
	}
	if opts.Timeout == 0 {
		opts.Timeout = 6 * time.Minute
	}
	return &Tool{opts: opts}
}

// Available memeriksa ketiga binary ada di PATH.
func (t *Tool) Available() bool {
	for _, b := range []string{t.opts.YtDlpPath, t.opts.FFmpegPath, t.opts.FFprobePath} {
		if _, err := exec.LookPath(b); err != nil {
			return false
		}
	}
	return true
}

// Info adalah metadata yang dilaporkan yt-dlp sebelum mengunduh.
type Info struct {
	Duration    int // detik
	Title       string
	Description string
	Uploader    string
	HasVideo    bool
}

// Probe membaca metadata tanpa mengunduh, untuk cek durasi & kuota lebih dulu.
func (t *Tool) Probe(ctx context.Context, url string) (Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := t.run(ctx, t.opts.YtDlpPath, "--no-playlist", "--no-warnings", "--skip-download", "-J", url)
	if err != nil {
		return Info{}, classify(err)
	}
	var j struct {
		Duration    float64 `json:"duration"`
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Uploader    string  `json:"uploader"`
		VCodec      string  `json:"vcodec"`
		Formats     []struct {
			VCodec string `json:"vcodec"`
			ACodec string `json:"acodec"`
		} `json:"formats"`
	}
	if err := json.Unmarshal(out, &j); err != nil {
		return Info{}, fmt.Errorf("baca metadata yt-dlp: %w", err)
	}
	info := Info{Duration: int(j.Duration + 0.5), Title: j.Title, Description: j.Description, Uploader: j.Uploader}
	for _, f := range j.Formats {
		if (f.VCodec != "" && f.VCodec != "none") || (f.ACodec != "" && f.ACodec != "none") {
			info.HasVideo = true
			break
		}
	}
	if j.VCodec != "" && j.VCodec != "none" {
		info.HasVideo = true
	}
	return info, nil
}

// Download mengunduh video ke dir dan mengembalikan path file-nya.
func (t *Tool) Download(ctx context.Context, url, dir string, maxDuration int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, t.opts.Timeout)
	defer cancel()
	format := fmt.Sprintf("bv*[height<=%d]+ba/b[height<=%d]/wv*+ba/w", t.opts.MaxHeight, t.opts.MaxHeight)
	args := []string{"--no-playlist", "--no-warnings", "--no-progress", "--quiet",
		"-f", format, "--merge-output-format", "mp4",
		"--max-filesize", strconv.FormatInt(t.opts.MaxFileBytes, 10),
		"-o", filepath.Join(dir, "video.%(ext)s")}
	if maxDuration > 0 {
		// "<=?" meloloskan video tanpa field durasi (Instagram tidak mengirimnya saat unduh);
		// durasi sudah dicek lewat Probe sebelumnya.
		args = append(args, "--match-filter", fmt.Sprintf("duration <=? %d", maxDuration))
	}
	args = append(args, url)
	if _, err := t.run(ctx, t.opts.YtDlpPath, args...); err != nil {
		return "", classify(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "video.*"))
	sort.Strings(matches)
	for _, m := range matches {
		if !strings.HasSuffix(m, ".part") && !strings.HasSuffix(m, ".json") {
			return m, nil
		}
	}
	// --match-filter yang menolak video tidak menghasilkan error, hanya tidak ada file.
	if maxDuration > 0 {
		return "", ErrTooLong
	}
	return "", ErrNoVideo
}

// Duration membaca durasi file (detik) lewat ffprobe.
func (t *Tool) Duration(ctx context.Context, path string) (float64, error) {
	out, err := t.run(ctx, t.opts.FFprobePath, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

// ExtractAudio membuat MP3 mono 16 kHz 32 kbps: 30 menit ≈ 7 MB, aman di bawah batas 25 MB Whisper.
func (t *Tool) ExtractAudio(ctx context.Context, videoPath, dir string) (string, error) {
	out := filepath.Join(dir, "audio.mp3")
	if _, err := t.run(ctx, t.opts.FFmpegPath, "-y", "-loglevel", "error", "-i", videoPath, "-vn", "-ac", "1", "-ar", "16000", "-b:a", "32k", out); err != nil {
		return "", fmt.Errorf("ambil audio: %w", err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() < 1024 {
		return "", fmt.Errorf("video tanpa suara")
	}
	return out, nil
}

// ExtractFrames mengambil n frame tersebar merata (melewati 5% awal & akhir), lebar 768 px, JPEG.
func (t *Tool) ExtractFrames(ctx context.Context, videoPath, dir string, duration float64, n int) ([]string, error) {
	if n <= 0 || duration <= 0 {
		return nil, nil
	}
	var paths []string
	for i := 0; i < n; i++ {
		ts := duration * (0.05 + 0.9*float64(i)/float64(max(n-1, 1)))
		if n == 1 {
			ts = duration / 2
		}
		p := filepath.Join(dir, fmt.Sprintf("frame-%02d.jpg", i))
		if _, err := t.run(ctx, t.opts.FFmpegPath, "-y", "-loglevel", "error", "-ss", strconv.FormatFloat(ts, 'f', 2, 64),
			"-i", videoPath, "-frames:v", "1", "-vf", "scale=768:-2", "-q:v", "4", p); err != nil {
			continue
		}
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

func (t *Tool) run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrToolMissing
		}
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(bin), err, msg)
	}
	return stdout.Bytes(), nil
}

// classify memetakan pesan yt-dlp yang umum ke error yang bisa ditangani pemanggil.
func classify(err error) error {
	if errors.Is(err, ErrToolMissing) {
		return err
	}
	m := strings.ToLower(err.Error())
	switch {
	case strings.Contains(m, "login") || strings.Contains(m, "cookies") || strings.Contains(m, "sign in") || strings.Contains(m, "private"):
		return fmt.Errorf("%w: %v", ErrLoginNeeded, err)
	case strings.Contains(m, "no video formats") || strings.Contains(m, "there is no video"):
		return fmt.Errorf("%w: %v", ErrNoVideo, err)
	}
	return err
}
