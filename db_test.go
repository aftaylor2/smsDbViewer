package main

import (
	"strings"
	"testing"
)

func TestFetchConversations(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	store := NewStore(db)

	convs, err := store.FetchConversations()
	if err != nil {
		t.Fatalf("FetchConversations: %v", err)
	}

	if len(convs) != 3 {
		t.Fatalf("expected 3 conversations, got %d", len(convs))
	}

	// Conversations are ordered by last message date DESC.
	// Chat 3 has the latest messages (offset +40..+47 min),
	// Chat 2 next (offset +20..+24 min), Chat 1 first (offset +0..+9 min).

	t.Run("order", func(t *testing.T) {
		if convs[0].ChatID != 3 {
			t.Errorf("expected first conv to be chat 3 (latest), got chat %d", convs[0].ChatID)
		}
		if convs[1].ChatID != 2 {
			t.Errorf("expected second conv to be chat 2, got chat %d", convs[1].ChatID)
		}
		if convs[2].ChatID != 1 {
			t.Errorf("expected third conv to be chat 1, got chat %d", convs[2].ChatID)
		}
	})

	t.Run("message_counts", func(t *testing.T) {
		// Find each chat by ID
		counts := map[int]int{}
		sent := map[int]int{}
		recv := map[int]int{}
		for _, c := range convs {
			counts[c.ChatID] = c.MessageCount
			sent[c.ChatID] = c.SentCount
			recv[c.ChatID] = c.ReceivedCount
		}
		if counts[1] != 10 {
			t.Errorf("chat 1: expected 10 messages, got %d", counts[1])
		}
		if counts[2] != 5 {
			t.Errorf("chat 2: expected 5 messages, got %d", counts[2])
		}
		if counts[3] != 8 {
			t.Errorf("chat 3: expected 8 messages, got %d", counts[3])
		}
		if sent[1] != 5 {
			t.Errorf("chat 1: expected 5 sent, got %d", sent[1])
		}
		if recv[1] != 5 {
			t.Errorf("chat 1: expected 5 received, got %d", recv[1])
		}
	})

	t.Run("participants", func(t *testing.T) {
		for _, c := range convs {
			switch c.ChatID {
			case 1:
				if len(c.Participants) != 1 || c.Participants[0] != "+15551234567" {
					t.Errorf("chat 1 participants: %v", c.Participants)
				}
			case 2:
				if len(c.Participants) != 1 || c.Participants[0] != "jane@example.com" {
					t.Errorf("chat 2 participants: %v", c.Participants)
				}
			case 3:
				if len(c.Participants) != 2 {
					t.Errorf("chat 3: expected 2 participants, got %d", len(c.Participants))
				}
			}
		}
	})

	t.Run("group_chat", func(t *testing.T) {
		for _, c := range convs {
			if c.ChatID == 3 {
				if c.DisplayName != "Family Group" {
					t.Errorf("chat 3 display name: got %q, want %q", c.DisplayName, "Family Group")
				}
				if c.Style != 16 {
					t.Errorf("chat 3 style: got %d, want 16", c.Style)
				}
			}
		}
	})

	t.Run("dates", func(t *testing.T) {
		for _, c := range convs {
			if c.FirstMsgDate.IsZero() {
				t.Errorf("chat %d: FirstMsgDate is zero", c.ChatID)
			}
			if c.LastMsgDate.IsZero() {
				t.Errorf("chat %d: LastMsgDate is zero", c.ChatID)
			}
			if !c.FirstMsgDate.Before(c.LastMsgDate) {
				t.Errorf("chat %d: FirstMsgDate (%v) not before LastMsgDate (%v)",
					c.ChatID, c.FirstMsgDate, c.LastMsgDate)
			}
		}
	})
}

