package main

import (
	"os"
	"strings"
	"testing"
)

func TestExportCSV(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	store := NewStore(db)
	contacts := &ContactBook{
		byDigits: make(map[string]*Contact),
		byEmail:  make(map[string]*Contact),
	}

	path, err := exportCSV(store, contacts, 1, []string{"+15551234567"}, "Test Chat")
	if err != nil {
		t.Fatalf("exportCSV: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}

	content := string(data)
	lines := strings.Split(strings.TrimSpace(content), "\n")

	t.Run("header", func(t *testing.T) {
		expected := "Timestamp,From,To,Body,Service,AttachmentType,AttachmentFile,AttachmentSize"
		if lines[0] != expected {
			t.Errorf("header:\ngot:  %q\nwant: %q", lines[0], expected)
		}
	})

	t.Run("row_count", func(t *testing.T) {
		// 10 messages + 1 header = 11 lines
		if len(lines) != 11 {
			t.Errorf("expected 11 lines, got %d", len(lines))
		}
	})

	t.Run("from_me", func(t *testing.T) {
		// First data line should be from "Me"
		if !strings.Contains(lines[1], ",Me,") {
			t.Errorf("first message should be from Me: %q", lines[1])
		}
	})

	t.Run("attachment_columns", func(t *testing.T) {
		// Message 3 (line index 3) has a JPEG attachment
		if !strings.Contains(lines[3], "photo") {
			t.Errorf("message 3 should have photo attachment: %q", lines[3])
		}
		if !strings.Contains(lines[3], "IMG_001.jpg") {
			t.Errorf("message 3 should have filename: %q", lines[3])
		}
	})

	t.Run("filename_format", func(t *testing.T) {
		if !strings.HasPrefix(path, "Test_Chat_") {
			t.Errorf("filename should start with 'Test_Chat_', got %q", path)
		}
		if !strings.HasSuffix(path, ".csv") {
			t.Errorf("filename should end with .csv, got %q", path)
		}
	})
}

func TestCsvEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello, world", `"hello, world"`},
		{`say "hi"`, `"say ""hi"""`},
		{"line1\nline2", "\"line1\nline2\""},
		{"", ""},
	}
	for _, tt := range tests {
		got := csvEscape(tt.input)
		if got != tt.want {
			t.Errorf("csvEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBuildExportFilename(t *testing.T) {
	contacts := &ContactBook{
		byDigits: make(map[string]*Contact),
		byEmail:  make(map[string]*Contact),
	}

	t.Run("with_title", func(t *testing.T) {
		name := buildExportFilename("John Smith", nil, contacts)
		if !strings.HasPrefix(name, "John_Smith_") {
			t.Errorf("expected prefix 'John_Smith_', got %q", name)
		}
	})

	t.Run("special_chars", func(t *testing.T) {
		name := buildExportFilename("John & Jane's Chat!", nil, contacts)
		if strings.ContainsAny(name, "&'! ") {
			t.Errorf("filename should not contain special chars: %q", name)
		}
	})

	t.Run("empty_fallback", func(t *testing.T) {
		name := buildExportFilename("", nil, contacts)
		if !strings.HasPrefix(name, "conversation_") {
			t.Errorf("expected prefix 'conversation_', got %q", name)
		}
	})
}
