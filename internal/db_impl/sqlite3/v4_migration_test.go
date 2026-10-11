package sqlite3

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ProtonMail/gluon/imap"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type v4Backfill struct {
	mailboxObjects map[int64]string
	messageObjects map[string]string
	messageThreads map[string]string
	tokenMessages  int
}

func migrateV0AndInspectV4(t *testing.T, testData *testData, uidGenerator imap.UIDValidityGenerator) v4Backfill {
	t.Helper()

	testDir := t.TempDir()
	prepareV0Database(t, testDir, "foo", testData, uidGenerator)

	client, _, err := NewClient(testDir, "foo", false, false)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, client.Init(ctx, uidGenerator))

	result := v4Backfill{
		mailboxObjects: make(map[int64]string),
		messageObjects: make(map[string]string),
		messageThreads: make(map[string]string),
	}

	require.NoError(t, client.wrapTx(ctx, func(ctx context.Context, tx *sql.Tx, _ *logrus.Entry) error {
		if err := scanRows(ctx, tx, "SELECT id, object_id FROM mailboxes_v2", func(rows *sql.Rows) error {
			var (
				id       int64
				objectID string
			)

			if err := rows.Scan(&id, &objectID); err != nil {
				return err
			}

			result.mailboxObjects[id] = objectID

			return nil
		}); err != nil {
			return err
		}

		if err := scanRows(ctx, tx, "SELECT id, object_id, thread_id FROM messages_v2", func(rows *sql.Rows) error {
			var (
				id       string
				objectID string
				threadID string
			)

			if err := rows.Scan(&id, &objectID, &threadID); err != nil {
				return err
			}

			result.messageObjects[id] = objectID
			result.messageThreads[id] = threadID

			return nil
		}); err != nil {
			return err
		}

		return tx.QueryRowContext(ctx, "SELECT COUNT(DISTINCT message_id) FROM message_thread_tokens").Scan(&result.tokenMessages)
	}))

	require.NoError(t, client.Close())

	return result
}

func scanRows(ctx context.Context, tx *sql.Tx, query string, scan func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return err
	}

	defer rows.Close()

	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}

	return rows.Err()
}

func TestMigrationV4ObjectIdentifiers(t *testing.T) {
	uidGenerator := &imap.IncrementalUIDValidityGenerator{}
	testData := newTestData(uidGenerator)

	first := migrateV0AndInspectV4(t, testData, uidGenerator)

	// The backfill is deterministic: migrating the same starting database always yields the
	// same ids.
	second := migrateV0AndInspectV4(t, testData, uidGenerator)
	require.Equal(t, first.mailboxObjects, second.mailboxObjects)
	require.Equal(t, first.messageThreads, second.messageThreads)

	// Every mailbox gets a unique, non-empty MAILBOXID.
	require.Len(t, first.mailboxObjects, len(testData.mailboxes))
	uniqueMailboxIDs := make(map[string]struct{}, len(first.mailboxObjects))

	for _, objectID := range first.mailboxObjects {
		require.NotEmpty(t, objectID)
		_, err := uuid.Parse(objectID)
		require.NoError(t, err)
		uniqueMailboxIDs[objectID] = struct{}{}
	}

	require.Len(t, uniqueMailboxIDs, len(first.mailboxObjects))

	// Every message gets an EMAILID equal to its pre-existing internal message id, a non-empty
	// THREADID and participates in threading.
	require.Len(t, first.messageObjects, len(testData.messages))
	require.Equal(t, len(testData.messages), first.tokenMessages)

	for internalID, objectID := range first.messageObjects {
		require.Equal(t, internalID, objectID)
		require.NotEmpty(t, first.messageThreads[internalID])
	}
}
