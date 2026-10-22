package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"vaulty/internal/model"
	"vaulty/internal/repository"
	"vaulty/pkg/ai"
)

// AnswerService: Vee menjawab dari simpanan user dengan citation tervalidasi (ADR-009), streaming.
type AnswerService struct {
	repos               *repository.Repos
	search              *SearchService
	items               *ItemService
	chat                ai.Chat
	ent                 *EntitlementService
	modelFree, modelPro string
	aiOn                bool
}

func NewAnswerService(repos *repository.Repos, search *SearchService, items *ItemService, chat ai.Chat, ent *EntitlementService, modelFree, modelPro string, aiOn bool) *AnswerService {
	return &AnswerService{repos: repos, search: search, items: items, chat: chat, ent: ent, modelFree: modelFree, modelPro: modelPro, aiOn: aiOn}
}

// Events yang dikirim ke SSE.
type ChatSink interface {
	Meta(conversationID, userMessageID int64) error
	Token(text string) error
	Citations(c []model.Citation) error
	Done(fromNotes bool, assistantMessageID int64) error
}

type ChatInput struct {
	ConversationID *int64
	Message        string
	Mode           string // notes|general
}

const (
	topK          = 12
	historyTurns  = 6
	maxChunkChars = 1200
)

func (s *AnswerService) Chat(ctx context.Context, u model.User, in ChatInput, sink ChatSink) error {
	if !s.aiOn {
		return model.ErrAIDisabled
	}
	in.Message = strings.TrimSpace(in.Message)
	if in.Message == "" {
		return model.Invalid("pesan kosong")
	}
	if in.Mode != "general" {
		in.Mode = "notes"
	}
	if err := s.ent.Consume(ctx, u, MetricQuestions, 1); err != nil {
		return err
	}

	// Percakapan
	var conv model.Conversation
	var err error
	if in.ConversationID != nil {
		conv, err = s.repos.Chats.Conversation(ctx, u.ID, *in.ConversationID)
	} else {
		conv, err = s.repos.Chats.CreateConversation(ctx, u.ID, "")
	}
	if err != nil {
		return err
	}
	history, _ := s.repos.Chats.Messages(ctx, u.ID, conv.ID, historyTurns*2)
	userMsg, err := s.repos.Chats.AddMessage(ctx, u.ID, model.Message{ConversationID: conv.ID, Role: "user", Mode: in.Mode, Content: in.Message})
	if err != nil {
		return err
	}
	if err := sink.Meta(conv.ID, userMsg.ID); err != nil {
		return err
	}

	modelName := s.modelFree
	if u.Plan == "pro" {
		modelName = s.modelPro
	}
	turns := make([]ai.Message, 0, len(history))
	for _, h := range history {
		turns = append(turns, ai.Message{Role: h.Role, Content: ai.Truncate(h.Content, 1500)})
	}

	if in.Mode == "general" {
		resp, err := s.chat.Stream(ctx, ai.ChatRequest{Model: modelName, System: answerGeneralPrompt, Messages: append(turns, ai.Message{Role: "user", Content: ai.Truncate(in.Message, 4000)}), MaxTokens: 700}, sink.Token)
		if err != nil {
			return fmt.Errorf("llm: %w", err)
		}
		fromNotes := false
		am, err := s.repos.Chats.AddMessage(ctx, u.ID, model.Message{ConversationID: conv.ID, Role: "assistant", Mode: "general", Content: strings.TrimSpace(resp.Content), FromNotes: &fromNotes})
		if err != nil {
			return err
		}
		return sink.Done(false, am.ID)
	}

	// Mode notes: cari chunk; pertanyaan sebelumnya ikut jadi query supaya follow-up punya konteks.
	query := in.Message
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			query = history[i].Content + " " + query
			break
		}
	}
	hits, err := s.search.Chunks(ctx, u, query, topK)
	if err != nil {
		return err
	}
	sources := formatSources(hits)
	userContent := sources + "\n\nPERTANYAAN:\n" + ai.Truncate(in.Message, 4000)

	// Streaming dengan penahanan: baris "CITATIONS:" tidak boleh bocor ke user.
	hold := &holdback{marker: "CITATIONS:", emit: sink.Token}
	resp, err := s.chat.Stream(ctx, ai.ChatRequest{Model: modelName, System: answerNotesPrompt, Messages: append(turns, ai.Message{Role: "user", Content: userContent}), MaxTokens: 900, Temperature: 0.2}, hold.Write)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	answer, cits := parseCitations(resp.Content, hits)
	if err := hold.Flush(answer); err != nil {
		return err
	}
	// Lengkapi kartu item untuk citations (dedup per item untuk tampilan tetap semua chunk disimpan).
	for i := range cits {
		if h, ok := findHit(hits, cits[i].ChunkID); ok {
			card := s.items.Card(ctx, h.Item)
			cits[i].Item = &card
		}
	}
	fromNotes := len(cits) > 0
	if err := sink.Citations(cits); err != nil {
		return err
	}
	am, err := s.repos.Chats.AddMessage(ctx, u.ID, model.Message{ConversationID: conv.ID, Role: "assistant", Mode: "notes", Content: answer, Citations: cits, FromNotes: &fromNotes})
	if err != nil {
		return err
	}
	return sink.Done(fromNotes, am.ID)
}

