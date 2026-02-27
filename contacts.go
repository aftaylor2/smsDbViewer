package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Contact holds a resolved contact's display name and identifiers.
type Contact struct {
	Name   string   // "First Last"
	Phones []string // raw phone numbers from AddressBook
	Emails []string // email addresses
}

// ContactBook maps handle identifiers (phone/email) to contact info.
type ContactBook struct {
	byDigits map[string]*Contact // normalized digits → contact
	byEmail  map[string]*Contact // lowercase email → contact
}

// NewContactBook loads contacts from all AddressBook databases found on the system.
// Returns an empty book (not an error) if contacts can't be read — the app
// should still work, just without names.
func NewContactBook() *ContactBook {
	cb := &ContactBook{
		byDigits: make(map[string]*Contact),
		byEmail:  make(map[string]*Contact),
	}

	abDir := filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "AddressBook")

	// Find all .abcddb files (main + per-source)
	var dbPaths []string
	filepath.Walk(abDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(path, ".abcddb") {
			dbPaths = append(dbPaths, path)
		}
		return nil
	})

	for _, p := range dbPaths {
		cb.loadFromDB(p)
	}

	return cb
}

func (cb *ContactBook) loadFromDB(path string) {
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=ro", path))
	if err != nil {
		return
	}
	defer db.Close()

	// Load contacts with phone numbers
	phoneRows, err := db.Query(`
		SELECT r.Z_PK, COALESCE(r.ZFIRSTNAME,''), COALESCE(r.ZLASTNAME,''),
		       COALESCE(r.ZORGANIZATION,''), p.ZFULLNUMBER
		FROM ZABCDRECORD r
		JOIN ZABCDPHONENUMBER p ON p.ZOWNER = r.Z_PK
	`)
	if err == nil {
		defer phoneRows.Close()
		for phoneRows.Next() {
			var pk int
			var first, last, org, phone string
			if err := phoneRows.Scan(&pk, &first, &last, &org, &phone); err != nil {
				continue
			}
			name := buildName(first, last, org)
			if name == "" {
				continue
			}
			digits := normalizePhone(phone)
			if digits == "" {
				continue
			}
			c := cb.getOrCreate(digits, "phone")
			c.Name = name
			c.Phones = appendUnique(c.Phones, phone)
		}
	}

	// Load contacts with email addresses
	emailRows, err := db.Query(`
		SELECT r.Z_PK, COALESCE(r.ZFIRSTNAME,''), COALESCE(r.ZLASTNAME,''),
		       COALESCE(r.ZORGANIZATION,''), e.ZADDRESS
		FROM ZABCDRECORD r
		JOIN ZABCDEMAILADDRESS e ON e.ZOWNER = r.Z_PK
	`)
	if err == nil {
		defer emailRows.Close()
		for emailRows.Next() {
			var pk int
			var first, last, org, email string
			if err := emailRows.Scan(&pk, &first, &last, &org, &email); err != nil {
				continue
			}
			name := buildName(first, last, org)
			if name == "" {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(email))
			if key == "" {
				continue
			}
			c := cb.getOrCreate(key, "email")
			c.Name = name
			c.Emails = appendUnique(c.Emails, email)
		}
	}
}

func (cb *ContactBook) getOrCreate(key string, kind string) *Contact {
	if kind == "phone" {
		if c, ok := cb.byDigits[key]; ok {
			return c
		}
		c := &Contact{}
		cb.byDigits[key] = c
		return c
	}
	if c, ok := cb.byEmail[key]; ok {
		return c
	}
	c := &Contact{}
	cb.byEmail[key] = c
	return c
}

// Resolve looks up a handle identifier (phone number or email) and returns
// the Contact if found, or nil.
func (cb *ContactBook) Resolve(handle string) *Contact {
	if handle == "" {
		return nil
	}
	// Try as email first (contains @)
	if strings.Contains(handle, "@") {
		if c, ok := cb.byEmail[strings.ToLower(strings.TrimSpace(handle))]; ok {
			return c
		}
		return nil
	}
	// Try as phone — normalize to digits and match
	digits := normalizePhone(handle)
	if digits == "" {
		return nil
	}
	// Try full digits match
	if c, ok := cb.byDigits[digits]; ok {
		return c
	}
	// Try last 10 digits (strip country code)
	if len(digits) > 10 {
		short := digits[len(digits)-10:]
		if c, ok := cb.byDigits[short]; ok {
			return c
		}
	}
	return nil
}

// ResolveName returns the contact name for a handle, or the handle itself if unknown.
func (cb *ContactBook) ResolveName(handle string) string {
	if c := cb.Resolve(handle); c != nil {
		return c.Name
	}
	return handle
}

// normalizePhone strips everything except digits from a phone number.
// Returns the last 10 digits if longer (strips country code for matching).
func normalizePhone(phone string) string {
	var digits strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	// Normalize to 10-digit for US numbers
	if len(d) == 11 && d[0] == '1' {
		d = d[1:]
	}
	return d
}

func buildName(first, last, org string) string {
	name := strings.TrimSpace(first + " " + last)
	if name == "" {
		name = org
	}
	return name
}

func appendUnique(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}
