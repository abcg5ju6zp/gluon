package tests

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/ProtonMail/gluon/imap"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestAppendRecovery_CleansReceivingAndCompletesAccepted(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "store")
	dbDir := filepath.Join(root, "db")
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	require.NoError(t, os.MkdirAll(dbDir, 0o700))

	builder := &streamingDummyConnectorBuilder{contentAddressed: true}
	options := defaultServerOptions(t,
		withDataDir(dataDir),
		withDatabaseDir(dbDir),
		withConnectorBuilder(builder),
		withAppendSpoolMemoryLimit(64),
	)

	// First server run: create one normal committed message (UID 1).
	normalLiteral := buildRFC5322TestLiteral("Subject: first\r\n\r\nfirst body")
	runOneToOneTestWithAuth(t, options, func(c *testConnection, _ *testSession) {
		tag := rawAppendPlus(c, "INBOX", normalLiteral)
		c.Sx(regexp.QuoteMeta(tag) + ` OK \[APPENDUID \d+ 1\]`)
	})

	userID := sha256Hex("user")
	dbPath := filepath.Join(dbDir, userID+".db")
	spoolDir := filepath.Join(dataDir, "appends", userID)

	// Accepted append: connector accepted before a crash; spool file survived on disk.
	acceptedAttempt := uuid.NewString()
	acceptedInternal := imap.NewInternalMessageID()
	recoveredLiteral := buildRFC5322TestLiteral("Subject: recovered\r\n\r\nrecovered after crash")
	require.NoError(t, os.WriteFile(filepath.Join(spoolDir, acceptedAttempt+".tmp"), []byte(recoveredLiteral), 0o600))

	// Receiving append: died while the client was uploading. Must be fully discarded.
	staleAttempt := uuid.NewString()
	require.NoError(t, os.WriteFile(filepath.Join(spoolDir, staleAttempt+".tmp"), []byte("partial"), 0o600))

	// Orphan spool file with no journal row at all.
	orphanAttempt := uuid.NewString()
	require.NoError(t, os.WriteFile(filepath.Join(spoolDir, orphanAttempt+".tmp"), []byte("orphan"), 0o600))

	seedJournalRows(t, dbPath, map[string]seedJournal{
		acceptedAttempt: {
			status:   3, // accepted: connector accepted before the crash
			remoteID: "recovered-remote-1",
			internal: acceptedInternal.String(),
			mailbox:  2,
			size:     int64(len(recoveredLiteral)),
			updated:  time.Now().Unix(),
		},
		staleAttempt: {status: 1, size: 7, updated: time.Now().Unix()},
	})

	// Second server run: startup recovery must finish the accepted append and drop the rest.
	runOneToOneTestWithAuth(t, options, func(c *testConnection, _ *testSession) {
		c.C("R1 STATUS INBOX (MESSAGES)")
		c.Sx(`MESSAGES 2`)
		c.OK("R1")

		c.C("R2 SELECT INBOX").OK("R2")
		c.C(`R3 UID FETCH 2 (BODY.PEEK[])`)
		c.Sx(regexp.QuoteMeta(recoveredLiteral))
		c.OK("R3")

		// The original message is still UID 1.
		c.C(`R4 UID FETCH 1 (BODY.PEEK[])`)
		c.Sx(regexp.QuoteMeta(normalLiteral))
		c.OK("R4")
	})

	// Verify journal state after the second run.
	conn, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer conn.Close()

	status, err := journalStatus(conn, acceptedAttempt)
	require.NoError(t, err)
	require.Equal(t, 4, status, "accepted append should be confirmed")

	uid, err := journalUID(conn, acceptedAttempt)
	require.NoError(t, err)
	require.Equal(t, uint32(2), uid)

	_, err = journalStatus(conn, staleAttempt)
	require.ErrorIs(t, err, sql.ErrNoRows, "receiving row should be deleted")

	entries, err := filepath.Glob(filepath.Join(spoolDir, "*.tmp"))
	require.NoError(t, err)
	require.Empty(t, entries, "no spool files should survive recovery")

	// Content-addressed connector must not have been called for the accepted recovery: only
	// the single normal append across both server runs creates a message remotely.
	requireZeroConnectorCreates(t, builder, 1)
}

type seedJournal struct {
	status   int
	remoteID string
	internal string
	mailbox  uint64
	size     int64
	updated  int64
}

func seedJournalRows(t *testing.T, dbPath string, rows map[string]seedJournal) {
	t.Helper()

	conn, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer conn.Close()

	for attempt, row := range rows {
		_, err = conn.Exec(
			`INSERT INTO append_journal_v1
			  (attempt_id, identity_hash, content_hash, mailbox_internal_id, remote_id, internal_id, uid, size, flags, date, status, created_at, updated_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			attempt, "", "", row.mailbox, row.remoteID, row.internal, 0, row.size, "[]", 0, row.status, row.updated, row.updated,
		)
		require.NoError(t, err)
	}

	require.NoError(t, conn.Close())
}

func journalStatus(conn *sql.DB, attempt string) (int, error) {
	var status int
	err := conn.QueryRow(`SELECT status FROM append_journal_v1 WHERE attempt_id = ?`, attempt).Scan(&status)
	return status, err
}

func journalUID(conn *sql.DB, attempt string) (uint32, error) {
	var uid uint32
	err := conn.QueryRow(`SELECT uid FROM append_journal_v1 WHERE attempt_id = ?`, attempt).Scan(&uid)
	return uid, err
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
