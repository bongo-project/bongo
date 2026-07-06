package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ErrInvalidHash is returned when a blob hash fails validation.
var ErrInvalidHash = errors.New("invalid blob hash")

// IsValidHash reports whether h is a valid SHA-256 blob hash:
// exactly 64 lowercase hexadecimal characters (0-9, a-f).
// Uppercase letters, path separators, and percent-encoded sequences are rejected.
// IsValidUserID reports whether a user ID is safe to embed in a filesystem
// path. User IDs are server-generated UUIDs; this allows only [A-Za-z0-9_-] so
// a crafted or mis-resolved ID (e.g. a raw email falling through a resolver)
// can never contain a path separator or traversal sequence and escape the
// per-user data directory.
func IsValidUserID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

func IsValidHash(h string) bool {
	if len(h) != 64 {
		return false
	}
	for i := range len(h) {
		c := h[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// BlobStore is a content-addressable blob store backed by the filesystem.
// Blobs are identified by their SHA-256 hash and stored in a flat, shared pool.
type BlobStore struct {
	blobDir string
	tmpDir  string
}

// NewBlobStore creates a blob store rooted at {dataDir}/blobs/.
func NewBlobStore(dataDir string) (*BlobStore, error) {
	blobDir := filepath.Join(dataDir, "blobs")
	tmpDir := filepath.Join(blobDir, "tmp")

	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return nil, fmt.Errorf("creating blob directories: %w", err)
	}

	return &BlobStore{blobDir: blobDir, tmpDir: tmpDir}, nil
}

// Put writes data and returns its SHA-256 hash. If a blob with the same hash
// already exists, the write is skipped (dedup).
func (bs *BlobStore) Put(data []byte) (string, error) {
	hash := sha256Hex(data)

	if bs.Exists(hash) {
		return hash, nil
	}

	if err := bs.writeAtomically(hash, func(f *os.File) error {
		_, err := f.Write(data)
		return err
	}); err != nil {
		return "", err
	}

	return hash, nil
}

// PutReader streams data from r and returns its SHA-256 hash and size.
// If a blob with the same hash already exists, the write is skipped.
func (bs *BlobStore) PutReader(r io.Reader) (string, int64, error) {
	// Write to temp file while computing hash.
	tmp, err := os.CreateTemp(bs.tmpDir, "blob-*")
	if err != nil {
		return "", 0, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	h := sha256.New()
	w := io.MultiWriter(tmp, h)

	size, err := io.Copy(w, r)
	if err != nil {
		closeErr := tmp.Close()
		removeErr := cleanupTempFile(tmpPath)
		return "", 0, errors.Join(fmt.Errorf("writing blob: %w", err), closeErr, removeErr)
	}

	if err := tmp.Close(); err != nil {
		removeErr := cleanupTempFile(tmpPath)
		return "", 0, errors.Join(fmt.Errorf("closing temp file: %w", err), removeErr)
	}

	hash := hex.EncodeToString(h.Sum(nil))

	if bs.Exists(hash) {
		if err := cleanupTempFile(tmpPath); err != nil {
			return "", 0, fmt.Errorf("cleaning duplicate blob temp file: %w", err)
		}
		return hash, size, nil
	}

	destDir := filepath.Join(bs.blobDir, hash[:2])
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		removeErr := cleanupTempFile(tmpPath)
		return "", 0, errors.Join(fmt.Errorf("creating prefix dir: %w", err), removeErr)
	}

	destPath := filepath.Join(destDir, hash)
	if err := os.Rename(tmpPath, destPath); err != nil {
		removeErr := cleanupTempFile(tmpPath)
		return "", 0, errors.Join(fmt.Errorf("renaming blob: %w", err), removeErr)
	}

	return hash, size, nil
}

// Get opens a blob by hash and returns a reader. The caller must close it.
// Returns ErrInvalidHash if hash is not a valid 64-char lowercase hex string.
func (bs *BlobStore) Get(hash string) (io.ReadCloser, error) {
	if !IsValidHash(hash) {
		return nil, fmt.Errorf("blob %q: %w", hash, ErrInvalidHash)
	}
	path := bs.blobPath(hash)
	f, err := os.Open(path) // #nosec G304 -- hash is validated as lowercase SHA-256 before deriving the path
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("blob %s: %w", hash, ErrNotFound)
		}
		return nil, fmt.Errorf("opening blob: %w", err)
	}
	return f, nil
}

// Exists reports whether a blob with the given hash exists on disk.
// Returns false for any hash that fails IsValidHash.
func (bs *BlobStore) Exists(hash string) bool {
	if !IsValidHash(hash) {
		return false
	}
	_, err := os.Stat(bs.blobPath(hash))
	return err == nil
}

