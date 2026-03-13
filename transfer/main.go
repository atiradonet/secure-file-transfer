package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	ctx := context.Background()
	var err error

	switch os.Args[1] {
	case "upload":
		err = cmdUpload(ctx, os.Args[2:])
	case "pack":
		err = cmdPack(ctx, os.Args[2:])
	case "list":
		err = cmdList(ctx, os.Args[2:])
	case "delete":
		err = cmdDelete(ctx, os.Args[2:])
	case "--help", "-h", "help":
		printUsage()
		return
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown subcommand %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		var gErr *googleapi.Error
		if errors.As(err, &gErr) {
			switch gErr.Code {
			case 403:
				fmt.Fprintf(os.Stderr, "Error: permission denied. Verify your account is listed in GCP_SIGNING_MEMBERS.\n"+
					"If you just provisioned the workspace, wait 90 s for IAM to propagate and retry.\n")
			case 404:
				fmt.Fprintf(os.Stderr, "Error: resource not found. Check the workspace name and that infrastructure has been provisioned.\n")
			default:
				fmt.Fprintf(os.Stderr, "Error: GCP API call failed (HTTP %d): %s\n", gErr.Code, gErr.Message)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}

// cmdUpload parses flags for the upload subcommand and calls transfer().
func cmdUpload(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace name (required)")
	file := fs.String("file", "", "Local file path (required)")
	expiry := fs.String("expiry", "1h", "URL lifetime: m/h/d (max 24h)")
	prefix := fs.String("prefix", "", "Optional folder prefix inside the bucket")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: transfer upload --workspace NAME --file PATH [--expiry DURATION] [--prefix PREFIX]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if *file == "" {
		return fmt.Errorf("--file is required")
	}
	if err := validateWorkspace(*workspace); err != nil {
		return err
	}
	if _, err := os.Stat(*file); err != nil {
		return fmt.Errorf("file not found: %s", *file)
	}
	d, err := parseExpiry(*expiry)
	if err != nil {
		return err
	}
	return transfer(ctx, *workspace, *file, *prefix, d)
}

// cmdPack parses flags for the pack subcommand and calls transfer().
func cmdPack(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace name (required)")
	folder := fs.String("folder", "", "Local folder path to pack and upload (required)")
	expiry := fs.String("expiry", "1h", "URL lifetime: m/h/d (max 24h)")
	prefix := fs.String("prefix", "", "Optional folder prefix inside the bucket")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: transfer pack --workspace NAME --folder PATH [--expiry DURATION] [--prefix PREFIX]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if *folder == "" {
		return fmt.Errorf("--folder is required")
	}
	if err := validateWorkspace(*workspace); err != nil {
		return err
	}
	info, err := os.Stat(*folder)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("folder not found: %s", *folder)
	}
	d, err := parseExpiry(*expiry)
	if err != nil {
		return err
	}
	return transfer(ctx, *workspace, *folder, *prefix, d)
}

// cmdList lists objects in the workspace bucket.
func cmdList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace name (required)")
	prefix := fs.String("prefix", "", "Filter by prefix")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: transfer list --workspace NAME [--prefix PREFIX]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if err := validateWorkspace(*workspace); err != nil {
		return err
	}

	project, err := gcpProject(ctx)
	if err != nil {
		return err
	}
	bucket, _ := workspaceResources(*workspace, project)

	objects, err := gcsListObjects(ctx, bucket, *prefix)
	if err != nil {
		return fmt.Errorf("listing objects: %w", err)
	}
	if len(objects) == 0 {
		fmt.Println("Bucket is empty.")
		return nil
	}
	fmt.Printf("%-60s  %12s  %s\n", "Object", "Size", "Updated")
	fmt.Println(strings.Repeat("-", 90))
	for _, obj := range objects {
		fmt.Printf("%-60s  %12s  %s\n", obj.Name, formatSize(obj.Size), obj.Updated.UTC().Format("2006-01-02 15:04 UTC"))
	}
	return nil
}

// cmdDelete deletes an object from the workspace bucket with a --confirm guard.
func cmdDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	workspace := fs.String("workspace", "", "Workspace name (required)")
	object := fs.String("object", "", "Object name to delete (required)")
	confirm := fs.String("confirm", "", "Repeat the object name to confirm deletion (required)")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: transfer delete --workspace NAME --object NAME --confirm NAME\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workspace == "" {
		return fmt.Errorf("--workspace is required")
	}
	if *object == "" {
		return fmt.Errorf("--object is required")
	}
	if *confirm == "" {
		return fmt.Errorf("--confirm is required")
	}
	if err := validateWorkspace(*workspace); err != nil {
		return err
	}
	if *confirm != *object {
		return fmt.Errorf("confirmation does not match object name.\nTo delete %q, pass --confirm %q", *object, *object)
	}

	project, err := gcpProject(ctx)
	if err != nil {
		return err
	}
	bucket, _ := workspaceResources(*workspace, project)

	if err := gcsDeleteObject(ctx, bucket, *object); err != nil {
		return fmt.Errorf("deleting object: %w", err)
	}
	fmt.Printf("Deleted gs://%s/%s\n", bucket, *object)
	return nil
}

