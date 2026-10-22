package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/ai"
	"vaulty/pkg/extract"
	"vaulty/pkg/media"
)

// transcribeVideo: isi video YouTube/TikTok/Instagram dibaca lewat transkrip suara dan
// frame (ADR-015). Hanya Pro; kuota stt_minutes dipotong sesuai durasi. Kegagalan platform
// (login, bukan video) membuat tahap ini "skipped" supaya item tetap siap dari caption.
func (s *IngestService) transcribeVideo(ctx context.Context, u model.User, it *model.Item) model.Stage {
	if !s.aiOn {
		return model.Stage{Status: "skipped", Error: "AI nonaktif"}
	}
	rules := RulesFor(u.Plan)
	if rules.STTMaxMinutes == 0 {
		return model.Stage{Status: "skipped", Error: "isi video (transkrip & layar) hanya untuk paket Pro"}
	}
	if s.media == nil || !s.media.Available() {
		return model.Stage{Status: "skipped", Error: "yt-dlp/ffmpeg belum terpasang di server"}
	}
	src := *it.SourceURL
	info, err := s.media.Probe(ctx, src)
	switch {
	case errors.Is(err, media.ErrLoginNeeded):
		return model.Stage{Status: "skipped", Error: "platform meminta login untuk video ini; isi diambil dari caption saja"}
	case errors.Is(err, media.ErrNoVideo):
		return model.Stage{Status: "skipped"}
	case err != nil:
		return model.Stage{Status: "failed", Error: "baca video: " + ai.Truncate(err.Error(), 200)}
	}
	if !info.HasVideo {
		return model.Stage{Status: "skipped"}
	}
	maxMin := min(rules.STTMaxMinutes, s.maxVideoMin)
	if info.Duration > maxMin*60 {
		return model.Stage{Status: "skipped", Error: fmt.Sprintf("video %d menit melebihi batas %d menit; isi diambil dari caption", (info.Duration+59)/60, maxMin)}
	}
	minutes := max(1, int(math.Ceil(float64(info.Duration)/60)))
	if err := s.ent.Consume(ctx, u, MetricSTTMinutes, minutes); err != nil {
		return model.Stage{Status: "skipped", Error: "kuota menit video bulan ini habis"}
	}
	refund := true
	defer func() {
		if refund {
			_ = s.ent.Consume(ctx, u, MetricSTTMinutes, -minutes)
		}
	}()

	dir, err := os.MkdirTemp("", "vaulty-video-*")
	if err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	defer os.RemoveAll(dir)

	start := time.Now()
	videoPath, err := s.media.Download(ctx, src, dir, maxMin*60)
	if err != nil {
		if errors.Is(err, media.ErrLoginNeeded) || errors.Is(err, media.ErrNoVideo) || errors.Is(err, media.ErrTooLong) {
			return model.Stage{Status: "skipped", Error: "video tidak bisa diunduh; isi diambil dari caption"}
		}
		return model.Stage{Status: "failed", Error: "unduh video: " + ai.Truncate(err.Error(), 200)}
	}
	duration, err := s.media.Duration(ctx, videoPath)
	if err != nil || duration <= 0 {
		duration = float64(info.Duration)
	}

	var warnings []string
	transcript := ""
	if audio, err := s.media.ExtractAudio(ctx, videoPath, dir); err != nil {
		warnings = append(warnings, "tanpa suara")
	} else if s.stt != nil {
		f, err := os.Open(audio)
		if err == nil {
			transcript, err = s.stt.Transcribe(ctx, f, filepath.Base(audio), "")
			f.Close()
		}
		if err != nil {
			warnings = append(warnings, "transkrip gagal: "+ai.Truncate(err.Error(), 120))
		}
	}

	screen := ""
	nFrames := min(s.maxFrames, 2+int(duration/20))
	frames, _ := s.media.ExtractFrames(ctx, videoPath, dir, duration, nFrames)
	if len(frames) > 0 {
		screen, err = s.readFrames(ctx, frames)
		if err != nil {
			warnings = append(warnings, "baca layar gagal: "+ai.Truncate(err.Error(), 120))
		}
	}

	transcript, screen = strings.TrimSpace(transcript), strings.TrimSpace(screen)
	if transcript == "" && screen == "" {
		return model.Stage{Status: "failed", Error: "isi video tidak terbaca: " + strings.Join(warnings, "; ")}
	}
	refund = false

	var sb strings.Builder
	if it.ContentText != nil && strings.TrimSpace(*it.ContentText) != "" {
		sb.WriteString(strings.TrimSpace(*it.ContentText))
	}
	if transcript != "" {
		sb.WriteString("\n\n[Transkrip video]\n" + transcript)
	}
	if screen != "" {
		sb.WriteString("\n\n[Isi layar video]\n" + screen)
	}
	text := extract.Clean(sb.String())
	if err := s.repos.Items.SetContentText(ctx, u.ID, it.ID, it.ContentVersion, text, nil); err != nil {
		return model.Stage{Status: "failed", Error: err.Error()}
	}
	it.ContentText = &text

	patch := repository.FetchResultPatch{DurationSec: int(duration + 0.5)}
	// Video tanpa thumbnail (mis. diblokir) memakai frame tengah sebagai gambar sampul.
	if it.ThumbnailKey == nil && len(frames) > 0 {
		if key, err := s.storeFrame(ctx, u.ID, it.ID, frames[len(frames)/2]); err == nil {
			patch.ThumbnailKey = &key
			it.ThumbnailKey = &key
		}
	}
	_ = s.repos.Items.ApplyFetch(ctx, u.ID, it.ID, it.ContentVersion, patch)
	slog.InfoContext(ctx, "video dibaca", "item_id", it.ID, "detik", int(duration), "frame", len(frames), "transkrip", transcript != "", "ms", time.Since(start).Milliseconds())
	return model.Stage{Status: "done", Error: strings.Join(warnings, "; ")}
}

// readFrames mengirim frame berurutan ke model vision dan mengembalikan teks layar + gambaran visual.
func (s *IngestService) readFrames(ctx context.Context, paths []string) (string, error) {
	imgs := make([]ai.Image, 0, len(paths))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		imgs = append(imgs, ai.Image{MIME: "image/jpeg", Data: b})
	}
	if len(imgs) == 0 {
		return "", nil
	}
	resp, err := s.visionAI.Complete(ctx, ai.ChatRequest{Model: s.visionModel, System: videoFramesPrompt, JSON: true, MaxTokens: 1500,
		Messages: []ai.Message{{Role: "user", Content: fmt.Sprintf("%d frame dari satu video, urut dari awal ke akhir.", len(imgs)), Images: imgs}}})
	if err != nil {
		return "", err
	}
	var out struct {
		OnScreenText string `json:"on_screen_text"`
		Visual       string `json:"visual_summary"`
	}
	if err := ai.ParseJSONLoose(resp.Content, &out); err != nil {
		return strings.TrimSpace(resp.Content), nil
	}
	parts := []string{}
	if t := strings.TrimSpace(out.Visual); t != "" {
		parts = append(parts, t)
	}
	if t := strings.TrimSpace(out.OnScreenText); t != "" {
		parts = append(parts, "Teks di layar: "+t)
	}
	return strings.Join(parts, "\n"), nil
}

func (s *IngestService) storeFrame(ctx context.Context, userID, itemID int64, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	key := fmt.Sprintf("u/%d/thumb/%d-%s.jpg", userID, itemID, uuid.NewString()[:8])
	return key, s.store.Put(ctx, key, f, st.Size(), "image/jpeg")
}
