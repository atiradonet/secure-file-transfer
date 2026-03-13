package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newUploadCmd(sc StorageClient) *cobra.Command {
	var workspace, file, expiry, prefix string
	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Encrypt and upload a file, print a signed URL and password",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := getProject(cmd.Context())
			if err != nil {
				return err
			}
			return runUpload(cmd.Context(), sc, project, workspace, file, expiry, prefix)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace name (required)")
	cmd.Flags().StringVar(&file, "file", "", "Local file path (required)")
	cmd.Flags().StringVar(&expiry, "expiry", "1h", "URL lifetime: m/h/d (max 24h)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "Optional folder prefix inside the bucket")
	_ = cmd.MarkFlagRequired("workspace")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func runUpload(ctx context.Context, sc StorageClient, project, workspace, file, expiryStr, prefix string) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	if _, err := os.Stat(file); err != nil {
		return fmt.Errorf("file not found: %s", file)
	}
	expiry, err := parseExpiry(expiryStr)
	if err != nil {
		return err
	}
	password, err := generatePassword()
	if err != nil {
		return fmt.Errorf("generating password: %w", err)
	}

	zipName := filepath.Base(file) + ".zip"
	var objectName string
	if prefix != "" {
		objectName = strings.TrimRight(prefix, "/") + "/" + zipName
	} else {
		objectName = zipName
	}

	bucket, signingServiceAccount := resolve(workspace, project)

	tmpDir, err := os.MkdirTemp("", "transfer-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, zipName)
	fmt.Printf("Encrypting  %s  →  %s  (AES-256)\n", file, zipName)
	if err := createEncryptedZip(file, zipPath, password); err != nil {
		return fmt.Errorf("creating encrypted zip: %w", err)
	}

	sha256sum, err := fileSHA256(zipPath)
	if err != nil {
		return err
	}

	fmt.Printf("Uploading  %s  →  gs://%s/%s\n", zipName, bucket, objectName)
	if err := sc.Upload(ctx, bucket, objectName, "application/zip", zipPath); err != nil {
		return fmt.Errorf("uploading: %w", err)
	}
	fmt.Printf("Upload complete.  SHA-256: %s\n", sha256sum)

	urlOpts, err := buildSignedURLOptions(ctx, signingServiceAccount, expiry, "GET")
	if err != nil {
		return err
	}
	url, err := sc.SignedURL(bucket, objectName, urlOpts)
	if err != nil {
		return fmt.Errorf("signing URL: %w", err)
	}
	printURLBlock(url, sha256sum, zipName, expiry, password)
	return nil
}
