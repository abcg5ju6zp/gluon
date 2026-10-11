// Package spool provides size-restricted temporary objects for streaming APPEND uploads.
// Small objects stay memory backed; once the in-memory threshold is exceeded, bytes are
// spilled to a restricted (0600, O_EXCL) temporary file named after the upload attempt ID.
//
// Every object is bounded by its declared size: writing more bytes than declared fails with
// ErrSpoolLimitExceeded. A SHA-256 of all received bytes is accumulated while writing.
package spool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	// DefaultMemoryThreshold is the maximum size kept in memory before spilling to disk.
	DefaultMemoryThreshold int64 = 256 * 1024

	tmpSuffix = ".tmp"
)

var (
	ErrSpoolLimitExceeded = errors.New("append spool: received more bytes than the declared size")
	ErrSpoolClosed        = errors.New("append spool: object is closed")
	ErrSpoolDisk          = errors.New("append spool: failed to write temporary object")
)

// Manager owns the directory in which on-disk spool objects live. It is safe for concurrent
// use; individual Objects are written by a single goroutine.
type Manager struct {
	dir          string
	memThreshold int64
}

// NewManager creates (if needed) and returns a manager rooted at dir.
func NewManager(dir string, memThreshold int64) (*Manager, error) {
	if memThreshold <= 0 {
		memThreshold = DefaultMemoryThreshold
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: failed to create spool directory: %v", ErrSpoolDisk, err)
	}

	return &Manager{dir: dir, memThreshold: memThreshold}, nil
}

// Dir returns the spool root directory.
func (m *Manager) Dir() string {
	return m.dir
}

func (m *Manager) path(attemptID string) string {
	return filepath.Join(m.dir, attemptID+tmpSuffix)
}

// Begin creates a new object bounded by declared bytes. No file is created while the object
// stays under the memory threshold.
func (m *Manager) Begin(declared int64) (*Object, error) {
	if declared <= 0 {
		return nil, fmt.Errorf("%w: invalid declared size %d", ErrSpoolDisk, declared)
	}

	attemptID := uuid.NewString()

	return &Object{
		manager:   m,
		attemptID: attemptID,
		declared:  declared,
		mem:       &bytes.Buffer{},
		hash:      sha256.New(),
	}, nil
}

// Open reopens an existing on-disk object by attempt ID (used during crash recovery). The
// content hash is not recomputed; callers read it from the persisted journal entry.
func (m *Manager) Open(attemptID string) (*Object, error) {
	if _, err := uuid.Parse(attemptID); err != nil {
		return nil, fmt.Errorf("%w: invalid attempt id: %v", ErrSpoolDisk, err)
	}

	f, err := os.Open(m.path(attemptID))
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	return &Object{
		manager:   m,
		attemptID: attemptID,
		declared:  info.Size(),
		file:      f,
		written:   info.Size(),
		ondisk:    true,
		reopened:  true,
	}, nil
}

// Remove deletes the object's temporary file if it exists. Missing files are not an error.
func (m *Manager) Remove(attemptID string) error {
	if err := os.Remove(m.path(attemptID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}

// Sweep removes every temporary object file in the spool directory and returns the removed
// attempt IDs. It runs at startup to clean up uploads interrupted by a process exit.
func (m *Manager) Sweep() ([]string, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}

		return nil, err
	}

	var removed []string

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != tmpSuffix {
			continue
		}

		if err := os.Remove(filepath.Join(m.dir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}

		removed = append(removed, entry.Name()[:len(entry.Name())-len(tmpSuffix)])
	}

	return removed, nil
}

// Object is a single in-progress upload.
type Object struct {
	manager *Manager

	attemptID string
	declared  int64
	written   int64

	mem  *bytes.Buffer
	file *os.File

	hash     hash.Hash
	ondisk   bool
	reopened bool
	closed   bool
	removed  bool
}

func (o *Object) AttemptID() string {
	return o.attemptID
}

// Declared returns the size declared by the client.
func (o *Object) Declared() int64 {
	return o.declared
}

