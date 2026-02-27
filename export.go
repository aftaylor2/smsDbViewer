package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

var nonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// exportCSV writes all messages for a chat to a CSV file.
// Returns the path of the written file.
func exportCSV(store *Store, contacts *ContactBook, chatID int, participants []string, chatTitle string) (string, error) {
	messages, err := store.FetchAllMessages(chatID)
	if err != nil {
		return "", err
	}

	filename := buildExportFilename(chatTitle, participants, contacts)
	f, err := os.Create(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Header
	f.WriteString("Timestamp,From,To,Body,Service,AttachmentType,AttachmentFile,AttachmentSize\n")

	// Resolve participant names for the "To" field
	var resolvedParticipants []string
	for _, p := range participants {
		resolvedParticipants = append(resolvedParticipants, contacts.ResolveName(p))
	}
	participantsStr := strings.Join(resolvedParticipants, "; ")

	for _, msg := range messages {
		ts := msg.Date.Format("2006-01-02 15:04:05")

		var from, to string
		if msg.IsFromMe {
			from = "Me"
			to = participantsStr
		} else {
			from = contacts.ResolveName(msg.Sender)
			to = "Me"
		}

		body := csvEscape(msg.Text)

		attachType := ""
		attachFile := ""
		attachSize := ""
		if len(msg.Attachments) > 0 {
			var types, files, sizes []string
			for _, a := range msg.Attachments {
				types = append(types, a.TypeLabel)
				if a.Filename != "" {
					files = append(files, a.Filename)
				}
				if a.Size > 0 {
					sizes = append(sizes, formatBytes(a.Size))
				}
			}
			attachType = csvEscape(strings.Join(types, "; "))
			attachFile = csvEscape(strings.Join(files, "; "))
			attachSize = csvEscape(strings.Join(sizes, "; "))
		}

		line := fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,%s\n",
			ts,
			csvEscape(from),
			csvEscape(to),
			body,
			msg.Service,
			attachType,
			attachFile,
			attachSize,
		)
		f.WriteString(line)
	}

	return filename, nil
}

func buildExportFilename(chatTitle string, participants []string, contacts *ContactBook) string {
	// Build a name from the chat title or participant names
	name := chatTitle
	if name == "" {
		var names []string
		for _, p := range participants {
			names = append(names, contacts.ResolveName(p))
		}
		name = strings.Join(names, "_")
	}

	// Sanitize for filename
	name = nonAlphaNum.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if len(name) > 50 {
		name = name[:50]
	}
	if name == "" {
		name = "conversation"
	}

	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("%s_%s.csv", name, timestamp)
}

// csvEscape wraps a field in quotes if it contains commas, quotes, or newlines.
// Doubles any internal quotes per RFC 4180.
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
	}
	return s
}
