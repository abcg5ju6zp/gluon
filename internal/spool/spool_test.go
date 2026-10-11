package spool

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func writeObject(t *testing.T, o *Object, data []byte, chunk int) {
	t.Helper()

	for offset := 0; offset < len(data); offset += chunk {
		end := offset + chunk
		if end > len(data) {
			end = len(data)
		}

		n, err := o.Write(data[offset:end])
		require.NoError(t, err)
		require.Equal(t, end-offset, n)
	}
}

func TestObject_MemoryBacked(t *testing.T) {
	mgr, err := NewManager(t.TempDir(), DefaultMemoryThreshold)
	require.NoError(t, err)

	data := []byte("My message body is here")

	o, err := mgr.Begin(int64(len(data)))
	require.NoError(t, err)
	require.False(t, o.OnDisk())

	writeObject(t, o, data, 4)
	require.Equal(t, int64(len(data)), o.Len())
	require.NoError(t, o.Close())

	require.Equal(t, sha256Hex(t, data), o.Hash())

	got, err := o.Bytes()
	require.NoError(t, err)
	require.Equal(t, data, got)

	r, err := o.Open()
	require.NoError(t, err)
	got2, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, data, got2)
}

func TestObject_SpillToDisk(t *testing.T) {
	mgr, err := NewManager(t.TempDir(), 16)
	require.NoError(t, err)

	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i % 251)
	}

	o, err := mgr.Begin(int64(len(data)))
	require.NoError(t, err)

	writeObject(t, o, data, 37)
	require.True(t, o.OnDisk())
	require.NoError(t, o.Close())

	info, err := os.Stat(filepath.Join(mgr.Dir(), o.AttemptID()+".tmp"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	require.Equal(t, sha256Hex(t, data), o.Hash())
	require.Equal(t, int64(len(data)), o.Len())

	got, err := o.Bytes()
	require.NoError(t, err)
	require.Equal(t, data, got)

	// Reopen through the manager like crash recovery would.
	reopened, err := mgr.Open(o.AttemptID())
	require.NoError(t, err)
	require.True(t, reopened.OnDisk())
	require.Equal(t, int64(len(data)), reopened.Len())

	got2, err := reopened.Bytes()
	require.NoError(t, err)
	require.Equal(t, data, got2)
	require.NoError(t, reopened.Remove())
}

func TestObject_DeclaredSizeIsEnforced(t *testing.T) {
	mgr, err := NewManager(t.TempDir(), 16)
	require.NoError(t, err)

	o, err := mgr.Begin(5)
	require.NoError(t, err)

	_, err = o.Write([]byte("123"))
	require.NoError(t, err)

	_, err = o.Write([]byte("456"))
	require.ErrorIs(t, err, ErrSpoolLimitExceeded)
	require.Equal(t, int64(3), o.Len())

	require.NoError(t, o.Remove())
}

func TestObject_RemoveIsIdempotent(t *testing.T) {
	mgr, err := NewManager(t.TempDir(), 1)
	require.NoError(t, err)

	data := make([]byte, 128)

	o, err := mgr.Begin(int64(len(data)))
	require.NoError(t, err)
	writeObject(t, o, data, 16)
	require.NoError(t, o.Close())
	require.True(t, o.OnDisk())

	require.NoError(t, o.Remove())
	require.NoError(t, o.Remove())

	_, err = os.Stat(filepath.Join(mgr.Dir(), o.AttemptID()+".tmp"))
	require.True(t, os.IsNotExist(err))
}

func TestManager_Sweep(t *testing.T) {
	dir := t.TempDir()

	mgr, err := NewManager(dir, 4)
	require.NoError(t, err)

	keep := make([][]byte, 0, 3)

	for i := 0; i < 3; i++ {
		data := make([]byte, 64+i)
		o, err := mgr.Begin(int64(len(data)))
		require.NoError(t, err)
		_, err = o.Write(data)
		require.NoError(t, err)
		require.NoError(t, o.Close())
		keep = append(keep, []byte(o.AttemptID()))
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	removed, err := mgr.Sweep()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		string(keep[0]), string(keep[1]), string(keep[2]),
	}, removed)

	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestManager_BeginRejectsInvalidSize(t *testing.T) {
	mgr, err := NewManager(t.TempDir(), DefaultMemoryThreshold)
	require.NoError(t, err)

	_, err = mgr.Begin(0)
	require.Error(t, err)
}
