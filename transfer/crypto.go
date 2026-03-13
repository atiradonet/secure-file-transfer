package main

import (
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"

	"github.com/yeka/zip"
)

const passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func generatePassword() (string, error) {
	b := make([]byte, 32)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
		if err != nil {
			return "", err
		}
		b[i] = passwordAlphabet[n.Int64()]
	}
	return string(b), nil
}

// createEncryptedZip creates an AES-256 encrypted zip at dest.
// source may be a single file or a directory (preserves structure).
func createEncryptedZip(source, dest, password string) error {
	outFile, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer outFile.Close()

	w := zip.NewWriter(outFile)
	defer w.Close()

	info, err := os.Stat(source)
	if err != nil {
		return err
	}

	if !info.IsDir() {
		return addFileToZip(w, source, info.Name(), password)
	}

	// Collect and sort for deterministic ordering
	var files []string
	err = filepath.Walk(source, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	base := filepath.Dir(source)
	for _, path := range files {
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		if err := addFileToZip(w, path, rel, password); err != nil {
			return err
		}
	}
	return nil
}

func addFileToZip(w *zip.Writer, path, nameInZip, password string) error {
	fw, err := w.Encrypt(nameInZip, password, zip.AES256Encryption)
	if err != nil {
		return fmt.Errorf("creating zip entry %q: %w", nameInZip, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(fw, f)
	return err
}
