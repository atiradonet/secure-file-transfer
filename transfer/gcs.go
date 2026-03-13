package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"cloud.google.com/go/storage"
	"golang.org/x/oauth2/google"
	iamv1 "google.golang.org/api/iam/v1"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type objectInfo struct {
	Name    string
	Size    int64
	Updated time.Time
}

// workspaceResources derives the GCS bucket name and signer service account
// email from the workspace name and GCP project.
func workspaceResources(workspace, project string) (bucket, signerSA string) {
	return "secure-transfer-" + workspace,
		"st-signer-" + workspace + "@" + project + ".iam.gserviceaccount.com"
}

// gcpProject reads the GCP project ID from Application Default Credentials.
func gcpProject(ctx context.Context) (string, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return "", fmt.Errorf("no Application Default Credentials found — run: gcloud auth application-default login")
	}
	if creds.ProjectID == "" {
		return "", fmt.Errorf("GCP project not set in ADC — run: gcloud config set project <project_id>")
	}
	return creds.ProjectID, nil
}

// gcsUpload uploads the file at localPath to gs://bucket/object.
func gcsUpload(ctx context.Context, bucket, object, localPath string) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("creating storage client: %w", err)
	}
	defer client.Close()

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()

	wc := client.Bucket(bucket).Object(object).NewWriter(ctx)
	wc.ContentType = "application/zip"
	if _, err := io.Copy(wc, f); err != nil {
		_ = wc.Close()
		return err
	}
	return wc.Close()
}

// gcsSignURL returns a V4 signed URL for the object using IAM signBlob.
// The URL includes response-content-disposition and response-content-type
// query parameters so the browser downloads the file with the correct name.
func gcsSignURL(ctx context.Context, bucket, object, signerSA string, expiry time.Duration) (string, error) {
	iamSvc, err := iamv1.NewService(ctx, option.WithScopes("https://www.googleapis.com/auth/cloud-platform"))
	if err != nil {
		return "", fmt.Errorf("creating IAM service: %w", err)
	}

	opts := &storage.SignedURLOptions{
		GoogleAccessID: signerSA,
		SignBytes: func(b []byte) ([]byte, error) {
			req := &iamv1.SignBlobRequest{
				BytesToSign: base64.StdEncoding.EncodeToString(b),
			}
			resp, err := iamSvc.Projects.ServiceAccounts.SignBlob(
				"projects/-/serviceAccounts/"+signerSA, req,
			).Context(ctx).Do()
			if err != nil {
				return nil, err
			}
			return base64.StdEncoding.DecodeString(resp.Signature)
		},
		Method:  "GET",
		Expires: time.Now().Add(expiry),
		Scheme:  storage.SigningSchemeV4,
		// V4 signed URLs enforce response headers via query parameters so
		// the browser downloads the file under the correct name.
		QueryParameters: url.Values{
			"response-content-disposition": []string{fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(object))},
			"response-content-type":        []string{"application/zip"},
		},
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("creating storage client: %w", err)
	}
	defer client.Close()

	return client.Bucket(bucket).SignedURL(object, opts)
}

// gcsListObjects returns metadata for all objects in the bucket whose name
// starts with prefix. An empty prefix lists all objects.
func gcsListObjects(ctx context.Context, bucket, prefix string) ([]objectInfo, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating storage client: %w", err)
	}
	defer client.Close()

	query := &storage.Query{}
	if prefix != "" {
		query.Prefix = prefix
	}

	var results []objectInfo
	it := client.Bucket(bucket).Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err != nil {
			if err == iterator.Done {
				break
			}
			return nil, err
		}
		results = append(results, objectInfo{
			Name:    attrs.Name,
			Size:    attrs.Size,
			Updated: attrs.Updated,
		})
	}
	return results, nil
}

// gcsDeleteObject deletes a single object from the bucket.
func gcsDeleteObject(ctx context.Context, bucket, object string) error {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("creating storage client: %w", err)
	}
	defer client.Close()
	return client.Bucket(bucket).Object(object).Delete(ctx)
}