// Delete removes a blob file.
// Returns ErrInvalidHash if hash is not a valid 64-char lowercase hex string.
func (bs *BlobStore) Delete(hash string) error {
	if !IsValidHash(hash) {
		return fmt.Errorf("blob %q: %w", hash, ErrInvalidHash)
	}
	path := bs.blobPath(hash)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting blob: %w", err)
	}
	return nil
}

// Verify re-hashes the blob file content and checks it matches the filename.
func (bs *BlobStore) Verify(hash string) (bool, error) {
	// Validate before building a filesystem path, consistent with Get/Exists/
	// Delete, so a crafted hash can never traverse out of the blob directory.
	if !IsValidHash(hash) {
		return false, fmt.Errorf("invalid blob hash")
	}
	f, err := os.Open(bs.blobPath(hash))
	if err != nil {
		return false, fmt.Errorf("opening blob for verify: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, fmt.Errorf("reading blob for verify: %w", err)
	}

	actual := hex.EncodeToString(h.Sum(nil))
	return actual == hash, nil
}

// List returns all blob hashes in the store.
func (bs *BlobStore) List() ([]string, error) {
	var hashes []string

	entries, err := os.ReadDir(bs.blobDir)
	if err != nil {
		return nil, fmt.Errorf("reading blob dir: %w", err)
	}

	for _, prefixEntry := range entries {
		if !prefixEntry.IsDir() || prefixEntry.Name() == "tmp" {
			continue
		}

		prefixDir := filepath.Join(bs.blobDir, prefixEntry.Name())
		blobEntries, err := os.ReadDir(prefixDir)
		if err != nil {
			return nil, fmt.Errorf("reading prefix dir %s: %w", prefixEntry.Name(), err)
		}

		for _, blobEntry := range blobEntries {
			if !blobEntry.IsDir() && len(blobEntry.Name()) == 64 {
				hashes = append(hashes, blobEntry.Name())
			}
		}
	}

	return hashes, nil
}

// Stats returns the total number of blobs and their combined size.
func (bs *BlobStore) Stats() (count int, totalSize int64) {
	entries, err := os.ReadDir(bs.blobDir)
	if err != nil {
		return 0, 0
	}
	for _, prefixEntry := range entries {
		if !prefixEntry.IsDir() || prefixEntry.Name() == "tmp" {
			continue
		}
		prefixDir := filepath.Join(bs.blobDir, prefixEntry.Name())
		blobEntries, err := os.ReadDir(prefixDir)
		if err != nil {
			continue
		}
		for _, blobEntry := range blobEntries {
			if !blobEntry.IsDir() && len(blobEntry.Name()) == 64 {
				count++
				if info, err := blobEntry.Info(); err == nil {
					totalSize += info.Size()
				}
			}
		}
	}
	return
}

func (bs *BlobStore) blobPath(hash string) string {
	if len(hash) < 2 {
		return filepath.Join(bs.blobDir, hash)
	}
	return filepath.Join(bs.blobDir, hash[:2], hash)
}

func (bs *BlobStore) writeAtomically(hash string, writeFn func(*os.File) error) error {
	tmp, err := os.CreateTemp(bs.tmpDir, "blob-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if err := writeFn(tmp); err != nil {
		closeErr := tmp.Close()
		removeErr := cleanupTempFile(tmpPath)
		return errors.Join(fmt.Errorf("writing blob: %w", err), closeErr, removeErr)
	}

	if err := tmp.Close(); err != nil {
		removeErr := cleanupTempFile(tmpPath)
		return errors.Join(fmt.Errorf("closing temp file: %w", err), removeErr)
	}

	// Re-check existence after write (another goroutine may have written it).
	if bs.Exists(hash) {
		if err := cleanupTempFile(tmpPath); err != nil {
			return fmt.Errorf("cleaning duplicate blob temp file: %w", err)
		}
		return nil
	}

	destDir := filepath.Join(bs.blobDir, hash[:2])
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		removeErr := cleanupTempFile(tmpPath)
		return errors.Join(fmt.Errorf("creating prefix dir: %w", err), removeErr)
	}

	destPath := filepath.Join(destDir, hash)
	if err := os.Rename(tmpPath, destPath); err != nil {
		// If rename fails because another goroutine created it, that's fine.
		if bs.Exists(hash) {
			if err := cleanupTempFile(tmpPath); err != nil {
				return fmt.Errorf("cleaning duplicate blob temp file: %w", err)
			}
			return nil
		}
		removeErr := cleanupTempFile(tmpPath)
		return errors.Join(fmt.Errorf("renaming blob: %w", err), removeErr)
	}

	return nil
}

func cleanupTempFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
