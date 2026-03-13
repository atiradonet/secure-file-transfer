package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("mismatched confirm returns error", func(t *testing.T) {
		sc := &mockStorageClient{}
		err := runDelete(ctx, sc, "test-project", "test-ws", "report.pdf.zip", "wrong.pdf.zip")
		if err == nil {
			t.Fatal("expected error for mismatched confirm, got nil")
		}
	})

	t.Run("correct bucket used", func(t *testing.T) {
		sc := &mockStorageClient{}
		captureOutput(func() {
			runDelete(ctx, sc, "test-project", "test-ws", "report.pdf.zip", "report.pdf.zip")
		})
		want := "secure-transfer-test-ws"
		if sc.deletedBucket != want {
			t.Errorf("got bucket %q, want %q", sc.deletedBucket, want)
		}
	})

	t.Run("success message contains object name", func(t *testing.T) {
		sc := &mockStorageClient{}
		out := captureOutput(func() {
			runDelete(ctx, sc, "test-project", "test-ws", "report.pdf.zip", "report.pdf.zip")
		})
		if !strings.Contains(out, "report.pdf.zip") {
			t.Errorf("expected 'report.pdf.zip' in output, got: %s", out)
		}
	})

	t.Run("correct object deleted", func(t *testing.T) {
		sc := &mockStorageClient{}
		captureOutput(func() {
			runDelete(ctx, sc, "test-project", "test-ws", "report.pdf.zip", "report.pdf.zip")
		})
		if sc.deletedObject != "report.pdf.zip" {
			t.Errorf("got deleted object %q, want 'report.pdf.zip'", sc.deletedObject)
		}
	})
}