// transfer is the shared workflow for upload and pack:
// encrypt → upload → sign → print result.
// source may be a file (upload) or a directory (pack).
func transfer(ctx context.Context, workspace, source, prefix string, expiry time.Duration) error {
	project, err := gcpProject(ctx)
	if err != nil {
		return err
	}
	bucket, signerSA := workspaceResources(workspace, project)

	password, err := generatePassword()
	if err != nil {
		return fmt.Errorf("generating password: %w", err)
	}

	zipName := filepath.Base(source) + ".zip"
	var objectName string
	if prefix != "" {
		objectName = strings.TrimRight(prefix, "/") + "/" + zipName
	} else {
		objectName = zipName
	}

	tmpDir, err := os.MkdirTemp("", "transfer-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, zipName)

	// Use "Packing" for folders, "Encrypting" for files.
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		fmt.Printf("Packing  %s  →  %s  (AES-256)\n", source, zipName)
	} else {
		fmt.Printf("Encrypting  %s  →  %s  (AES-256)\n", source, zipName)
	}

	if err := createEncryptedZip(source, zipPath, password); err != nil {
		return fmt.Errorf("creating encrypted zip: %w", err)
	}

	checksum, err := fileSHA256(zipPath)
	if err != nil {
		return err
	}

	fmt.Printf("Uploading  %s  →  gs://%s/%s\n", zipName, bucket, objectName)
	if err := gcsUpload(ctx, bucket, objectName, zipPath); err != nil {
		return fmt.Errorf("uploading: %w", err)
	}
	fmt.Printf("Upload complete.  SHA-256: %s\n", checksum)

	signedURL, err := gcsSignURL(ctx, bucket, objectName, signerSA, expiry)
	if err != nil {
		return fmt.Errorf("signing URL: %w", err)
	}

	printResult(signedURL, checksum, zipName, expiry, password)
	return nil
}

// workspaceRE enforces the same format as scripts/transfer.py and .github/workflows/terraform.yml.
// If the allowed format changes, update all three locations.
var workspaceRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,47}[a-z0-9]$`)

// validateWorkspace checks that name conforms to the workspace naming rules.
// Same regex as scripts/transfer.py and .github/workflows/terraform.yml.
func validateWorkspace(name string) error {
	if !workspaceRE.MatchString(name) {
		return fmt.Errorf("invalid workspace name %q: must be 3-49 chars, lowercase letters, numbers, and hyphens only; cannot start or end with a hyphen", name)
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("invalid workspace name %q: cannot contain consecutive hyphens", name)
	}
	return nil
}

// parseExpiry parses a duration string with m/h/d suffix. Maximum is 24h.
func parseExpiry(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("invalid expiry %q: use a number followed by m/h/d (e.g. 30m, 4h, 1d)", s)
	}
	unit := s[len(s)-1]
	amount, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("invalid expiry %q: %w", s, err)
	}
	if amount <= 0 {
		return 0, fmt.Errorf("expiry must be a positive number")
	}
	var d time.Duration
	switch unit {
	case 'm':
		d = time.Duration(amount) * time.Minute
	case 'h':
		d = time.Duration(amount) * time.Hour
	case 'd':
		d = time.Duration(amount) * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid expiry unit %q: use m, h, or d", string(unit))
	}
	if d > 24*time.Hour {
		return 0, fmt.Errorf("expiry cannot exceed 24 hours — files are auto-deleted after 1 day")
	}
	return d, nil
}

// printResult prints the URL block and password block to stdout.
func printResult(url, checksum, filename string, expiry time.Duration, password string) {
	expiresAt := time.Now().UTC().Add(expiry)
	fmt.Println()
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("Shareable URL (expires %s):\n", expiresAt.Format("2006-01-02 15:04 UTC"))
	fmt.Println()
	fmt.Println(url)
	fmt.Println()
	fmt.Printf("Integrity:  SHA-256 = %s\n", checksum)
	fmt.Println(strings.Repeat("=", 72))
	fmt.Println()
	fmt.Println(strings.Repeat("─", 72))
	fmt.Println("PASSWORD — share via a separate channel, do NOT send with the URL:")
	fmt.Println()
	fmt.Println(password)
	fmt.Println(strings.Repeat("─", 72))
}

// formatSize formats a byte count with comma-separated thousands, e.g. 1,048,576.
func formatSize(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// printUsage prints the top-level usage text.
func printUsage() {
	fmt.Print(`transfer — Secure File Transfer: encrypt, upload, and generate signed download URLs

Usage:
  transfer <subcommand> [flags]

Subcommands:
  upload   Encrypt and upload a file; print a signed URL and password
  pack     Pack a folder into an encrypted zip, upload; print a signed URL and password
  list     List objects in the workspace bucket
  delete   Delete an object from the workspace bucket

Run "transfer <subcommand> --help" for subcommand flags.
`)
}
