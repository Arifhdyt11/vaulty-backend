package model

import "time"

// Pola ulang reminder (kosong = sekali).
const (
	RepeatNone    = ""
	RepeatDaily   = "daily"
	RepeatWeekly  = "weekly"
	RepeatMonthly = "monthly"
	RepeatYearly  = "yearly"
)

// Jenis notifikasi berikutnya: acara itu sendiri, pengingat bertahap sebelum acara, atau hasil tunda.
const (
	NotifyEvent  = "event"
	NotifyLead   = "lead"
	NotifySnooze = "snooze"
)

const (
	ReminderPending   = "pending"
	ReminderSending   = "sending"
	ReminderSent      = "sent"
	ReminderDone      = "done"
	ReminderCancelled = "cancelled"
	ReminderFailed    = "failed"
)

type Reminder struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"-"`
	NoteID      *int64    `json:"note_id,omitempty"`
	Text        string    `json:"text"`
	RemindAt    time.Time `json:"remind_at" doc:"Waktu acara (untuk reminder berulang: kejadian berikutnya)"`
	Repeat      string    `json:"repeat" enum:",daily,weekly,monthly,yearly"`
	LeadMinutes []int     `json:"lead_minutes" doc:"Pengingat bertahap sebelum acara, dalam menit (mis. 10080 = H-7)"`
	NotifyAt    time.Time `json:"notify_at" doc:"Waktu notifikasi berikutnya"`
	NotifyKind  string    `json:"notify_kind" enum:"event,lead,snooze"`
	Status      string    `json:"status" enum:"pending,sending,sent,done,cancelled,failed"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateReminderInput struct {
	Text        string
	RemindAt    time.Time
	Repeat      string
	LeadMinutes []int
	NoteID      *int64
}
