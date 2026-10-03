package model

import (
	"io"
	"time"
)

const (
	NoteTypeNote     = "note"
	NoteTypeLink     = "link"
	NoteTypeCommand  = "command"
	NoteTypeDocument = "document"
)

const (
	IndexPending = "pending"
	IndexDone    = "done"
	IndexFailed  = "failed"
)

type Note struct {
	ID          int64          `json:"id"`
	Type        string         `json:"type" example:"command"`
	Title       *string        `json:"title"`
	Body        string         `json:"body" doc:"Untuk type command, ditampilkan verbatim (ADR-010)"`
	URL         *string        `json:"url"`
	Tags        []string       `json:"tags"`
	AutoTags    []string       `json:"auto_tags" doc:"Tag hasil AI, terpisah dari tag user"`
	Project     *string        `json:"project"`
	Metadata    map[string]any `json:"metadata"`
	File        *File          `json:"file,omitempty"`
	IndexStatus string         `json:"index_status" enum:"pending,done,failed"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`

	// ContentVersion dipakai worker (tidak dikirim ke client).
	ContentVersion int32 `json:"-"`
}

type File struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
	URL  string `json:"url" doc:"Path unduh, relatif terhadap base URL API (mis. /notes/12/file -> /api/v1/notes/12/file)"`
}

type TypeCount struct {
	Type  string `json:"type"`
	Total int64  `json:"total"`
}

type SearchHit struct {
	Note    Note    `json:"note"`
	Score   float64 `json:"score" doc:"Skor RRF; makin besar makin relevan"`
	Snippet string  `json:"snippet,omitempty" doc:"Potongan isi dokumen yang cocok (HTML <b>…</b>)"`
}

// NoteFilter dipakai list dan search.
type NoteFilter struct {
	Type, Tag, Project string
}

type CreateNoteInput struct {
	Type     string
	Title    string
	Body     string
	URL      string
	Tags     []string
	Project  string
	Metadata map[string]any
}

// UpdateNoteInput: field nil berarti tidak diubah; string kosong berarti dikosongkan.
type UpdateNoteInput struct {
	// UpdatedAt: waktu edit di client. Jika lebih lama dari versi server, perubahan
	// diabaikan (last-write-wins, ADR-009), mis. saat outbox offline dikirim ulang.
	UpdatedAt *time.Time
	Type      *string
	Title     *string
	Body      *string
	URL       *string
	Tags      *[]string
	Project   *string
	Metadata  map[string]any
}

type UploadNoteInput struct {
	Type     string
	Title    string
	Body     string
	Tags     []string
	Project  string
	Filename string
	Mime     string
	File     io.Reader
}

// NoteResult adalah hasil create/update beserta peringatan untuk user.
type NoteResult struct {
	Note     Note     `json:"note"`
	Warnings []string `json:"warnings" doc:"Peringatan, mis. terdeteksi pola credential"`
}

// IndexableNote adalah data yang dibutuhkan worker untuk indexing.
type IndexableNote struct {
	ID, UserID     int64
	Type           string
	Title, URL     string
	Body           string
	Tags           []string
	Project        string
	FileKey        string
	FileName       string
	FileMime       string
	ContentText    string
	HasContentText bool
	ContentVersion int32
}
