package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
)

// mockStorageClient implements StorageClient for testing.
type mockStorageClient struct {
	uploadedBucket      string
	uploadedObject      string
	uploadedContentType string
	uploadErr           error
	signedURL           string
	signErr             error
	signedURLOpts       *storage.SignedURLOptions
	listedObjects       []ObjectInfo
	listErr             error
	deletedBucket       string
	deletedObject       string
	deleteErr           error
}

func (m *mockStorageClient) Upload(ctx context.Context, bucket, object, contentType, localPath string) error {
	m.uploadedBucket = bucket
	m.uploadedObject = object
	m.uploadedContentType = contentType
	return m.uploadErr
}

func (m *mockStorageClient) SignedURL(bucket, object string, opts *storage.SignedURLOptions) (string, error) {
	m.signedURLOpts = opts
	if m.signErr != nil {
		return "", m.signErr
	}
	if m.signedURL != "" {
		return m.signedURL, nil
	}
	return "https://signed.example.com/" + object, nil
}

func (m *mockStorageClient) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	return m.listedObjects, m.listErr
}

func (m *mockStorageClient) DeleteObject(ctx context.Context, bucket, object string) error {
	m.deletedBucket = bucket
	m.deletedObject = object
	return m.deleteErr
}

// captureOutput redirects stdout and returns what was printed.
func captureOutput(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestRunUpload(t *testing.T) {
	ctx := context.Background()

	t.Run("missing file returns error", func(t *testing.T) {
		sc := &mockStorageClient{}
		err := runUpload(ctx, sc, "test-project", "test-ws", "/nonexistent/file.pdf", "1h", "")
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
	})

	t.Run("correct bucket used", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := filepath.Join(tmpDir, "report.pdf")
		os.WriteFile(f, []byte("data"), 0644)

		sc := &mockStorageClient{}
		var out string
		out = captureOutput(func() {
			runUpload(ctx, sc, "test-project", "test-ws", f, "1h", "")
		})
		_ = out

		want := "secure-transfer-test-ws"
		if sc.uploadedBucket != want {
			t.Errorf("got bucket %q, want %q", sc.uploadedBucket, want)
		}
	})

	t.Run("content type is application/zip", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := filepath.Join(tmpDir, "report.pdf")
		os.WriteFile(f, []byte("data"), 0644)

		sc := &mockStorageClient{}
		captureOutput(func() {
			runUpload(ctx, sc, "test-project", "test-ws", f, "1h", "")
		})

		if sc.uploadedContentType != "application/zip" {
			t.Errorf("got content type %q, want application/zip", sc.uploadedContentType)
		}
	})

	t.Run("zip name is file.zip", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := filepath.Join(tmpDir, "report.pdf")
		os.WriteFile(f, []byte("data"), 0644)

		sc := &mockStorageClient{}
		captureOutput(func() {
			runUpload(ctx, sc, "test-project", "test-ws", f, "1h", "")
		})

		if sc.uploadedObject != "report.pdf.zip" {
			t.Errorf("got object name %q, want 'report.pdf.zip'", sc.uploadedObject)
		}
	})

	t.Run("password in output", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := filepath.Join(tmpDir, "report.pdf")
		os.WriteFile(f, []byte("data"), 0644)

		sc := &mockStorageClient{}
		out := captureOutput(func() {
			runUpload(ctx, sc, "test-project", "test-ws", f, "1h", "")
		})

		if !strings.Contains(out, "PASSWORD") {
			t.Errorf("expected 'PASSWORD' in output, got: %s", out)
		}
	})

	t.Run("SHA-256 in output", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := filepath.Join(tmpDir, "report.pdf")
		os.WriteFile(f, []byte("data"), 0644)

		sc := &mockStorageClient{}
		out := captureOutput(func() {
			runUpload(ctx, sc, "test-project", "test-ws", f, "1h", "")
		})

		if !strings.Contains(out, "SHA-256") {
			t.Errorf("expected 'SHA-256' in output, got: %s", out)
		}
	})

	t.Run("upload with prefix: object name is prefix/file.zip", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := filepath.Join(tmpDir, "file.txt")
		os.WriteFile(f, []byte("x"), 0644)

		sc := &mockStorageClient{}
		captureOutput(func() {
			runUpload(ctx, sc, "test-project", "test-ws", f, "1h", "folder")
		})

		want := "folder/file.txt.zip"
		if sc.uploadedObject != want {
			t.Errorf("got object name %q, want %q", sc.uploadedObject, want)
		}
	})

	t.Run("signed URL has content-disposition attachment", func(t *testing.T) {
		tmpDir := t.TempDir()
		f := filepath.Join(tmpDir, "report.pdf")
		os.WriteFile(f, []byte("data"), 0644)

		sc := &mockStorageClient{}
		captureOutput(func() {
			runUpload(ctx, sc, "test-project", "test-ws", f, "1h", "")
		})

		if sc.signedURLOpts == nil {
			t.Fatal("SignedURL was not called")
		}
		wantDisp := `attachment; filename="report.pdf.zip"`
		gotDisp := sc.signedURLOpts.QueryParameters.Get("response-content-disposition")
		if gotDisp != wantDisp {
			t.Errorf("response-content-disposition = %q, want %q", gotDisp, wantDisp)
		}
		gotType := sc.signedURLOpts.QueryParameters.Get("response-content-type")
		if gotType != "application/zip" {
			t.Errorf("response-content-type = %q, want application/zip", gotType)
		}
	})
}

// Ensure mockStorageClient satisfies interface at compile time.
var _ StorageClient = (*mockStorageClient)(nil)