func formatSources(hits []model.ChunkHit) string {
	if len(hits) == 0 {
		return "SUMBER: (tidak ada simpanan yang cocok)"
	}
	var sb strings.Builder
	sb.WriteString("SUMBER:")
	for _, h := range hits {
		fmt.Fprintf(&sb, "\n\n[chunk_id=%d] item=%d", h.Chunk.ID, h.Item.ID)
		if h.Item.Title != nil && *h.Item.Title != "" {
			fmt.Fprintf(&sb, " judul=%q", *h.Item.Title)
		}
		if h.Item.Platform != nil {
			fmt.Fprintf(&sb, " platform=%s", *h.Item.Platform)
		}
		if h.Item.SourceURL != nil {
			fmt.Fprintf(&sb, " url=%s", *h.Item.SourceURL)
		}
		fmt.Fprintf(&sb, " disimpan=%s\n%s", h.Item.CreatedAt.Format("2006-01-02"), ai.Truncate(h.Chunk.Text, maxChunkChars))
	}
	return sb.String()
}

var (
	citLine   = regexp.MustCompile(`(?is)\n?[\s*]*citations[\s*]*:[\s*]*(\[.*\])?\s*$`)
	citInline = regexp.MustCompile(`\[chunk_id=\d+\]`)
)

// parseCitations memisahkan jawaban dan daftar citation; chunk_id di luar hits dibuang (ADR-009).
func parseCitations(content string, hits []model.ChunkHit) (string, []model.Citation) {
	content = strings.TrimSpace(content)
	answer := content
	var raw []struct {
		ChunkID any    `json:"chunk_id"`
		Quote   string `json:"quote"`
	}
	if m := citLine.FindStringSubmatchIndex(content); m != nil {
		answer = strings.TrimSpace(content[:m[0]])
		if m[2] >= 0 {
			_ = ai.ParseJSONLoose(content[m[2]:m[3]], &raw)
		}
	}
	answer = strings.TrimSpace(citInline.ReplaceAllString(answer, ""))
	out := []model.Citation{}
	seen := map[int64]bool{}
	for _, r := range raw {
		id := toInt64(r.ChunkID)
		if id == 0 || seen[id] {
			continue
		}
		h, ok := findHit(hits, id)
		if !ok {
			continue
		}
		seen[id] = true
		q := strings.TrimSpace(r.Quote)
		if q == "" {
			q = ai.Truncate(h.Chunk.Text, 200)
		}
		out = append(out, model.Citation{ItemID: h.Item.ID, ChunkID: id, Quote: ai.Truncate(q, 300)})
		if len(out) >= 6 {
			break
		}
	}
	return answer, out
}

func findHit(hits []model.ChunkHit, chunkID int64) (model.ChunkHit, bool) {
	for _, h := range hits {
		if h.Chunk.ID == chunkID {
			return h, true
		}
	}
	return model.ChunkHit{}, false
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		var n int64
		_, _ = fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}

// holdback menyalurkan token ke client tetapi menahan ekor teks supaya penanda CITATIONS tidak terlihat.
type holdback struct {
	marker  string
	emit    func(string) error
	buf     strings.Builder
	sent    int // jumlah rune yang sudah dikirim
	stopped bool
}

func (h *holdback) Write(delta string) error {
	if h.stopped {
		h.buf.WriteString(delta)
		return nil
	}
	h.buf.WriteString(delta)
	all := h.buf.String()
	if i := strings.Index(strings.ToUpper(all), h.marker); i >= 0 {
		h.stopped = true
		// buang spasi/newline/bintang markdown tepat sebelum marker
		for i > 0 && strings.ContainsRune(" \n\t*", rune(all[i-1])) {
			i--
		}
		return h.emitUpTo(i)
	}
	// kirim semua kecuali ekor sepanjang marker+2 (bisa saja awal marker yang terpotong)
	safe := len(all) - (len(h.marker) + 2)
	return h.emitUpTo(safe)
}

func (h *holdback) emitUpTo(byteIdx int) error {
	all := h.buf.String()
	if byteIdx <= h.sent || byteIdx <= 0 {
		return nil
	}
	// jangan memotong di tengah rune
	for byteIdx < len(all) && byteIdx > 0 && (all[byteIdx]&0xC0) == 0x80 {
		byteIdx--
	}
	if byteIdx <= h.sent {
		return nil
	}
	chunk := all[h.sent:byteIdx]
	h.sent = byteIdx
	return h.emit(chunk)
}

// Flush mengirim sisa jawaban final (hasil parse) yang belum terkirim.
func (h *holdback) Flush(finalAnswer string) error {
	sentText := h.buf.String()
	if h.sent > len(sentText) {
		h.sent = len(sentText)
	}
	sentText = sentText[:h.sent]
	if strings.HasPrefix(sentText, finalAnswer) {
		return nil // sudah terkirim semua (mis. beda whitespace di ekor)
	}
	if strings.HasPrefix(finalAnswer, sentText) {
		rest := finalAnswer[len(sentText):]
		if rest != "" {
			return h.emit(rest)
		}
		return nil
	}
	// Jawaban final berbeda dari yang sudah terkirim (jarang: marker inline dibersihkan) → kirim sisanya apa adanya.
	if len(finalAnswer) > len(sentText) {
		return h.emit(finalAnswer[len(sentText):])
	}
	return nil
}

var ErrNoAnswer = errors.New("tidak ada jawaban")