func TestFetchMessages(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	store := NewStore(db)

	t.Run("basic", func(t *testing.T) {
		msgs, err := store.FetchMessages(1, 0, 200)
		if err != nil {
			t.Fatalf("FetchMessages: %v", err)
		}
		if len(msgs) != 10 {
			t.Fatalf("expected 10 messages, got %d", len(msgs))
		}

		// Should be in chronological order (oldest first)
		if msgs[0].Text != "Hey, how are you?" {
			t.Errorf("first message text: got %q", msgs[0].Text)
		}
		if msgs[9].Text != "No worries, I just got here" {
			t.Errorf("last message text: got %q", msgs[9].Text)
		}

		// Check is_from_me alternation
		if !msgs[0].IsFromMe {
			t.Error("first message should be from me")
		}
		if msgs[1].IsFromMe {
			t.Error("second message should not be from me")
		}
	})

	t.Run("chronological_order", func(t *testing.T) {
		msgs, _ := store.FetchMessages(1, 0, 200)
		for i := 1; i < len(msgs); i++ {
			if msgs[i].Date.Before(msgs[i-1].Date) {
				t.Errorf("message %d (%v) is before message %d (%v)",
					i, msgs[i].Date, i-1, msgs[i-1].Date)
			}
		}
	})

	t.Run("sender_handle", func(t *testing.T) {
		msgs, _ := store.FetchMessages(1, 0, 200)
		for _, m := range msgs {
			if !m.IsFromMe && m.Sender != "+15551234567" {
				t.Errorf("expected sender +15551234567, got %q", m.Sender)
			}
		}
	})

	t.Run("pagination", func(t *testing.T) {
		// Fetch first 5 messages (most recent due to DESC, then reversed)
		page1, err := store.FetchMessages(1, 0, 5)
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		if len(page1) != 5 {
			t.Fatalf("page 1: expected 5 messages, got %d", len(page1))
		}

		// These are the 5 most recent messages (ROWIDs 6-10), reversed to chronological
		// page1[0] should be ROWID 6, page1[4] should be ROWID 10

		// Fetch next page using cursor from oldest in page1
		cursor := page1[0].ROWID
		page2, err := store.FetchMessages(1, cursor, 5)
		if err != nil {
			t.Fatalf("page 2: %v", err)
		}
		if len(page2) != 5 {
			t.Fatalf("page 2: expected 5 messages, got %d", len(page2))
		}

		// page2 should be the 5 oldest messages (ROWIDs 1-5)
		// No overlap
		for _, m1 := range page1 {
			for _, m2 := range page2 {
				if m1.ROWID == m2.ROWID {
					t.Errorf("duplicate ROWID %d across pages", m1.ROWID)
				}
			}
		}

		// Third page should be empty
		cursor2 := page2[0].ROWID
		page3, err := store.FetchMessages(1, cursor2, 5)
		if err != nil {
			t.Fatalf("page 3: %v", err)
		}
		if len(page3) != 0 {
			t.Errorf("page 3: expected 0 messages, got %d", len(page3))
		}
	})

	t.Run("attachments", func(t *testing.T) {
		msgs, _ := store.FetchMessages(1, 0, 200)

		// Message 3 (index 2) should have 1 JPEG attachment
		if len(msgs[2].Attachments) != 1 {
			t.Fatalf("msg 3: expected 1 attachment, got %d", len(msgs[2].Attachments))
		}
		if msgs[2].Attachments[0].TypeLabel != "photo" {
			t.Errorf("msg 3 attachment type: got %q, want %q", msgs[2].Attachments[0].TypeLabel, "photo")
		}
		if msgs[2].Attachments[0].Filename != "IMG_001.jpg" {
			t.Errorf("msg 3 attachment filename: got %q", msgs[2].Attachments[0].Filename)
		}
		if msgs[2].Attachments[0].Size != 2048576 {
			t.Errorf("msg 3 attachment size: got %d", msgs[2].Attachments[0].Size)
		}

		// Message 5 (index 4) should have 2 attachments (photo + video)
		if len(msgs[4].Attachments) != 2 {
			t.Fatalf("msg 5: expected 2 attachments, got %d", len(msgs[4].Attachments))
		}

		// Message 7 (index 6) should have a PDF
		if len(msgs[6].Attachments) != 1 {
			t.Fatalf("msg 7: expected 1 attachment, got %d", len(msgs[6].Attachments))
		}
		if msgs[6].Attachments[0].TypeLabel != "PDF" {
			t.Errorf("msg 7 attachment type: got %q, want %q", msgs[6].Attachments[0].TypeLabel, "PDF")
		}

		// Messages without attachments should have none
		if len(msgs[0].Attachments) != 0 {
			t.Errorf("msg 1: expected 0 attachments, got %d", len(msgs[0].Attachments))
		}
	})
}

