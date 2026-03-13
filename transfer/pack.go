package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newPackCmd(sc StorageClient) *cobra.Command {
	var workspace, folder, expiry string
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Pack a folder into an AES-256 encrypted zip, upload, and print a signed URL and password",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := getProject(cmd.Context())
			if err != nil {
				return err
			}
			return runPack(cmd.Context(), sc, project, workspace, folder, expiry)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace name (required)")
	cmd.Flags().StringVar(&folder, "folder", "", "Local folder path to pack and upload (required)")
	cmd.Flags().StringVar(&expiry, "expiry", "1h", "URL lifetime: m/h/d (max 24h)")
	_ = cmd.MarkFlagRequired("workspace")
	_ = cmd.MarkFlagRequired("folder")
	return cmd
}

func runPack(ctx context.Context, sc StorageClient, project, workspace, folder, expiryStr string) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("folder not found: %s", folder)
	}

	expiry, err := parseExpiry(expiryStr)
	if err != nil {
		return err
	}
	password, err := generatePassword()
	if err != nil {
		return fmt.Errorf("generating password: %w", err)
	}

	zipName := filepath.Base(folder) + ".zip"
	bucket, signingServiceAccount := resolve(workspace, project)

	tmpDir, err := os.MkdirTemp("", "transfer-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, zipName)
	fmt.Printf("Packing    %s  →  %s  (AES-256)\n", folder, zipName)
	if err := createEncryptedZip(folder, zipPath, password); err != nil {
		return fmt.Errorf("creating encrypted zip: %w", err)
	}

	sha256sum, err := fileSHA256(zipPath)
	if err != nil {
		return err
	}

	fmt.Printf("Uploading  %s  →  gs://%s/%s\n", zipName, bucket, zipName)
	if err := sc.Upload(ctx, bucket, zipName, "application/zip", zipPath); err != nil {
		return fmt.Errorf("uploading: %w", err)
	}
	fmt.Printf("Upload complete.  SHA-256: %s\n", sha256sum)

	urlOpts, err := buildSignedURLOptions(ctx, signingServiceAccount, expiry, "GET", zipName, "application/zip")
	if err != nil {
		return err
	}
	url, err := sc.SignedURL(bucket, zipName, urlOpts)
	if err != nil {
		return fmt.Errorf("signing URL: %w", err)
	}
	printURLBlock(url, sha256sum, zipName, expiry, password)
	return nil
}
