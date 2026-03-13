package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	iamv1 "google.golang.org/api/iam/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// StorageClient is the interface used by commands — allows injection in tests.
type StorageClient interface {
	Upload(ctx context.Context, bucket, object, contentType, localPath string) error
	SignedURL(bucket, object string, opts *storage.SignedURLOptions) (string, error)
	ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error)
	DeleteObject(ctx context.Context, bucket, object string) error
}

type ObjectInfo struct {
	Name    string
	Size    int64
	Updated time.Time
}

// realStorageClient wraps the GCS SDK.
type realStorageClient struct {
	client *storage.Client
}

func newStorageClient(ctx context.Context) (*realStorageClient, error) {
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &realStorageClient{client: c}, nil
}

func (r *realStorageClient) Upload(ctx context.Context, bucket, object, contentType, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	wc := r.client.Bucket(bucket).Object(object).NewWriter(ctx)
	wc.ContentType = contentType
	if _, err := io.Copy(wc, f); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}

func (r *realStorageClient) SignedURL(bucket, object string, opts *storage.SignedURLOptions) (string, error) {
	return r.client.Bucket(bucket).SignedURL(object, opts)
}

func (r *realStorageClient) ListObjects(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	var results []ObjectInfo
	query := &storage.Query{}
	if prefix != "" {
		query.Prefix = prefix
	}
	it := r.client.Bucket(bucket).Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err != nil {
			if err == iterator.Done {
				break
			}
			return nil, err
		}
		results = append(results, ObjectInfo{
			Name:    attrs.Name,
			Size:    attrs.Size,
			Updated: attrs.Updated,
		})
	}
	return results, nil
}

func (r *realStorageClient) DeleteObject(ctx context.Context, bucket, object string) error {
	return r.client.Bucket(bucket).Object(object).Delete(ctx)
}

// buildSignedURLOptions returns SignedURLOptions that use IAM signBlob.
func buildSignedURLOptions(ctx context.Context, signingServiceAccount string, expiry time.Duration, method string) (*storage.SignedURLOptions, error) {
	iamSvc, err := iamv1.NewService(ctx, option.WithScopes("https://www.googleapis.com/auth/cloud-platform"))
	if err != nil {
		return nil, fmt.Errorf("creating IAM service: %w", err)
	}
	return &storage.SignedURLOptions{
		GoogleAccessID: signingServiceAccount,
		SignBytes: func(b []byte) ([]byte, error) {
			req := &iamv1.SignBlobRequest{
				BytesToSign: base64.StdEncoding.EncodeToString(b),
			}
			resp, err := iamSvc.Projects.ServiceAccounts.SignBlob(
				"projects/-/serviceAccounts/"+signingServiceAccount, req,
			).Context(ctx).Do()
			if err != nil {
				return nil, err
			}
			return base64.StdEncoding.DecodeString(resp.Signature)
		},
		Method:  method,
		Expires: time.Now().Add(expiry),
		Scheme:  storage.SigningSchemeV4,
	}, nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), nil
}

func printURLBlock(url, sha256sum, objectName string, expiry time.Duration, password string) {
	expiresAt := time.Now().UTC().Add(expiry)
	fmt.Println()
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Shareable URL (expires %s):\n", expiresAt.Format("2006-01-02 15:04 UTC"))
	fmt.Println()
	fmt.Println(url)
	fmt.Println()
	fmt.Printf("Integrity:  SHA-256 = %s\n", sha256sum)
	fmt.Println(strings.Repeat("=", 72))
	if password != "" {
		fmt.Println()
		fmt.Println(strings.Repeat("─", 72))
		fmt.Println("PASSWORD — share via a separate channel, do NOT send with the URL:")
		fmt.Println()
		fmt.Println(password)
		fmt.Println(strings.Repeat("─", 72))
	}
}

// isNotFound returns true for GCS 404 errors.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if e, ok := err.(*googleapi.Error); ok {
		return e.Code == 404
	}
	return false
}

// getProject reads the GCP project from Application Default Credentials.
func getProject(ctx context.Context) (string, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", fmt.Errorf("no Application Default Credentials found — run: gcloud auth application-default login")
	}
	if creds.ProjectID == "" {
		return "", fmt.Errorf("GCP project not set in ADC — run: gcloud config set project <project_id>")
	}
	return creds.ProjectID, nil
}
