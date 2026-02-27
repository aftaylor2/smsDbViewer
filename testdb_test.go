package main

import (
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestDB creates an in-memory SQLite database with the iMessage schema
// and populates it with test conversations, handles, and messages.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	// Create tables (minimal schema matching chat.db)
	for _, stmt := range []string{
		`CREATE TABLE handle (
			ROWID INTEGER PRIMARY KEY AUTOINCREMENT,
			id TEXT NOT NULL,
			country TEXT,
			service TEXT NOT NULL,
			uncanonicalized_id TEXT,
			person_centric_id TEXT,
			UNIQUE (id, service)
		)`,
		`CREATE TABLE chat (
			ROWID INTEGER PRIMARY KEY AUTOINCREMENT,
			guid TEXT UNIQUE NOT NULL,
			style INTEGER,
			chat_identifier TEXT,
			service_name TEXT,
			display_name TEXT
		)`,
		`CREATE TABLE message (
			ROWID INTEGER PRIMARY KEY AUTOINCREMENT,
			guid TEXT UNIQUE NOT NULL,
			text TEXT,
			handle_id INTEGER DEFAULT 0,
			service TEXT,
			date INTEGER,
			is_from_me INTEGER DEFAULT 0,
			cache_has_attachments INTEGER DEFAULT 0
		)`,
		`CREATE TABLE chat_message_join (
			chat_id INTEGER REFERENCES chat (ROWID),
			message_id INTEGER REFERENCES message (ROWID),
			message_date INTEGER DEFAULT 0,
			PRIMARY KEY (chat_id, message_id)
		)`,
		`CREATE TABLE chat_handle_join (
			chat_id INTEGER REFERENCES chat (ROWID),
			handle_id INTEGER REFERENCES handle (ROWID),
			UNIQUE(chat_id, handle_id)
		)`,
		`CREATE TABLE attachment (
			ROWID INTEGER PRIMARY KEY AUTOINCREMENT,
			guid TEXT UNIQUE NOT NULL,
			original_guid TEXT UNIQUE NOT NULL,
			mime_type TEXT,
			transfer_name TEXT,
			total_bytes INTEGER DEFAULT 0,
			filename TEXT
		)`,
		`CREATE TABLE message_attachment_join (
			message_id INTEGER REFERENCES message (ROWID),
			attachment_id INTEGER REFERENCES attachment (ROWID),
			UNIQUE(message_id, attachment_id)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("failed to create table: %v\nSQL: %s", err, stmt)
		}
	}

	seedTestData(t, db)
	return db
}

// Apple epoch: nanoseconds since 2001-01-01.
// Base timestamp: 2024-06-15 10:00:00 UTC = 740,142,000 seconds from Apple epoch.
const baseAppleNanos = 740_142_000_000_000_000

func seedTestData(t *testing.T, db *sql.DB) {
	t.Helper()

	// --- Handles ---
	handles := []struct {
		id      string
		service string
	}{
		{"+15551234567", "iMessage"},
		{"+15559876543", "iMessage"},
		{"jane@example.com", "iMessage"},
	}
	for _, h := range handles {
		_, err := db.Exec(`INSERT INTO handle (id, service) VALUES (?, ?)`, h.id, h.service)
		if err != nil {
			t.Fatalf("insert handle: %v", err)
		}
	}

	// --- Chats ---
	// Chat 1: 1-on-1 with handle 1
	db.Exec(`INSERT INTO chat (guid, style, chat_identifier, service_name, display_name)
		VALUES ('chat1', 1, '+15551234567', 'iMessage', '')`)
	db.Exec(`INSERT INTO chat_handle_join (chat_id, handle_id) VALUES (1, 1)`)

	// Chat 2: 1-on-1 with handle 3 (email)
	db.Exec(`INSERT INTO chat (guid, style, chat_identifier, service_name, display_name)
		VALUES ('chat2', 1, 'jane@example.com', 'iMessage', '')`)
	db.Exec(`INSERT INTO chat_handle_join (chat_id, handle_id) VALUES (2, 3)`)

	// Chat 3: group chat with handles 1 and 2
	db.Exec(`INSERT INTO chat (guid, style, chat_identifier, service_name, display_name)
		VALUES ('chat3', 16, 'chat100200300', 'iMessage', 'Family Group')`)
	db.Exec(`INSERT INTO chat_handle_join (chat_id, handle_id) VALUES (3, 1)`)
	db.Exec(`INSERT INTO chat_handle_join (chat_id, handle_id) VALUES (3, 2)`)

	// --- Messages for Chat 1 (10 messages, alternating sender) ---
	chat1Msgs := []struct {
		text     string
		fromMe   int
		handleID int
	}{
		{"Hey, how are you?", 1, 0},
		{"I'm good, thanks! How about you?", 0, 1},
		{"Doing great! Want to grab lunch?", 1, 0},
		{"Sure, where?", 0, 1},
		{"How about the new place downtown?", 1, 0},
		{"Sounds good! What time?", 0, 1},
		{"12:30 work for you?", 1, 0},
		{"Perfect, see you there!", 0, 1},
		{"Running 5 mins late", 1, 0},
		{"No worries, I just got here", 0, 1},
	}
	for i, m := range chat1Msgs {
		dateNanos := baseAppleNanos + int64(i)*60_000_000_000 // 1 minute apart
		guid := fmt.Sprintf("msg-c1-%d", i)
		db.Exec(`INSERT INTO message (guid, text, handle_id, service, date, is_from_me)
			VALUES (?, ?, ?, 'iMessage', ?, ?)`, guid, m.text, m.handleID, dateNanos, m.fromMe)
		db.Exec(`INSERT INTO chat_message_join (chat_id, message_id, message_date) VALUES (1, ?, ?)`,
			i+1, dateNanos)
	}

	// --- Messages for Chat 2 (5 messages) ---
	chat2Msgs := []struct {
		text   string
		fromMe int
	}{
		{"Did you see the report?", 0},
		{"Yes, looks good overall", 1},
		{"Great, I'll send the final version", 0},
		{"Thanks Jane!", 1},
		{"You're welcome", 0},
	}
	msgID := len(chat1Msgs) + 1
	for i, m := range chat2Msgs {
		dateNanos := baseAppleNanos + int64(i+20)*60_000_000_000 // offset from chat1
		guid := fmt.Sprintf("msg-c2-%d", i)
		handleID := 3
		if m.fromMe == 1 {
			handleID = 0
		}
		db.Exec(`INSERT INTO message (guid, text, handle_id, service, date, is_from_me)
			VALUES (?, ?, ?, 'iMessage', ?, ?)`, guid, m.text, handleID, dateNanos, m.fromMe)
		db.Exec(`INSERT INTO chat_message_join (chat_id, message_id, message_date) VALUES (2, ?, ?)`,
			msgID, dateNanos)
		msgID++
	}

	// --- Messages for Chat 3 / group (8 messages from different people) ---
	chat3Msgs := []struct {
		text     string
		fromMe   int
		handleID int
	}{
		{"Happy birthday everyone!", 1, 0},
		{"Thanks!", 0, 1},
		{"Party at 7?", 0, 2},
		{"I'll bring cake", 1, 0},
		{"Awesome!", 0, 1},
		{"Can someone pick up balloons?", 0, 2},
		{"I'll get them on the way", 1, 0},
		{"See you all tonight!", 0, 1},
	}
	for i, m := range chat3Msgs {
		dateNanos := baseAppleNanos + int64(i+40)*60_000_000_000
		guid := fmt.Sprintf("msg-c3-%d", i)
		db.Exec(`INSERT INTO message (guid, text, handle_id, service, date, is_from_me)
			VALUES (?, ?, ?, 'iMessage', ?, ?)`, guid, m.text, m.handleID, dateNanos, m.fromMe)
		db.Exec(`INSERT INTO chat_message_join (chat_id, message_id, message_date) VALUES (3, ?, ?)`,
			msgID, dateNanos)
		msgID++
	}

	// --- Attachments (add to a couple messages in chat 1) ---
	// Message 3 (ROWID 3) gets a photo attachment
	db.Exec(`INSERT INTO attachment (guid, original_guid, mime_type, transfer_name, total_bytes, filename)
		VALUES ('att1', 'att1-orig', 'image/jpeg', 'IMG_001.jpg', 2048576,
		'~/Library/Messages/Attachments/ab/cd/att1/IMG_001.jpg')`)
	db.Exec(`INSERT INTO message_attachment_join (message_id, attachment_id) VALUES (3, 1)`)
	db.Exec(`UPDATE message SET cache_has_attachments = 1 WHERE ROWID = 3`)

	// Message 7 (ROWID 7) gets a PDF attachment
	db.Exec(`INSERT INTO attachment (guid, original_guid, mime_type, transfer_name, total_bytes, filename)
		VALUES ('att2', 'att2-orig', 'application/pdf', 'menu.pdf', 524288,
		'~/Library/Messages/Attachments/ef/gh/att2/menu.pdf')`)
	db.Exec(`INSERT INTO message_attachment_join (message_id, attachment_id) VALUES (7, 2)`)
	db.Exec(`UPDATE message SET cache_has_attachments = 1 WHERE ROWID = 7`)

	// Message 5 (ROWID 5) gets two attachments (image + video)
	db.Exec(`INSERT INTO attachment (guid, original_guid, mime_type, transfer_name, total_bytes, filename)
		VALUES ('att3', 'att3-orig', 'image/heic', 'photo.heic', 1048576,
		'~/Library/Messages/Attachments/ij/kl/att3/photo.heic')`)
	db.Exec(`INSERT INTO attachment (guid, original_guid, mime_type, transfer_name, total_bytes, filename)
		VALUES ('att4', 'att4-orig', 'video/quicktime', 'clip.mov', 10485760,
		'~/Library/Messages/Attachments/mn/op/att4/clip.mov')`)
	db.Exec(`INSERT INTO message_attachment_join (message_id, attachment_id) VALUES (5, 3)`)
	db.Exec(`INSERT INTO message_attachment_join (message_id, attachment_id) VALUES (5, 4)`)
	db.Exec(`UPDATE message SET cache_has_attachments = 1 WHERE ROWID = 5`)
}
