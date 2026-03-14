package main

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/pbkdf2"
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
// createSecureBundle — helpers
// ---------------------------------------------------------------------------

// extractHTMLVar parses a JS const assignment from the HTML bundle.
// It matches: const NAME="VALUE"; or const NAME=VALUE; (unquoted for JSON).
var extractRE = regexp.MustCompile(`const ([A-Z_]+)=("(?:[^"\\]|\\.)*"|[^;]+);`)

func extractVars(t *testing.T, html string) map[string]string {
	t.Helper()
	m := make(map[string]string)
	for _, match := range extractRE.FindAllStringSubmatch(html, -1) {
		val := match[2]
		// Strip surrounding quotes for string literals.
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		m[match[1]] = val
	}
	return m
}

// roundtripDecrypt decrypts AES-256-GCM ciphertext using the same parameters
// as the HTML bundle (PBKDF2-SHA256, 100 000 iterations). Returns plaintext.
func roundtripDecrypt(t *testing.T, saltB64, nonceB64, dataB64, password string) []byte {
	t.Helper()
	salt, err := base64.StdEncoding.DecodeString(saltB64)
	if err != nil {
		t.Fatalf("decoding salt: %v", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		t.Fatalf("decoding nonce: %v", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		t.Fatalf("decoding ciphertext: %v", err)
	}

	key := pbkdf2.Key([]byte(password), salt, kdfIterations, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("creating cipher: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("creating GCM: %v", err)
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("decrypting: %v", err)
	}
	return plaintext
}

// ---------------------------------------------------------------------------
// createSecureBundle — tests
// ---------------------------------------------------------------------------

func TestCreateSecureBundle(t *testing.T) {
	t.Run("single file: decryptedName equals filename", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "report.pdf")
		if err := os.WriteFile(src, []byte("pdf content"), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		name, err := createSecureBundle(src, &buf, "testpassword")
		if err != nil {
			t.Fatalf("createSecureBundle: %v", err)
		}
		if name != "report.pdf" {
			t.Errorf("decryptedName = %q, want %q", name, "report.pdf")
		}
	})

	t.Run("single file: HTML output contains all placeholders substituted", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "data.csv")
		if err := os.WriteFile(src, []byte("a,b,c"), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := createSecureBundle(src, &buf, "pw"); err != nil {
			t.Fatalf("createSecureBundle: %v", err)
		}
		html := buf.String()
		for _, placeholder := range []string{"{{TITLE}}", "{{FILENAME_JS}}", "{{SALT}}", "{{NONCE}}", "{{DATA}}"} {
			if strings.Contains(html, placeholder) {
				t.Errorf("HTML still contains unsubstituted placeholder %q", placeholder)
			}
		}
	})

	t.Run("single file: roundtrip decrypt recovers original content", func(t *testing.T) {
		content := []byte("the quick brown fox jumps over the lazy dog")
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "message.txt")
		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := createSecureBundle(src, &buf, "mypassword"); err != nil {
			t.Fatalf("createSecureBundle: %v", err)
		}
		vars := extractVars(t, buf.String())
		for _, k := range []string{"SALT", "NONCE", "DATA"} {
			if vars[k] == "" {
				t.Fatalf("JS variable %q not found in HTML", k)
			}
		}
		plaintext := roundtripDecrypt(t, vars["SALT"], vars["NONCE"], vars["DATA"], "mypassword")
		if !bytes.Equal(plaintext, content) {
			t.Errorf("roundtrip failed: got %q, want %q", plaintext, content)
		}
	})

	t.Run("single file: wrong password fails to decrypt", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "secret.bin")
		if err := os.WriteFile(src, []byte("secret"), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if _, err := createSecureBundle(src, &buf, "correctpassword"); err != nil {
			t.Fatalf("createSecureBundle: %v", err)
		}
		vars := extractVars(t, buf.String())

		salt, _ := base64.StdEncoding.DecodeString(vars["SALT"])
		nonce, _ := base64.StdEncoding.DecodeString(vars["NONCE"])
		ciphertext, _ := base64.StdEncoding.DecodeString(vars["DATA"])
		key := pbkdf2.Key([]byte("wrongpassword"), salt, kdfIterations, 32, sha256.New)
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		_, err := gcm.Open(nil, nonce, ciphertext, nil)
		if err == nil {
			t.Error("expected decryption to fail with wrong password")
		}
	})

	t.Run("directory: decryptedName ends with .zip", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "docs")
		if err := os.MkdirAll(src, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "readme.txt"), []byte("hi"), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		name, err := createSecureBundle(src, &buf, "pw")
		if err != nil {
			t.Fatalf("createSecureBundle: %v", err)
		}
		if name != "docs.zip" {
			t.Errorf("decryptedName = %q, want %q", name, "docs.zip")
		}
	})

	t.Run("directory: roundtrip recovers zip containing expected files", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "project")
		sub := filepath.Join(src, "subdir")
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "nested.txt"), []byte("nested"), 0644); err != nil {
			t.Fatal(err)
		}

		var buf bytes.Buffer
		if _, err := createSecureBundle(src, &buf, "zippass"); err != nil {
			t.Fatalf("createSecureBundle: %v", err)
		}
		vars := extractVars(t, buf.String())
		zipBytes := roundtripDecrypt(t, vars["SALT"], vars["NONCE"], vars["DATA"], "zippass")

		zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
		if err != nil {
			t.Fatalf("opening decrypted zip: %v", err)
		}
		names := make(map[string]bool)
		for _, f := range zr.File {
			names[f.Name] = true
		}
		for _, want := range []string{"project/top.txt", "project/subdir/nested.txt"} {
			if !names[want] {
				t.Errorf("expected %q in zip, got: %v", want, names)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// packToZip
// ---------------------------------------------------------------------------

func TestPackToZip(t *testing.T) {
	t.Run("sorted walk preserves deterministic order", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "mydir")
		if err := os.MkdirAll(src, 0755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"c.txt", "a.txt", "b.txt"} {
			if err := os.WriteFile(filepath.Join(src, name), []byte(name), 0644); err != nil {
				t.Fatal(err)
			}
		}

		var buf1, buf2 bytes.Buffer
		if err := packToZip(src, &buf1); err != nil {
			t.Fatalf("packToZip run 1: %v", err)
		}
		if err := packToZip(src, &buf2); err != nil {
			t.Fatalf("packToZip run 2: %v", err)
		}
		if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
			t.Error("two packToZip runs produced different bytes (not deterministic)")
		}
	})

	t.Run("output is a valid zip", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "stuff")
		if err := os.MkdirAll(src, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := packToZip(src, &buf); err != nil {
			t.Fatalf("packToZip: %v", err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatalf("invalid zip output: %v", err)
		}
		if len(zr.File) != 1 {
			t.Errorf("expected 1 file in zip, got %d", len(zr.File))
		}
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
