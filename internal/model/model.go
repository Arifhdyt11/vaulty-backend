// Package model berisi struktur domain dan error yang dipakai lintas layer.
package model

import (
	"encoding/json"
	"time"
)

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	AvatarURL *string   `json:"avatar_url"`
	Timezone  string    `json:"timezone"`
	Plan      string    `json:"plan" enum:"free,pro"`
	Role      string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// Location mengembalikan zona waktu user; jatuh ke Asia/Jakarta bila tidak valid.
func (u User) Location() *time.Location {
	if loc, err := time.LoadLocation(u.Timezone); err == nil {
		return loc
	}
	loc, _ := time.LoadLocation("Asia/Jakarta")
	return loc
}

type Session struct {
	ID               int64
	UserID           int64
	AccessToken      string // hanya terisi saat baru dibuat
	RefreshToken     string // hanya terisi saat baru dibuat
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

type Quota struct {
	Metric   string    `json:"metric"`
	Limit    int       `json:"limit" doc:"-1 berarti tanpa batas"`
	Used     int       `json:"used"`
	ResetsAt time.Time `json:"resets_at"`
}

type Stage struct {
	Status string `json:"status" enum:"pending,running,done,failed,skipped"`
	Error  string `json:"error,omitempty"`
}

type Ingest struct {
	Fetch      Stage `json:"fetch"`
	Transcribe Stage `json:"transcribe"`
	Understand Stage `json:"understand"`
	Index      Stage `json:"index"`
}

type Entities struct {
	People   []string `json:"people"`
	Places   []string `json:"places"`
	Products []string `json:"products"`
	Dates    []string `json:"dates"`
}

type FileInfo struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
}

