package sqlite3

import (
	"context"
	"testing"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/stretchr/testify/require"
)

func newModSeqTestClient(t *testing.T) (*Client, context.Context) {
	t.Helper()

	client, _, err := NewClient(t.TempDir(), "user", false, false)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, client.Init(ctx, imap.DefaultEpochUIDValidityGenerator()))

	t.Cleanup(func() { require.NoError(t, client.Close()) })

	return client, ctx
}

func createModSeqMailbox(t *testing.T, ctx context.Context, tx db.Transaction, remoteID imap.MailboxID, uidValidity imap.UID) imap.InternalMailboxID {
	t.Helper()

	mbox, err := tx.CreateMailbox(
		ctx,
		remoteID,
		string(remoteID),
		imap.NewFlagSet(),
		imap.NewFlagSet(),
		imap.NewFlagSet(),
		uidValidity,
	)
	require.NoError(t, err)

	return mbox.ID
}

func TestModSeq_CreateBumpsMailboxWatermark(t *testing.T) {
	client, ctx := newModSeqTestClient(t)

	var internalID imap.InternalMessageID

	require.NoError(t, client.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		mboxID := createModSeqMailbox(t, ctx, tx, "mbox", 7)

		req := db.CreateMessageReq{
			Message: imap.Message{ID: imap.MessageID("msg-1")},
		}

		uid, _, modSeq, err := tx.CreateMessageAndAddToMailbox(ctx, mboxID, &req)
		require.NoError(t, err)
		require.Equal(t, imap.UID(1), uid)
		require.Equal(t, imap.ModSeq(1), modSeq)
		internalID = req.InternalID

		highest, err := tx.GetMailboxHighestModSeq(ctx, mboxID)
		require.NoError(t, err)
		require.Equal(t, imap.ModSeq(1), highest)

		results, err := tx.GetMailboxMessagesChangedSince(ctx, mboxID, 0)
		require.NoError(t, err)
		require.Len(t, results, 1)
		require.Equal(t, imap.ModSeq(1), results[0].ModSeq)
		require.Equal(t, internalID, results[0].InternalID)

		return nil
	}))
}

func TestModSeq_MetadataAssignmentAndOtherMailboxes(t *testing.T) {
	client, ctx := newModSeqTestClient(t)

	require.NoError(t, client.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		mboxA := createModSeqMailbox(t, ctx, tx, "mbox-a", 7)

		req := db.CreateMessageReq{
			Message: imap.Message{ID: imap.MessageID("msg-a")},
		}

		_, _, _, err := tx.CreateMessageAndAddToMailbox(ctx, mboxA, &req)
		require.NoError(t, err)

		mboxBID := createModSeqMailbox(t, ctx, tx, "mbox-b", 8)

		// A global flag change only bumps mailboxes containing the message.
		originModSeq, modSeqByMailbox, err := tx.AssignModSeqForMessageMetadata(
			ctx,
			mboxA,
			[]imap.InternalMessageID{req.InternalID},
			[]imap.InternalMessageID{req.InternalID},
		)
		require.NoError(t, err)
		require.Equal(t, imap.ModSeq(2), originModSeq)
		require.Equal(t, imap.ModSeq(2), modSeqByMailbox[mboxA])

		_, ok := modSeqByMailbox[mboxBID]
		require.False(t, ok)

		highestB, err := tx.GetMailboxHighestModSeq(ctx, mboxBID)
		require.NoError(t, err)
		require.Equal(t, imap.ModSeq(0), highestB)

		return nil
	}))
}

func TestModSeq_RemovalRecordsVanishedHistory(t *testing.T) {
	client, ctx := newModSeqTestClient(t)

	var mboxID imap.InternalMailboxID
	var internalID imap.InternalMessageID

	require.NoError(t, client.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		mboxID = createModSeqMailbox(t, ctx, tx, "mbox", 7)

		req := db.CreateMessageReq{
			Message: imap.Message{ID: imap.MessageID("msg-v")},
		}

		_, _, _, err := tx.CreateMessageAndAddToMailbox(ctx, mboxID, &req)
		require.NoError(t, err)

		internalID = req.InternalID

		return nil
	}))

	require.NoError(t, client.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		_, err := tx.RemoveMessagesFromMailbox(ctx, mboxID, []imap.InternalMessageID{internalID})

		return err
	}))

	require.NoError(t, client.Read(ctx, func(ctx context.Context, rd db.ReadOnly) error {
		vanished, err := rd.GetVanishedUIDsAfterModSeq(ctx, mboxID, 0)
		require.NoError(t, err)
		require.Equal(t, []imap.UID{1}, vanished)

		// Watermarks at or after the removal yield nothing further.
		vanished, err = rd.GetVanishedUIDsAfterModSeq(ctx, mboxID, 2)
		require.NoError(t, err)
		require.Empty(t, vanished)

		// No pruning has happened yet.
		boundary, err := rd.GetVanishedHistoryPrunedModSeq(ctx, mboxID)
		require.NoError(t, err)
		require.Equal(t, imap.ModSeq(0), boundary)

		return nil
	}))
}

func TestModSeq_SetUIDValidityResetsState(t *testing.T) {
	client, ctx := newModSeqTestClient(t)

	var mboxID imap.InternalMailboxID

	require.NoError(t, client.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		mboxID = createModSeqMailbox(t, ctx, tx, "mbox", 7)

		req := db.CreateMessageReq{
			Message: imap.Message{ID: imap.MessageID("msg-u")},
		}

		_, _, _, err := tx.CreateMessageAndAddToMailbox(ctx, mboxID, &req)
		require.NoError(t, err)

		return tx.SetMailboxUIDValidity(ctx, mboxID, imap.UID(99))
	}))

	require.NoError(t, client.Read(ctx, func(ctx context.Context, rd db.ReadOnly) error {
		mbox, err := rd.GetMailboxByID(ctx, mboxID)
		require.NoError(t, err)
		require.Equal(t, imap.UID(99), mbox.UIDValidity)
		require.Equal(t, imap.ModSeq(0), mbox.HighestModSeq)

		// History for the previous UIDVALIDITY epoch must be gone.
		vanished, err := rd.GetVanishedUIDsAfterModSeq(ctx, mboxID, 0)
		require.NoError(t, err)
		require.Empty(t, vanished)

		return nil
	}))
}
