package httpcapture

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/google/uuid"
)

// Body parts and their file names
const (
	PartRequest  = "request"
	PartResponse = "response"
)

// BlobStore keeps captured bodies as files under one directory, opened as
// an os.Root: names that leave it (.., absolute paths, symbolic links
// pointing outside) cannot be opened, written or removed. Paths are built
// from server-generated IDs only, validated before use. Directories are
// created 0700 and files 0600 (POSIX); bodies are not encrypted, which the
// local-only listener and these permissions compensate for (SECURITY.md).
type BlobStore struct {
	dir  string
	root *os.Root
}

// OpenBlobStore opens (creating) the capture directory
func OpenBlobStore(dir string) (*BlobStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating capture directory: %w", err)
	}
	// MkdirAll keeps the mode of an existing directory: restrict it
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("restricting capture directory: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening capture directory: %w", err)
	}
	b := &BlobStore{dir: dir, root: root}
	for _, d := range []string{"runs", "tmp"} {
		if err := root.MkdirAll(d, 0o700); err != nil {
			root.Close()
			return nil, fmt.Errorf("creating %s in capture directory: %w", d, err)
		}
	}
	return b, nil
}

// Dir returns the capture directory
func (b *BlobStore) Dir() string { return b.dir }

// Close closes the directory
func (b *BlobStore) Close() error { return b.root.Close() }

// ValidID reports whether id is an ID this package generates (a UUID), the
// only names used in paths
func ValidID(id string) bool {
	u, err := uuid.Parse(id)
	return err == nil && u.String() == id
}

// NewID returns a new time-ordered ID (UUIDv7)
func NewID() string {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

// BodyPath returns the relative path of a stored body
func BodyPath(runID, exchangeID, part, variant string) (string, error) {
	if !ValidID(runID) || !ValidID(exchangeID) {
		return "", errors.New("invalid capture ID")
	}
	switch part {
	case PartRequest, PartResponse:
	default:
		return "", errors.New("invalid body part")
	}
	switch variant {
	case "safe", "raw":
	default:
		return "", errors.New("invalid body variant")
	}
	return path.Join("runs", runID, exchangeID+"."+part+"."+variant), nil
}

// checkRel refuses relative paths that are not a body path or a spool file
func checkRel(rel string) error {
	if rel == "" || path.IsAbs(rel) || strings.Contains(rel, "\\") || path.Clean(rel) != rel || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("invalid capture path %q", rel)
	}
	parts := strings.Split(rel, "/")
	switch {
	case len(parts) == 3 && parts[0] == "runs" && ValidID(parts[1]):
		return nil
	case len(parts) == 2 && parts[0] == "tmp":
		return nil
	}
	return fmt.Errorf("invalid capture path %q", rel)
}

// WriteFile stores data at rel (a BodyPath) atomically: written to a
// temporary file and renamed
func (b *BlobStore) WriteFile(rel string, data []byte) error {
	if err := checkRel(rel); err != nil {
		return err
	}
	if err := b.root.MkdirAll(path.Dir(rel), 0o700); err != nil {
		return err
	}
	tmp := path.Join("tmp", randomName()+".partial")
	f, err := b.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		b.root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		b.root.Remove(tmp)
		return err
	}
	if err := b.root.Rename(tmp, rel); err != nil {
		b.root.Remove(tmp)
		return err
	}
	return nil
}

// ReadFile reads at most max bytes of rel; truncated tells whether the file
// is longer
func (b *BlobStore) ReadFile(rel string, max int64) (data []byte, truncated bool, err error) {
	if err := checkRel(rel); err != nil {
		return nil, false, err
	}
	f, err := b.root.Open(rel)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("%s is not a regular file", rel)
	}
	data, err = io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}

// Spool is a temporary file a body is written to while it is observed
type Spool struct {
	b    *BlobStore
	rel  string
	f    *os.File
	size int64
}

// CreateSpool creates a temporary file in tmp/
func (b *BlobStore) CreateSpool() (*Spool, error) {
	rel := path.Join("tmp", randomName()+".spool")
	f, err := b.root.OpenFile(rel, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	return &Spool{b: b, rel: rel, f: f}, nil
}

// Write appends to the spool file
func (s *Spool) Write(p []byte) (int, error) {
	n, err := s.f.Write(p)
	s.size += int64(n)
	return n, err
}

// Size returns the bytes written
func (s *Spool) Size() int64 { return s.size }

// ReadAll reads the first max bytes written
func (s *Spool) ReadAll(max int64) ([]byte, error) {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(s.f, max))
}

// Keep closes the spool file and moves it to rel (a BodyPath)
func (s *Spool) Keep(rel string) error {
	if err := checkRel(rel); err != nil {
		s.Discard()
		return err
	}
	if err := s.f.Close(); err != nil {
		s.b.root.Remove(s.rel)
		return err
	}
	if err := s.b.root.MkdirAll(path.Dir(rel), 0o700); err != nil {
		s.b.root.Remove(s.rel)
		return err
	}
	if err := s.b.root.Rename(s.rel, rel); err != nil {
		s.b.root.Remove(s.rel)
		return err
	}
	return nil
}

// Discard closes and removes the spool file
func (s *Spool) Discard() {
	s.f.Close()
	s.b.root.Remove(s.rel)
}

// RemoveRun removes the stored bodies of a run
func (b *BlobStore) RemoveRun(runID string) error {
	if !ValidID(runID) {
		return errors.New("invalid run ID")
	}
	err := b.root.RemoveAll(path.Join("runs", runID))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// RunDirs lists the run directories present
func (b *BlobStore) RunDirs() ([]string, error) {
	entries, err := fs.ReadDir(b.root.FS(), "runs")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && ValidID(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// ClearTemp removes leftover temporary files (of a process that stopped
// while writing)
func (b *BlobStore) ClearTemp() error {
	entries, err := fs.ReadDir(b.root.FS(), "tmp")
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		if e.Type().IsRegular() {
			if err := b.root.Remove(path.Join("tmp", e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Size returns the bytes of all stored bodies
func (b *BlobStore) Size() (int64, error) {
	var total int64
	err := fs.WalkDir(b.root.FS(), "runs", func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func randomName() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