// Item adalah representasi penuh (GET /items/{id}). Field *Key internal, tidak keluar ke JSON.
type Item struct {
	ID             int64           `json:"id"`
	UserID         int64           `json:"-"`
	Kind           string          `json:"kind" enum:"url,image,text,audio,file"`
	Status         string          `json:"status" enum:"queued,processing,ready,partial,failed"`
	SourceURL      *string         `json:"source_url"`
	CanonicalURL   *string         `json:"canonical_url"`
	Platform       *string         `json:"platform"`
	ContentType    *string         `json:"content_type,omitempty"`
	Title          *string         `json:"title"`
	Description    *string         `json:"description"`
	ThumbnailKey   *string         `json:"-"`
	ThumbnailURL   *string         `json:"thumbnail_url"`
	UserNote       string          `json:"user_note"`
	Body           string          `json:"body,omitempty"`
	Summary        *string         `json:"summary"`
	KeyPoints      []string        `json:"key_points"`
	Entities       Entities        `json:"entities"`
	Tags           []string        `json:"tags"`
	AutoTags       []string        `json:"auto_tags"`
	Language       *string         `json:"language,omitempty"`
	ContentText    *string         `json:"content_text,omitempty"`
	FileKey        *string         `json:"-"`
	FileName       *string         `json:"-"`
	FileMime       *string         `json:"-"`
	FileSize       *int64          `json:"-"`
	File           *FileInfo       `json:"file"`
	AttachmentKey  *string         `json:"-"`
	DurationSec    int             `json:"duration_sec,omitempty"`
	PublishedAt    *time.Time      `json:"published_at,omitempty"`
	ContentVersion int32           `json:"-"`
	Ingest         Ingest          `json:"ingest"`
	RemindersCount int             `json:"reminders_count"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	EntitiesRaw    json.RawMessage `json:"-"`
}

// Card adalah bentuk ringkas untuk list & citation.
type Card struct {
	ID             int64     `json:"id"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	SourceURL      *string   `json:"source_url"`
	Platform       *string   `json:"platform"`
	Title          *string   `json:"title"`
	ThumbnailURL   *string   `json:"thumbnail_url"`
	UserNote       string    `json:"user_note"`
	Summary        *string   `json:"summary"`
	Tags           []string  `json:"tags"`
	AutoTags       []string  `json:"auto_tags"`
	RemindersCount int       `json:"reminders_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (i Item) Card() Card {
	return Card{
		ID: i.ID, Kind: i.Kind, Status: i.Status, SourceURL: i.SourceURL, Platform: i.Platform,
		Title: i.Title, ThumbnailURL: i.ThumbnailURL, UserNote: i.UserNote, Summary: i.Summary,
		Tags: nz(i.Tags), AutoTags: nz(i.AutoTags), RemindersCount: i.RemindersCount,
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt,
	}
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type Chunk struct {
	ID     int64
	ItemID int64
	Seq    int
	Source string
	Text   string
}

type SearchHit struct {
	Item    Card    `json:"item"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

type ChunkHit struct {
	Chunk Chunk
	Item  Item
	Score float64
}

type Citation struct {
	ItemID  int64  `json:"item_id"`
	ChunkID int64  `json:"chunk_id"`
	Quote   string `json:"quote"`
	Item    *Card  `json:"item,omitempty"`
}

type Conversation struct {
	ID        int64     `json:"id"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
	ID             int64      `json:"id"`
	ConversationID int64      `json:"conversation_id"`
	Role           string     `json:"role" enum:"user,assistant"`
	Mode           string     `json:"mode" enum:"notes,general"`
	Content        string     `json:"content"`
	Citations      []Citation `json:"citations"`
	FromNotes      *bool      `json:"from_notes,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

type Reminder struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"-"`
	ItemID      *int64    `json:"item_id"`
	Item        *Card     `json:"item,omitempty"`
	Text        string    `json:"text"`
	Origin      string    `json:"origin" enum:"user,item,ai"`
	RemindAt    time.Time `json:"remind_at"`
	Repeat      string    `json:"repeat" enum:",daily,weekly,monthly,yearly"`
	LeadMinutes []int32   `json:"lead_minutes"`
	NotifyAt    time.Time `json:"notify_at"`
	NotifyKind  string    `json:"notify_kind" enum:"event,lead,snooze"`
	Status      string    `json:"status" enum:"suggested,pending,sending,sent,done,cancelled,failed"`
	CreatedAt   time.Time `json:"created_at"`
}

type Device struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"-"`
	Platform   string    `json:"platform" enum:"ios,android,web"`
	PushToken  string    `json:"push_token"`
	AppVersion string    `json:"app_version"`
	CreatedAt  time.Time `json:"created_at"`
}

type ExportJob struct {
	ID        int64     `json:"job_id"`
	Status    string    `json:"status" enum:"pending,ready,failed"`
	URL       *string   `json:"url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type PlanLimit struct {
	Plan   string
	Metric string
	Period string // day|month
	Limit  int
}

type Subscription struct {
	UserID      int64
	Plan        string
	Provider    string
	ProviderRef string
	ProductID   string
	Status      string
	PeriodEnd   *time.Time
	Raw         json.RawMessage
}

// Briefing adalah Siaran Vee satu hari (zona waktu user) berisi beberapa bab audio (ADR-016).
type Briefing struct {
	ID               int64             `json:"id"`
	UserID           int64             `json:"-"`
	Date             string            `json:"date" doc:"YYYY-MM-DD di zona waktu user"`
	Status           string            `json:"status" enum:"building,ready,failed,empty" doc:"empty = belum ada simpanan siap"`
	Greeting         string            `json:"greeting"`
	Chapters         []BriefingChapter `json:"chapters"`
	TotalDurationSec int               `json:"total_duration_sec" doc:"Jumlah durasi bab yang sudah siap"`
	GeneratedAt      time.Time         `json:"generated_at"`
	CreatedAt        time.Time         `json:"-"`
}

// BriefingChapter: satu simpanan yang dibacakan Vee. Field *Key internal, tidak keluar ke JSON.
type BriefingChapter struct {
	ID          int64   `json:"-"`
	Seq         int     `json:"-"`
	Index       int     `json:"index" doc:"Mulai 1"`
	ItemID      int64   `json:"-"`
	Item        Card    `json:"item"`
	Label       string  `json:"label" doc:"Tag utama / platform, huruf kalimat"`
	Title       string  `json:"title"`
	Quote       string  `json:"quote" doc:"Satu kalimat pendek dari poin penting"`
	Script      string  `json:"script" doc:"Naskah lisan; kosong selama dibuat"`
	AudioKey    *string `json:"-"`
	AudioURL    *string `json:"audio_url" doc:"Signed URL audio/mp4 (AAC); null selama dibuat"`
	DurationSec int     `json:"duration_sec" doc:"0 selama dibuat"`
	Status      string  `json:"status" enum:"pending,ready,failed"`
	Error       string  `json:"-"`
}
