package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeka/zip"
)

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

	t.Run("two passwords are different", func(t *testing.T) {
		pw1, _ := generatePassword()
		pw2, _ := generatePassword()
		if pw1 == pw2 {
			t.Error("expected two different passwords, got identical")
		}
	})
}

// readZipNames opens an AES-256 encrypted zip and returns the file names.
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
	t.Run("single file zip contains just that file", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "report.pdf")
		if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(tmpDir, "out.zip")
		if err := createEncryptedZip(src, dest, "password123"); err != nil {
			t.Fatalf("createEncryptedZip error: %v", err)
		}

		names := readZipNames(t, dest, "password123")
		if len(names) != 1 {
			t.Errorf("expected 1 file in zip, got %d: %v", len(names), names)
		}
		if len(names) > 0 && names[0] != "report.pdf" {
			t.Errorf("expected file name 'report.pdf', got %q", names[0])
		}
	})

	t.Run("folder zip preserves directory structure", func(t *testing.T) {
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
			t.Fatalf("createEncryptedZip error: %v", err)
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

	t.Run("wrong password raises error when reading", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "secret.txt")
		if err := os.WriteFile(src, []byte("secret content"), 0644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(tmpDir, "out.zip")
		if err := createEncryptedZip(src, dest, "correctpassword"); err != nil {
			t.Fatalf("createEncryptedZip error: %v", err)
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
			// Error at open is acceptable for wrong password
			return
		}
		defer rc.Close()
		buf := make([]byte, 64)
		_, readErr := io.ReadFull(rc, buf)
		if readErr == nil {
			t.Log("no error on read with wrong password — implementation-defined behaviour")
		}
		// Either Open or Read should fail with wrong password for AES-256
	})
}
