package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/storage"
)

func TestRunList(t *testing.T) {
	ctx := context.Background()

	t.Run("empty returns 'empty' message", func(t *testing.T) {
		sc := &mockStorageClient{listedObjects: []ObjectInfo{}}
		out := captureOutput(func() {
			runList(ctx, sc, "test-project", "test-ws", "")
		})
		if !strings.Contains(strings.ToLower(out), "empty") {
			t.Errorf("expected 'empty' in output, got: %s", out)
		}
	})

	t.Run("non-empty prints name and size", func(t *testing.T) {
		sc := &mockStorageClient{
			listedObjects: []ObjectInfo{
				{Name: "report.pdf.zip", Size: 1024, Updated: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)},
			},
		}
		out := captureOutput(func() {
			runList(ctx, sc, "test-project", "test-ws", "")
		})
		if !strings.Contains(out, "report.pdf.zip") {
			t.Errorf("expected 'report.pdf.zip' in output, got: %s", out)
		}
		if !strings.Contains(out, "1,024") {
			t.Errorf("expected size '1,024' in output, got: %s", out)
		}
	})

	t.Run("prefix forwarded to ListObjects", func(t *testing.T) {
		scCustom := &prefixCaptureMock{inner: &mockStorageClient{}}
		captureOutput(func() {
			runList(ctx, scCustom, "test-project", "test-ws", "invoices/")
		})
		if scCustom.capturedPrefix != "invoices/" {
			t.Errorf("expected prefix 'invoices/', got %q", scCustom.capturedPrefix)
		}
	})
}

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
		{1073741824, "1,073,741,824"},
	}
	for _, c := range cases {
		got := formatSize(c.in)
		if got != c.want {
			t.Errorf("formatSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// prefixCaptureMock wraps mockStorageClient and records the prefix argument.
type prefixCaptureMock struct {
	inner          *mockStorageClient
	capturedPrefix string
}

func (p *prefixCaptureMock) Upload(ctx context.Context, bucket, object, contentType, localPath string) error {
	return p.inner.Upload(ctx, bucket, object, contentType, localPath)
}

func (p *prefixCaptureMock) SignedURL(bucket, object string, opts *storage.SignedURLOptions) (string, error) {
	return p.inner.SignedURL(bucket, object, opts)
}

func (p *prefixCaptureMock) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	p.capturedPrefix = prefix
	return p.inner.ListObjects(ctx, bucket, prefix)
}

func (p *prefixCaptureMock) DeleteObject(ctx context.Context, bucket, object string) error {
	return p.inner.DeleteObject(ctx, bucket, object)
}
