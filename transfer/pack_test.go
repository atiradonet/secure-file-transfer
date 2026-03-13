package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPack(t *testing.T) {
	ctx := context.Background()

	t.Run("missing folder returns error", func(t *testing.T) {
		sc := &mockStorageClient{}
		err := runPack(ctx, sc, "test-project", "test-ws", "/nonexistent/folder", "1h")
		if err == nil {
			t.Fatal("expected error for missing folder, got nil")
		}
	})

	t.Run("zip name matches folder name", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "my-folder")
		os.MkdirAll(src, 0755)
		os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0644)

		sc := &mockStorageClient{}
		captureOutput(func() {
			runPack(ctx, sc, "test-project", "test-ws", src, "1h")
		})

		want := "my-folder.zip"
		if sc.uploadedObject != want {
			t.Errorf("got object name %q, want %q", sc.uploadedObject, want)
		}
	})

	t.Run("password in output", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "docs")
		os.MkdirAll(src, 0755)
		os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644)

		sc := &mockStorageClient{}
		out := captureOutput(func() {
			runPack(ctx, sc, "test-project", "test-ws", src, "1h")
		})

		if !strings.Contains(out, "PASSWORD") {
			t.Errorf("expected 'PASSWORD' in output, got: %s", out)
		}
	})

	t.Run("SHA-256 in output", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "docs")
		os.MkdirAll(src, 0755)
		os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644)

		sc := &mockStorageClient{}
		out := captureOutput(func() {
			runPack(ctx, sc, "test-project", "test-ws", src, "1h")
		})

		if !strings.Contains(out, "SHA-256") {
			t.Errorf("expected 'SHA-256' in output, got: %s", out)
		}
	})

	t.Run("correct bucket used", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "docs")
		os.MkdirAll(src, 0755)
		os.WriteFile(filepath.Join(src, "file.txt"), []byte("hello"), 0644)

		sc := &mockStorageClient{}
		captureOutput(func() {
			runPack(ctx, sc, "test-project", "test-ws", src, "1h")
		})

		want := "secure-transfer-test-ws"
		if sc.uploadedBucket != want {
			t.Errorf("got bucket %q, want %q", sc.uploadedBucket, want)
		}
	})
}
