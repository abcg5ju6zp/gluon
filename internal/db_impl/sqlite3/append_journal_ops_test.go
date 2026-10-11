package sqlite3

import (
	"context"
	"testing"
	"time"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/stretchr/testify/require"
)

func newMigratedTestClient(t *testing.T) *Client {
	t.Helper()

	c, isNew, err := NewClient(t.TempDir(), "test-user", false, false)
	require.NoError(t, err)
	require.True(t, isNew)

	require.NoError(t, c.Init(context.Background(), imap.DefaultEpochUIDValidityGenerator()))
	t.Cleanup(func() { _ = c.Close() })

	return c
}

func TestAppendJournal_StateTransitions(t *testing.T) {
	c := newMigratedTestClient(t)
	ctx := context.Background()

	now := time.Unix(1_700_000_000, 0).UTC()
	flags := []string{`\Seen`, `\Flagged`}
	date := time.Unix(1_600_000_000, 0).UTC()

	require.NoError(t, c.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.InsertAppendJournalReceiving(ctx, "attempt-1", 123, now)
	}))

	entry, err := db.ClientReadType(ctx, c, func(ctx context.Context, ro db.ReadOnly) (*db.AppendJournalEntry, error) {
		return ro.GetAppendJournal(ctx, "attempt-1")
	})
	require.NoError(t, err)
	require.Equal(t, db.AppendJournalReceiving, entry.Status)
	require.Equal(t, int64(123), entry.Size)

	internal := imap.NewInternalMessageID()

	require.NoError(t, c.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		if err := tx.SetAppendJournalSubmitting(ctx, "attempt-1", "identity", "content", 7, 0, flags, date, now); err != nil {
			return err
		}

		return tx.SetAppendJournalAccepted(ctx, "attempt-1", imap.MessageID("remote-1"), internal, now.Add(time.Second))
	}))

	require.NoError(t, c.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.SetAppendJournalConfirmed(ctx, "attempt-1", 7, 42, now.Add(2*time.Second))
	}))

	entry, err = db.ClientReadType(ctx, c, func(ctx context.Context, ro db.ReadOnly) (*db.AppendJournalEntry, error) {
		return ro.GetAppendJournalByIdentity(ctx, "identity")
	})
	require.NoError(t, err)
	require.Equal(t, db.AppendJournalConfirmed, entry.Status)
	require.Equal(t, imap.UID(42), entry.UID)
	require.Equal(t, imap.InternalMailboxID(7), entry.MailboxInternalID)
	require.Equal(t, imap.MessageID("remote-1"), entry.RemoteID)
	require.Equal(t, internal, entry.InternalID)
	require.Equal(t, "content", entry.ContentHash)
	require.ElementsMatch(t, flags, entry.Flags)
	require.Equal(t, date.Unix(), entry.Date.Unix())

	// Pruning keeps confirmed entries newer than the cutoff.
	pruned, err := db.ClientWriteType(ctx, c, func(ctx context.Context, tx db.Transaction) (int64, error) {
		return tx.DeleteConfirmedAppendJournalBefore(ctx, now.Add(time.Second))
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), pruned)

	pruned, err = db.ClientWriteType(ctx, c, func(ctx context.Context, tx db.Transaction) (int64, error) {
		return tx.DeleteConfirmedAppendJournalBefore(ctx, now.Add(time.Hour))
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), pruned)

	_, err = db.ClientReadType(ctx, c, func(ctx context.Context, ro db.ReadOnly) (struct{}, error) {
		_, err := ro.GetAppendJournal(ctx, "attempt-1")
		return struct{}{}, err
	})
	require.ErrorIs(t, err, db.ErrNotFound)
}

func TestAppendJournal_Delete(t *testing.T) {
	c := newMigratedTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.InsertAppendJournalReceiving(ctx, "attempt-x", 9, time.Now())
	}))

	require.NoError(t, c.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.DeleteAppendJournal(ctx, "attempt-x")
	}))

	_, err := db.ClientReadType(ctx, c, func(ctx context.Context, ro db.ReadOnly) (struct{}, error) {
		_, err := ro.GetAppendJournal(ctx, "attempt-x")
		return struct{}{}, err
	})
	require.ErrorIs(t, err, db.ErrNotFound)
}

func TestAppendJournal_GetAllAndTotalMessageSizeEmpty(t *testing.T) {
	c := newMigratedTestClient(t)
	ctx := context.Background()

	total, err := db.ClientReadType(ctx, c, func(ctx context.Context, ro db.ReadOnly) (int64, error) {
		return ro.GetTotalMessageSize(ctx)
	})
	require.NoError(t, err)
	require.Equal(t, int64(0), total)

	require.NoError(t, c.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		if err := tx.InsertAppendJournalReceiving(ctx, "a", 1, time.Now()); err != nil {
			return err
		}

		return tx.InsertAppendJournalReceiving(ctx, "b", 2, time.Now())
	}))

	entries, err := db.ClientReadType(ctx, c, func(ctx context.Context, ro db.ReadOnly) ([]*db.AppendJournalEntry, error) {
		return ro.GetAllAppendJournal(ctx)
	})
	require.NoError(t, err)
	require.Len(t, entries, 2)
}