func TestFetchAllMessages(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	store := NewStore(db)

	msgs, err := store.FetchAllMessages(1)
	if err != nil {
		t.Fatalf("FetchAllMessages: %v", err)
	}
	if len(msgs) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(msgs))
	}

	// Should be in chronological order (ASC)
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Date.Before(msgs[i-1].Date) {
			t.Errorf("message %d is before message %d", i, i-1)
		}
	}
}

func TestSearchMessages(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	store := NewStore(db)

	t.Run("found", func(t *testing.T) {
		results, err := store.SearchMessages("lunch", 100)
		if err != nil {
			t.Fatalf("SearchMessages: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result for 'lunch', got %d", len(results))
		}
		if results[0].ChatID != 1 {
			t.Errorf("expected chat 1, got chat %d", results[0].ChatID)
		}
	})

	t.Run("multiple_results", func(t *testing.T) {
		results, _ := store.SearchMessages("good", 100)
		if len(results) < 2 {
			t.Errorf("expected at least 2 results for 'good', got %d", len(results))
		}
	})

	t.Run("no_results", func(t *testing.T) {
		results, _ := store.SearchMessages("xyznonexistent", 100)
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("cross_chat", func(t *testing.T) {
		// "cake" is only in chat 3
		results, _ := store.SearchMessages("cake", 100)
		if len(results) != 1 {
			t.Fatalf("expected 1 result for 'cake', got %d", len(results))
		}
		if results[0].ChatID != 3 {
			t.Errorf("expected chat 3, got chat %d", results[0].ChatID)
		}
		if results[0].ChatName != "Family Group" {
			t.Errorf("expected chat name 'Family Group', got %q", results[0].ChatName)
		}
	})

	t.Run("limit", func(t *testing.T) {
		results, _ := store.SearchMessages("e", 3) // many matches, limit to 3
		if len(results) > 3 {
			t.Errorf("expected at most 3 results, got %d", len(results))
		}
	})
}

func TestAppleNanosToTime(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		result := appleNanosToTime(0)
		if !result.IsZero() {
			t.Errorf("expected zero time, got %v", result)
		}
	})

	t.Run("known_value", func(t *testing.T) {
		// 2024-06-15 10:00:00 UTC = 740142000 seconds from Apple epoch
		nanos := int64(740_142_000_000_000_000)
		result := appleNanosToTime(nanos)
		if result.UTC().Year() != 2024 || result.UTC().Month() != 6 || result.UTC().Day() != 15 {
			t.Errorf("expected 2024-06-15, got %v", result.UTC())
		}
	})
}

func TestAttachmentLabel(t *testing.T) {
	tests := []struct {
		mime string
		want string
	}{
		{"image/jpeg", "photo"},
		{"image/heic", "photo"},
		{"image/png", "image"},
		{"image/gif", "GIF"},
		{"video/quicktime", "video"},
		{"audio/mpeg", "audio"},
		{"application/pdf", "PDF"},
		{"text/vcard", "contact card"},
		{"", "attachment"},
		{"application/octet-stream", "file"},
	}
	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			got := attachmentLabel(tt.mime)
			if got != tt.want {
				t.Errorf("attachmentLabel(%q) = %q, want %q", tt.mime, got, tt.want)
			}
		})
	}
}