// Len returns the number of bytes received so far.
func (o *Object) Len() int64 {
	return o.written
}

// OnDisk reports whether the object spilled to a temporary file.
func (o *Object) OnDisk() bool {
	return o.ondisk
}

// Hash returns the hex SHA-256 of all received bytes. It is final once Close has been called.
func (o *Object) Hash() string {
	if o.hash == nil {
		return ""
	}

	return hex.EncodeToString(o.hash.Sum(nil))
}

func (o *Object) Write(p []byte) (int, error) {
	if o.closed || o.removed {
		return 0, ErrSpoolClosed
	}

	if o.reopened {
		return 0, fmt.Errorf("%w: reopened objects are read-only", ErrSpoolDisk)
	}

	if int64(len(p)) > o.declared-o.written {
		return 0, ErrSpoolLimitExceeded
	}

	// Spill to disk before receiving bytes that would grow the object past the threshold.
	if o.mem != nil && o.written+int64(len(p)) > o.manager.memThreshold {
		if err := o.spill(); err != nil {
			return 0, err
		}
	}

	// Hash every byte regardless of the backing.
	if _, err := o.hash.Write(p); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSpoolDisk, err)
	}

	var (
		n   int
		err error
	)

	if o.mem != nil {
		n, err = o.mem.Write(p)
	} else {
		n, err = o.file.Write(p)
	}

	if err != nil {
		return n, fmt.Errorf("%w: %v", ErrSpoolDisk, err)
	}

	o.written += int64(n)

	return n, nil
}

func (o *Object) spill() error {
	f, err := os.OpenFile(o.manager.path(o.attemptID), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%w: failed to create temporary file: %v", ErrSpoolDisk, err)
	}

	if _, err := f.Write(o.mem.Bytes()); err != nil {
		_ = f.Close()
		_ = o.manager.Remove(o.attemptID)

		return fmt.Errorf("%w: failed to spill to temporary file: %v", ErrSpoolDisk, err)
	}

	o.file = f
	o.mem = nil
	o.ondisk = true

	return nil
}

// Close seals the object. On-disk objects may be reopened afterwards with Open.
func (o *Object) Close() error {
	if o.closed || o.removed {
		return nil
	}

	o.closed = true

	if o.file != nil {
		if err := o.file.Sync(); err != nil {
			return fmt.Errorf("%w: %v", ErrSpoolDisk, err)
		}

		if err := o.file.Close(); err != nil {
			return fmt.Errorf("%w: %v", ErrSpoolDisk, err)
		}

		o.file = nil
	}

	return nil
}

// Open returns a seekable reader over the object contents.
func (o *Object) Open() (io.ReadSeekCloser, error) {
	if o.removed {
		return nil, fmt.Errorf("%w: object removed", ErrSpoolDisk)
	}

	if !o.ondisk {
		if o.mem == nil {
			return nil, fmt.Errorf("%w: no contents", ErrSpoolDisk)
		}

		return nopSeekCloser{bytes.NewReader(o.mem.Bytes())}, nil
	}

	if o.file != nil {
		// Reopened (read-only) handle: rewind and reuse it.
		if _, err := o.file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}

		return o.file, nil
	}

	f, err := os.Open(o.manager.path(o.attemptID))
	if err != nil {
		return nil, err
	}

	return f, nil
}

// Bytes materializes the object contents. For on-disk objects the file is fully read; the
// caller is responsible for keeping large reads short lived.
func (o *Object) Bytes() ([]byte, error) {
	r, err := o.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()

	return io.ReadAll(r)
}

// Remove deletes the temporary file (disk objects only) and marks the object unusable. It is
// idempotent.
func (o *Object) Remove() error {
	if o.removed {
		return nil
	}

	o.removed = true

	if o.file != nil {
		_ = o.file.Close()
		o.file = nil
	}

	o.mem = nil

	if o.ondisk {
		return o.manager.Remove(o.attemptID)
	}

	return nil
}

type nopSeekCloser struct {
	*bytes.Reader
}

func (nopSeekCloser) Close() error { return nil }
