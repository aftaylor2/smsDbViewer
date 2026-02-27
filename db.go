package main

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	appleEpochOffset = 978307200
	messagesPageSize = 200
)

type Conversation struct {
	ChatID        int
	Identifier    string
	DisplayName   string
	Participants  []string
	ServiceName   string
	FirstMsgDate  time.Time
	LastMsgDate   time.Time
	MessageCount  int
	SentCount     int
	ReceivedCount int
	Style         int
}

type AttachmentInfo struct {
	TypeLabel string // e.g. "photo", "PDF", "video"
	Filename  string // e.g. "IMG_1234.jpeg"
	Size      int64  // bytes
}

func (a AttachmentInfo) String() string {
	parts := []string{a.TypeLabel}
	if a.Filename != "" {
		parts = append(parts, a.Filename)
	}
	if a.Size > 0 {
		parts = append(parts, formatBytes(a.Size))
	}
	return "[" + strings.Join(parts, " — ") + "]"
}

type Message struct {
	ROWID       int
	Text        string
	Date        time.Time
	IsFromMe    bool
	Sender      string
	Service     string
	Attachments []AttachmentInfo
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// attachmentLabel returns a human-friendly label from a mime_type string.
func attachmentLabel(mime string) string {
	mime = strings.TrimSpace(strings.ToLower(mime))
	switch {
	case strings.HasPrefix(mime, "image/"):
		sub := strings.TrimPrefix(mime, "image/")
		switch sub {
		case "jpeg", "jpg":
			return "photo"
		case "png":
			return "image"
		case "heic", "heif":
			return "photo"
		case "gif":
			return "GIF"
		case "webp":
			return "image"
		default:
			return "image"
		}
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case mime == "application/pdf":
		return "PDF"
	case mime == "text/vcard":
		return "contact card"
	case mime == "text/x-markdown":
		return "markdown"
	case strings.Contains(mime, "zip") || strings.Contains(mime, "archive"):
		return "archive"
	case strings.Contains(mime, "iwork-numbers"):
		return "Numbers spreadsheet"
	case strings.Contains(mime, "iwork-pages"):
		return "Pages document"
	case strings.Contains(mime, "iwork-keynote"):
		return "Keynote presentation"
	case mime == "":
		return "attachment"
	default:
		return "file"
	}
}

// parseAttachments splits a GROUP_CONCAT result into AttachmentInfo structs.
// Each attachment is separated by ";;", fields within by "||".
// Format: mime_type||transfer_name||total_bytes
func parseAttachments(raw string) []AttachmentInfo {
	if raw == "" {
		return nil
	}
	entries := strings.Split(raw, ";;")
	var attachments []AttachmentInfo
	for _, entry := range entries {
		fields := strings.SplitN(entry, "||", 3)
		mime := ""
		if len(fields) > 0 {
			mime = fields[0]
		}
		name := ""
		if len(fields) > 1 {
			name = fields[1]
		}
		var size int64
		if len(fields) > 2 {
			size, _ = strconv.ParseInt(fields[2], 10, 64)
		}
		// Skip empty entries from LEFT JOIN producing null rows
		if mime == "" && name == "" && size == 0 {
			continue
		}
		attachments = append(attachments, AttachmentInfo{
			TypeLabel: attachmentLabel(mime),
			Filename:  name,
			Size:      size,
		})
	}
	return attachments
}

type ChatAttachment struct {
	ROWID     int
	FilePath  string // full path from attachment.filename
	Filename  string // transfer_name (display name)
	MimeType  string
	TypeLabel string
	Size      int64
	Date      time.Time
	IsFromMe  bool
	Sender    string
}

type SearchResult struct {
	Message
	ChatID   int
	ChatName string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func appleNanosToTime(nanos int64) time.Time {
	if nanos == 0 {
		return time.Time{}
	}
	unixSeconds := nanos/1_000_000_000 + appleEpochOffset
	remainder := nanos % 1_000_000_000
	return time.Unix(unixSeconds, remainder)
}

func (s *Store) FetchConversations() ([]Conversation, error) {
	query := `
		SELECT
			c.ROWID,
			c.chat_identifier,
			COALESCE(c.display_name, ''),
			c.service_name,
			COALESCE(c.style, 0),
			COALESCE(sub.first_date, 0),
			COALESCE(sub.last_date, 0),
			COALESCE(sub.msg_count, 0),
			COALESCE(sub.sent_count, 0),
			COALESCE(sub.recv_count, 0)
		FROM chat c
		LEFT JOIN (
			SELECT
				cmj.chat_id,
				MIN(m.date) AS first_date,
				MAX(m.date) AS last_date,
				COUNT(*) AS msg_count,
				SUM(m.is_from_me) AS sent_count,
				SUM(CASE WHEN m.is_from_me = 0 THEN 1 ELSE 0 END) AS recv_count
			FROM chat_message_join cmj
			JOIN message m ON cmj.message_id = m.ROWID
			GROUP BY cmj.chat_id
		) sub ON sub.chat_id = c.ROWID
		ORDER BY sub.last_date DESC
	`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conversations []Conversation
	for rows.Next() {
		var conv Conversation
		var firstDate, lastDate int64
		err := rows.Scan(
			&conv.ChatID,
			&conv.Identifier,
			&conv.DisplayName,
			&conv.ServiceName,
			&conv.Style,
			&firstDate,
			&lastDate,
			&conv.MessageCount,
			&conv.SentCount,
			&conv.ReceivedCount,
		)
		if err != nil {
			return nil, err
		}
		conv.FirstMsgDate = appleNanosToTime(firstDate)
		conv.LastMsgDate = appleNanosToTime(lastDate)
		conversations = append(conversations, conv)
	}

	for i := range conversations {
		participants, err := s.fetchParticipants(conversations[i].ChatID)
		if err != nil {
			return nil, err
		}
		conversations[i].Participants = participants
	}

	return conversations, nil
}

func (s *Store) fetchParticipants(chatID int) ([]string, error) {
	query := `
		SELECT h.id
		FROM handle h
		JOIN chat_handle_join chj ON chj.handle_id = h.ROWID
		WHERE chj.chat_id = ?
	`
	rows, err := s.db.Query(query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		participants = append(participants, p)
	}
	return participants, nil
}

func (s *Store) FetchMessages(chatID int, cursor int, pageSize int) ([]Message, error) {
	if pageSize <= 0 {
		pageSize = messagesPageSize
	}

	var query string
	var args []interface{}

	if cursor == 0 {
		query = `
			SELECT m.ROWID, COALESCE(m.text, ''), m.date, m.is_from_me,
			       COALESCE(h.id, ''), COALESCE(m.service, ''),
			       COALESCE(GROUP_CONCAT(COALESCE(a.mime_type,'') || '||' || COALESCE(a.transfer_name,'') || '||' || COALESCE(a.total_bytes,0), ';;'), '')
			FROM message m
			JOIN chat_message_join cmj ON cmj.message_id = m.ROWID
			LEFT JOIN handle h ON m.handle_id = h.ROWID
			LEFT JOIN message_attachment_join maj ON maj.message_id = m.ROWID
			LEFT JOIN attachment a ON maj.attachment_id = a.ROWID
			WHERE cmj.chat_id = ?
			GROUP BY m.ROWID
			ORDER BY m.date DESC
			LIMIT ?
		`
		args = []interface{}{chatID, pageSize}
	} else {
		query = `
			SELECT m.ROWID, COALESCE(m.text, ''), m.date, m.is_from_me,
			       COALESCE(h.id, ''), COALESCE(m.service, ''),
			       COALESCE(GROUP_CONCAT(COALESCE(a.mime_type,'') || '||' || COALESCE(a.transfer_name,'') || '||' || COALESCE(a.total_bytes,0), ';;'), '')
			FROM message m
			JOIN chat_message_join cmj ON cmj.message_id = m.ROWID
			LEFT JOIN handle h ON m.handle_id = h.ROWID
			LEFT JOIN message_attachment_join maj ON maj.message_id = m.ROWID
			LEFT JOIN attachment a ON maj.attachment_id = a.ROWID
			WHERE cmj.chat_id = ? AND m.ROWID < ?
			GROUP BY m.ROWID
			ORDER BY m.date DESC
			LIMIT ?
		`
		args = []interface{}{chatID, cursor, pageSize}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var dateNanos int64
		var attachRaw string
		err := rows.Scan(&msg.ROWID, &msg.Text, &dateNanos, &msg.IsFromMe, &msg.Sender, &msg.Service, &attachRaw)
		if err != nil {
			return nil, err
		}
		msg.Date = appleNanosToTime(dateNanos)
		msg.Attachments = parseAttachments(attachRaw)
		messages = append(messages, msg)
	}

	// Reverse to chronological order
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

func (s *Store) FetchAllMessages(chatID int) ([]Message, error) {
	query := `
		SELECT m.ROWID, COALESCE(m.text, ''), m.date, m.is_from_me,
		       COALESCE(h.id, ''), COALESCE(m.service, ''),
		       COALESCE(GROUP_CONCAT(COALESCE(a.mime_type,'') || '||' || COALESCE(a.transfer_name,'') || '||' || COALESCE(a.total_bytes,0), ';;'), '')
		FROM message m
		JOIN chat_message_join cmj ON cmj.message_id = m.ROWID
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		LEFT JOIN message_attachment_join maj ON maj.message_id = m.ROWID
		LEFT JOIN attachment a ON maj.attachment_id = a.ROWID
		WHERE cmj.chat_id = ?
		GROUP BY m.ROWID
		ORDER BY m.date ASC
	`

	rows, err := s.db.Query(query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var dateNanos int64
		var attachRaw string
		err := rows.Scan(&msg.ROWID, &msg.Text, &dateNanos, &msg.IsFromMe, &msg.Sender, &msg.Service, &attachRaw)
		if err != nil {
			return nil, err
		}
		msg.Date = appleNanosToTime(dateNanos)
		msg.Attachments = parseAttachments(attachRaw)
		messages = append(messages, msg)
	}
	return messages, nil
}

func (s *Store) SearchMessages(term string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT m.ROWID, COALESCE(m.text, ''), m.date, m.is_from_me,
		       COALESCE(h.id, ''), COALESCE(m.service, ''),
		       c.ROWID, COALESCE(c.display_name, c.chat_identifier)
		FROM message m
		JOIN chat_message_join cmj ON cmj.message_id = m.ROWID
		JOIN chat c ON cmj.chat_id = c.ROWID
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE m.text LIKE '%' || ? || '%'
		ORDER BY m.date DESC
		LIMIT ?
	`

	rows, err := s.db.Query(query, term, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var dateNanos int64
		err := rows.Scan(&r.ROWID, &r.Text, &dateNanos, &r.IsFromMe, &r.Sender, &r.Service,
			&r.ChatID, &r.ChatName)
		if err != nil {
			return nil, err
		}
		r.Date = appleNanosToTime(dateNanos)
		results = append(results, r)
	}
	return results, nil
}

func expandTilde(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

func (s *Store) FetchChatAttachments(chatID int) ([]ChatAttachment, error) {
	query := `
		SELECT a.ROWID, COALESCE(a.filename, ''), COALESCE(a.transfer_name, ''),
		       COALESCE(a.mime_type, ''), COALESCE(a.total_bytes, 0),
		       m.date, m.is_from_me, COALESCE(h.id, '')
		FROM attachment a
		JOIN message_attachment_join maj ON maj.attachment_id = a.ROWID
		JOIN message m ON maj.message_id = m.ROWID
		JOIN chat_message_join cmj ON cmj.message_id = m.ROWID
		LEFT JOIN handle h ON m.handle_id = h.ROWID
		WHERE cmj.chat_id = ?
		ORDER BY m.date DESC
	`

	rows, err := s.db.Query(query, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var attachments []ChatAttachment
	for rows.Next() {
		var a ChatAttachment
		var dateNanos int64
		err := rows.Scan(&a.ROWID, &a.FilePath, &a.Filename, &a.MimeType, &a.Size,
			&dateNanos, &a.IsFromMe, &a.Sender)
		if err != nil {
			return nil, err
		}
		a.Date = appleNanosToTime(dateNanos)
		a.TypeLabel = attachmentLabel(a.MimeType)
		a.FilePath = expandTilde(a.FilePath)
		attachments = append(attachments, a)
	}
	return attachments, nil
}