func TestParseAttachments(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		result := parseAttachments("")
		if result != nil {
			t.Errorf("expected nil, got %v", result)
		}
	})

	t.Run("null_row", func(t *testing.T) {
		// LEFT JOIN producing empty fields: mime="" || name="" || size=0
		result := parseAttachments("||||0")
		if len(result) != 0 {
			t.Errorf("expected 0 attachments for null row, got %d", len(result))
		}
	})

	t.Run("single", func(t *testing.T) {
		result := parseAttachments("image/jpeg||photo.jpg||2048576")
		if len(result) != 1 {
			t.Fatalf("expected 1 attachment, got %d", len(result))
		}
		if result[0].TypeLabel != "photo" {
			t.Errorf("type: got %q", result[0].TypeLabel)
		}
		if result[0].Filename != "photo.jpg" {
			t.Errorf("filename: got %q", result[0].Filename)
		}
		if result[0].Size != 2048576 {
			t.Errorf("size: got %d", result[0].Size)
		}
	})

	t.Run("multiple", func(t *testing.T) {
		result := parseAttachments("image/heic||a.heic||1000;;video/quicktime||b.mov||5000")
		if len(result) != 2 {
			t.Fatalf("expected 2 attachments, got %d", len(result))
		}
		if result[0].TypeLabel != "photo" {
			t.Errorf("first type: got %q", result[0].TypeLabel)
		}
		if result[1].TypeLabel != "video" {
			t.Errorf("second type: got %q", result[1].TypeLabel)
		}
	})
}

func TestFetchChatAttachments(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	store := NewStore(db)

	t.Run("chat_with_attachments", func(t *testing.T) {
		attachments, err := store.FetchChatAttachments(1)
		if err != nil {
			t.Fatalf("FetchChatAttachments: %v", err)
		}
		// Chat 1 has 4 attachments: msg 3 (1 JPEG), msg 5 (1 HEIC + 1 video), msg 7 (1 PDF)
		if len(attachments) != 4 {
			t.Fatalf("expected 4 attachments, got %d", len(attachments))
		}
	})

	t.Run("ordered_by_date_desc", func(t *testing.T) {
		attachments, _ := store.FetchChatAttachments(1)
		for i := 1; i < len(attachments); i++ {
			if attachments[i].Date.After(attachments[i-1].Date) {
				t.Errorf("attachment %d date (%v) is after attachment %d date (%v)",
					i, attachments[i].Date, i-1, attachments[i-1].Date)
			}
		}
	})

	t.Run("fields_populated", func(t *testing.T) {
		attachments, _ := store.FetchChatAttachments(1)
		// First result (most recent) should be the PDF from msg 7
		pdf := attachments[0]
		if pdf.TypeLabel != "PDF" {
			t.Errorf("expected PDF, got %q", pdf.TypeLabel)
		}
		if pdf.Filename != "menu.pdf" {
			t.Errorf("expected menu.pdf, got %q", pdf.Filename)
		}
		if pdf.Size != 524288 {
			t.Errorf("expected size 524288, got %d", pdf.Size)
		}
		if !strings.Contains(pdf.FilePath, "menu.pdf") {
			t.Errorf("expected file path containing menu.pdf, got %q", pdf.FilePath)
		}
	})

	t.Run("sender_info", func(t *testing.T) {
		attachments, _ := store.FetchChatAttachments(1)
		for _, a := range attachments {
			if !a.IsFromMe && a.Sender == "" {
				t.Errorf("non-from-me attachment should have sender, date=%v", a.Date)
			}
		}
	})

	t.Run("chat_without_attachments", func(t *testing.T) {
		attachments, err := store.FetchChatAttachments(2)
		if err != nil {
			t.Fatalf("FetchChatAttachments: %v", err)
		}
		if len(attachments) != 0 {
			t.Errorf("expected 0 attachments for chat 2, got %d", len(attachments))
		}
	})
}

func TestExpandTilde(t *testing.T) {
	t.Run("with_tilde", func(t *testing.T) {
		result := expandTilde("~/Library/Messages/test.jpg")
		if strings.HasPrefix(result, "~/") {
			t.Errorf("tilde not expanded: %q", result)
		}
		if !strings.HasSuffix(result, "/Library/Messages/test.jpg") {
			t.Errorf("path suffix wrong: %q", result)
		}
	})

	t.Run("without_tilde", func(t *testing.T) {
		result := expandTilde("/usr/local/test.jpg")
		if result != "/usr/local/test.jpg" {
			t.Errorf("path should be unchanged: %q", result)
		}
	})
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
