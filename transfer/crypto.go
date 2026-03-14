package main

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// kdfIterations is the PBKDF2 iteration count. Must match the JavaScript
	// in the HTML template — both sides derive the same key from the password.
	kdfIterations = 100_000
)

// generatePassword returns a 32-character alphanumeric password from crypto/rand.
// The alphabet is intentionally limited to a-zA-Z0-9 so the password can be
// typed or read aloud without ambiguity — the trade-off for ~190 bits of entropy
// vs. the ~256 bits that a full printable-ASCII alphabet would give.
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

// createSecureBundle encrypts source and writes a self-contained HTML file to dst.
// The HTML file decrypts entirely in the browser using the Web Crypto API —
// no plugins, extensions, or installed software required on any platform.
//
// source may be a single file or a directory. For a directory, all files are
// first packed into an unencrypted zip (openable natively on macOS and Windows),
// then the zip bytes are AES-256-GCM encrypted and embedded.
//
// The decrypted file name is returned so the caller can name the upload correctly.
func createSecureBundle(source string, dst io.Writer, password string) (decryptedName string, err error) {
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}

	var plaintext []byte
	if info.IsDir() {
		var buf bytes.Buffer
		if err := packToZip(source, &buf); err != nil {
			return "", fmt.Errorf("packing folder: %w", err)
		}
		plaintext = buf.Bytes()
		decryptedName = filepath.Base(source) + ".zip"
	} else {
		plaintext, err = os.ReadFile(source)
		if err != nil {
			return "", err
		}
		decryptedName = info.Name()
	}

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	key := pbkdf2.Key([]byte(password), salt, kdfIterations, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	filenameJSON, err := json.Marshal(decryptedName)
	if err != nil {
		return "", err
	}

	html := strings.NewReplacer(
		"{{TITLE}}", decryptedName,
		"{{FILENAME_JS}}", string(filenameJSON),
		"{{SALT}}", base64.StdEncoding.EncodeToString(salt),
		"{{NONCE}}", base64.StdEncoding.EncodeToString(nonce),
		"{{DATA}}", base64.StdEncoding.EncodeToString(ciphertext),
	).Replace(decryptTemplate)

	_, err = io.WriteString(dst, html)
	return decryptedName, err
}

// packToZip writes source directory as an unencrypted zip to dst.
// Files are stored relative to the parent of source.
// Walk order is sorted for deterministic output.
func packToZip(source string, dst io.Writer) error {
	w := zip.NewWriter(dst)
	var files []string
	err := filepath.Walk(source, func(path string, fi os.FileInfo, err error) error {
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
		fw, err := w.Create(rel)
		if err != nil {
			return fmt.Errorf("creating zip entry %q: %w", rel, err)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(fw, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return w.Close()
}
