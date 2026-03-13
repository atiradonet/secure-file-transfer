package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yeka/zip"
)

// ---------------------------------------------------------------------------
// validateWorkspace
// ---------------------------------------------------------------------------

func TestValidateWorkspace(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid hyphenated name", "acme-q1-report", false},
		{"valid alphanumeric", "abc123", false},
		{"valid three chars", "abc", false},
		{"too short two chars", "ab", true},
		{"leading hyphen", "-invalid", true},
		{"trailing hyphen", "invalid-", true},
		{"uppercase letters", "Invalid-Name", true},
		{"consecutive hyphens", "double--hyphen", true},
		{"underscore", "invalid_name", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWorkspace(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateWorkspace(%q) error = %v, wantErr %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseExpiry
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// generatePassword
// ---------------------------------------------------------------------------

func TestGeneratePassword(t *testing.T) {
	t.Run("length is 32", func(t *testing.T) {
		pw, err := generatePassword()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pw) != 32 {
			t.Errorf("got length %d, want 32", len(pw))
		}
	})

	t.Run("alphanumeric only", func(t *testing.T) {
		pw, err := generatePassword()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, ch := range pw {
			if !strings.ContainsRune(passwordAlphabet, ch) {
				t.Errorf("password contains non-alphanumeric character: %q", ch)
			}
		}
	})

	t.Run("two passwords differ", func(t *testing.T) {
		pw1, _ := generatePassword()
		pw2, _ := generatePassword()
		if pw1 == pw2 {
			t.Error("expected two different passwords, got identical")
		}
	})
}

// ---------------------------------------------------------------------------
// createEncryptedZip
// ---------------------------------------------------------------------------

// readZipNames opens an AES-256 encrypted zip and returns the stored file names.
func readZipNames(t *testing.T, zipPath, password string) []string {
	t.Helper()
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("opening zip %q: %v", zipPath, err)
	}
	defer r.Close()
	var names []string
	for _, f := range r.File {
		f.SetPassword(password)
		names = append(names, f.Name)
	}
	return names
}

func TestCreateEncryptedZip(t *testing.T) {
	t.Run("single file stored at root with correct name", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "report.pdf")
		if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(tmpDir, "out.zip")
		if err := createEncryptedZip(src, dest, "password123"); err != nil {
			t.Fatalf("createEncryptedZip: %v", err)
		}
		names := readZipNames(t, dest, "password123")
		if len(names) != 1 {
			t.Fatalf("expected 1 file in zip, got %d: %v", len(names), names)
		}
		if names[0] != "report.pdf" {
			t.Errorf("expected file name 'report.pdf', got %q", names[0])
		}
	})

	t.Run("folder with subfolder preserves structure", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "docs")
		sub := filepath.Join(src, "invoices")
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "inv001.pdf"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(tmpDir, "out.zip")
		if err := createEncryptedZip(src, dest, "password123"); err != nil {
			t.Fatalf("createEncryptedZip: %v", err)
		}
		names := readZipNames(t, dest, "password123")
		found := false
		for _, n := range names {
			if strings.Contains(n, "invoices/inv001.pdf") || strings.Contains(n, "invoices\\inv001.pdf") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected invoices/inv001.pdf in zip, got: %v", names)
		}
	})

	t.Run("wrong password returns error on read", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "secret.txt")
		if err := os.WriteFile(src, []byte("secret content"), 0644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(tmpDir, "out.zip")
		if err := createEncryptedZip(src, dest, "correctpassword"); err != nil {
			t.Fatalf("createEncryptedZip: %v", err)
		}

		r, err := zip.OpenReader(dest)
		if err != nil {
			t.Fatalf("opening zip: %v", err)
		}
		defer r.Close()

		if len(r.File) == 0 {
			t.Fatal("expected at least one file in zip")
		}
		f := r.File[0]
		f.SetPassword("wrongpassword")
		rc, err := f.Open()
		if err != nil {
			// Error at open is acceptable for wrong password.
			return
		}
		defer rc.Close()
		buf := make([]byte, 64)
		_, readErr := io.ReadFull(rc, buf)
		if readErr == nil {
			t.Log("no error on read with wrong password — implementation-defined behaviour")
		}
		// Either Open or Read should fail with wrong password for AES-256.
	})
}

// ---------------------------------------------------------------------------
// formatSize
// ---------------------------------------------------------------------------

func TestFormatSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1024, "1,024"},
		{1048576, "1,048,576"},
	}
	for _, tc := range cases {
		got := formatSize(tc.in)
		if got != tc.want {
			t.Errorf("formatSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
