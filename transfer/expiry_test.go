package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseExpiry(t *testing.T) {
	t.Run("1h", func(t *testing.T) {
		d, err := parseExpiry("1h")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != time.Hour {
			t.Errorf("got %v, want 1h", d)
		}
	})

	t.Run("30m", func(t *testing.T) {
		d, err := parseExpiry("30m")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != 30*time.Minute {
			t.Errorf("got %v, want 30m", d)
		}
	})

	t.Run("1d", func(t *testing.T) {
		d, err := parseExpiry("1d")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if d != 24*time.Hour {
			t.Errorf("got %v, want 24h", d)
		}
	})

	t.Run("exceeds max 2d returns error containing 24 hours", func(t *testing.T) {
		_, err := parseExpiry("2d")
		if err == nil {
			t.Fatal("expected error for 2d, got nil")
		}
		if !strings.Contains(err.Error(), "24 hours") {
			t.Errorf("error %q does not contain '24 hours'", err.Error())
		}
	})

	t.Run("zero 0h returns error containing positive", func(t *testing.T) {
		_, err := parseExpiry("0h")
		if err == nil {
			t.Fatal("expected error for 0h, got nil")
		}
		if !strings.Contains(err.Error(), "positive") {
			t.Errorf("error %q does not contain 'positive'", err.Error())
		}
	})

	t.Run("negative -1h returns error", func(t *testing.T) {
		_, err := parseExpiry("-1h")
		if err == nil {
			t.Fatal("expected error for -1h, got nil")
		}
	})

	t.Run("missing unit 24 returns error", func(t *testing.T) {
		_, err := parseExpiry("24")
		if err == nil {
			t.Fatal("expected error for '24', got nil")
		}
	})

	t.Run("unknown unit 2w returns error", func(t *testing.T) {
		_, err := parseExpiry("2w")
		if err == nil {
			t.Fatal("expected error for '2w', got nil")
		}
	})

	t.Run("non-numeric abch returns error", func(t *testing.T) {
		_, err := parseExpiry("abch")
		if err == nil {
			t.Fatal("expected error for 'abch', got nil")
		}
	})
}
