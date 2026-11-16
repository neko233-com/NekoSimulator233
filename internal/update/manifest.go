// Package update implements the local full-MSI update contract.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Manifest is trusted local configuration, not a remotely authenticated feed.
type Manifest struct {
	Version string `json:"version"`
	MSI     string `json:"msi"`
	SHA256  string `json:"sha256"`
}

func version(s string) ([3]uint64, error) {
	var result [3]uint64
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return result, fmt.Errorf("invalid MSI version %q", s)
	}
	for i, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" || (len(part) > 1 && part[0] == '0') {
			return result, fmt.Errorf("invalid MSI version %q", s)
		}
		n, err := strconv.ParseUint(part, 10, 16)
		if err != nil || (i < 2 && n > 255) {
			return result, fmt.Errorf("invalid MSI version %q", s)
		}
		result[i] = n
	}
	return result, nil
}

func newer(candidate, current string) (bool, error) {
	a, err := version(candidate)
	if err != nil {
		return false, err
	}
	b, err := version(current)
	if err != nil {
		return false, err
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i], nil
		}
	}
	return false, nil
}

// Load returns nil when no update is configured or when it is not newer.
func Load(path, current string) (*Manifest, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 64<<10))
	dec.DisallowUnknownFields()
	var m Manifest
	if err = dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("read update manifest: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("trailing data in update manifest")
	}
	isNew, err := newer(m.Version, current)
	if err != nil {
		return nil, err
	}
	if !isNew {
		return nil, nil
	}
	if !filepath.IsAbs(m.MSI) || strings.HasPrefix(m.MSI, `\\`) || !strings.EqualFold(filepath.Ext(m.MSI), ".msi") {
		return nil, errors.New("update MSI must be an absolute local path")
	}
	digest, err := hex.DecodeString(m.SHA256)
	if err != nil || len(digest) != sha256.Size {
		return nil, errors.New("invalid update SHA-256")
	}
	return &m, nil
}

// VerifyHash ensures the exact bytes match the hash in the locally trusted manifest.
func VerifyHash(r io.Reader, want string) error {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return errors.New("update MSI SHA-256 mismatch")
	}
	return nil
}
