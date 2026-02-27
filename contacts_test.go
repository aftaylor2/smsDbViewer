package main

import (
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"+15551234567", "5551234567"},
		{"(555) 123-4567", "5551234567"},
		{"555-123-4567", "5551234567"},
		{"5551234567", "5551234567"},
		{"+1 (555) 123-4567", "5551234567"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizePhone(tt.input)
		if got != tt.want {
			t.Errorf("normalizePhone(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestContactBookResolve(t *testing.T) {
	cb := &ContactBook{
		byDigits: map[string]*Contact{
			"5551234567": {Name: "John Doe", Phones: []string{"+15551234567"}},
		},
		byEmail: map[string]*Contact{
			"jane@example.com": {Name: "Jane Smith", Emails: []string{"jane@example.com"}},
		},
	}

	t.Run("phone_with_country_code", func(t *testing.T) {
		c := cb.Resolve("+15551234567")
		if c == nil || c.Name != "John Doe" {
			t.Errorf("expected John Doe, got %v", c)
		}
	})

	t.Run("phone_formatted", func(t *testing.T) {
		c := cb.Resolve("(555) 123-4567")
		if c == nil || c.Name != "John Doe" {
			t.Errorf("expected John Doe, got %v", c)
		}
	})

	t.Run("email", func(t *testing.T) {
		c := cb.Resolve("jane@example.com")
		if c == nil || c.Name != "Jane Smith" {
			t.Errorf("expected Jane Smith, got %v", c)
		}
	})

	t.Run("email_case_insensitive", func(t *testing.T) {
		c := cb.Resolve("Jane@Example.COM")
		if c == nil || c.Name != "Jane Smith" {
			t.Errorf("expected Jane Smith, got %v", c)
		}
	})

	t.Run("not_found", func(t *testing.T) {
		c := cb.Resolve("+19999999999")
		if c != nil {
			t.Errorf("expected nil, got %v", c)
		}
	})

	t.Run("empty", func(t *testing.T) {
		c := cb.Resolve("")
		if c != nil {
			t.Errorf("expected nil, got %v", c)
		}
	})
}

func TestContactBookResolveName(t *testing.T) {
	cb := &ContactBook{
		byDigits: map[string]*Contact{
			"5551234567": {Name: "John Doe"},
		},
		byEmail: make(map[string]*Contact),
	}

	t.Run("known", func(t *testing.T) {
		name := cb.ResolveName("+15551234567")
		if name != "John Doe" {
			t.Errorf("expected 'John Doe', got %q", name)
		}
	})

	t.Run("unknown_returns_handle", func(t *testing.T) {
		name := cb.ResolveName("+19999999999")
		if name != "+19999999999" {
			t.Errorf("expected handle back, got %q", name)
		}
	})
}

func TestBuildName(t *testing.T) {
	tests := []struct {
		first, last, org string
		want             string
	}{
		{"John", "Doe", "Acme", "John Doe"},
		{"John", "", "", "John"},
		{"", "Doe", "", "Doe"},
		{"", "", "Acme Inc", "Acme Inc"},
		{"", "", "", ""},
	}
	for _, tt := range tests {
		got := buildName(tt.first, tt.last, tt.org)
		if got != tt.want {
			t.Errorf("buildName(%q,%q,%q) = %q, want %q", tt.first, tt.last, tt.org, got, tt.want)
		}
	}
}
