package tests

import (
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/gluon/connector"
	"github.com/ProtonMail/gluon/imap"
	"github.com/stretchr/testify/require"
)

// slowDummyConnectorBuilder uses a very long update ticker period so that
// asynchronous dummy connector updates never fire during a test, keeping
// mod-sequences fully deterministic.
type slowDummyConnectorBuilder struct{}

func (*slowDummyConnectorBuilder) New(usernames []string, password []byte, _ time.Duration, flags, permFlags, attrs imap.FlagSet) Connector {
	return connector.NewDummy(usernames, password, 24*time.Hour, flags, permFlags, attrs)
}

func condStoreOptions(t *testing.T) *serverOptions {
	return defaultServerOptions(t,
		withUIDValidityGenerator(imap.NewFixedUIDValidityGenerator(imap.UID(1))),
		withConnectorBuilder(&slowDummyConnectorBuilder{}),
	)
}

// drainUntil reads responses up to and including the tagged response.
func drainUntil(c *testConnection, tag string) []string {
	var lines []string

	for {
		line := string(c.read())
		lines = append(lines, line)

		if strings.HasPrefix(line, tag+" ") {
			return lines
		}
	}
}

func requireLine(t *testing.T, lines []string, want string) {
	t.Helper()

	for _, line := range lines {
		if strings.Contains(line, want) {
			return
		}
	}

	require.Failf(t, "response not found", "looking for %q in %q", want, lines)
}

func requireNoLine(t *testing.T, lines []string, want string) {
	t.Helper()

	for _, line := range lines {
		if strings.Contains(line, want) {
			require.Failf(t, "unexpected response", "did not expect %q in %q", want, lines)
		}
	}
}

// TestEnableAndCapabilities verifies ENABLE negotiation and capability
// advertisement.
func TestEnableAndCapabilities(t *testing.T) {
	runOneToOneTest(t, defaultServerOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A001 ENABLE CONDSTORE")
		c.S("A001 NO session is not authenticated")

		c.Login("user", "pass")

		c.C("B001 ENABLE CONDSTORE")
		c.S("* ENABLE CONDSTORE", "B001 OK ENABLE")

		c.C("B002 ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "B002 OK ENABLE")

		c.C("B003 ENABLE FROZEN")
		c.Sx(`^B003 BAD ENABLE: unsupported capability FROZEN\r\n`)

		c.C("B004 CAPABILITY")
		c.Sxe(`\* CAPABILITY .*CONDSTORE.*ENABLE.*QRESYNC.*`)
		c.S("B004 OK CAPABILITY")
	})
}

// TestCondStoreSelectAndFetch verifies HIGHESTMODSEQ advertisement, per
// message MODSEQ FETCH values and STORE watermarks.
func TestCondStoreSelectAndFetch(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE CONDSTORE")
		c.S("* ENABLE CONDSTORE", "A OK ENABLE")

		c.C("B SELECT INBOX (CONDSTORE)")
		c.Sxe(`\* OK \[HIGHESTMODSEQ 0\]`)
		c.OK("B")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 2@pm.me")).expect("OK")
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 3@pm.me")).expect("OK")

		c.C("C UID FETCH 1:* (UID MODSEQ)")
		c.S(
			`* 1 FETCH (UID 1 MODSEQ (1))`,
			`* 2 FETCH (UID 2 MODSEQ (2))`,
			`* 3 FETCH (UID 3 MODSEQ (3))`,
		)
		c.OK("C")

		// Non-silent STORE reports MODSEQ in the FETCH and tagged response.
		c.C("D UID STORE 2 +FLAGS (\\Seen)")
		c.Sx(`^\* 2 FETCH \(FLAGS \([^)]*\\Seen[^)]*\) UID 2 MODSEQ \(4\)\)\r\n`)
		c.Sxe(`^D OK \[MODSEQ 4\]`)

		// Silent STORE reports only the tagged watermark.
		c.C("E UID STORE 3 +FLAGS.SILENT (\\Flagged)")
		c.Sxe(`^E OK \[MODSEQ 5\]`)

		// CHANGEDSINCE only returns the messages modified after the watermark.
		c.C("F UID FETCH 1:* CHANGEDSINCE 3 (UID FLAGS MODSEQ)")
		c.Sx(
			`^\* 2 FETCH \(UID 2 FLAGS \([^)]*\\Seen[^)]*\) MODSEQ \(4\)\)\r\n`,
			`^\* 3 FETCH \(UID 3 FLAGS \([^)]*\\Flagged[^)]*\) MODSEQ \(5\)\)\r\n`,
		)
		c.OK("F")

		// STATUS exposes the current highest mod-sequence.
		c.C("G STATUS INBOX (HIGHESTMODSEQ)")
		c.Sxe(`\* STATUS .*INBOX.* \(HIGHESTMODSEQ 5\)`)
		c.OK("G")
	})
}

// TestCondStoreRequiresEnable verifies that MODSEQ and CHANGEDSINCE are
// rejected when CONDSTORE is not enabled and that plain SELECT keeps IMAP4rev1
// behaviour.
func TestCondStoreRequiresEnable(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")
		c.C("A SELECT INBOX")
		c.OK("A")

		c.C("B UID FETCH 1 (UID MODSEQ)")
		c.Sx(`^B BAD MODSEQ fetch attribute requires CONDSTORE`)

		c.C("C UID FETCH 1 CHANGEDSINCE 0 (UID)")
		c.Sx(`^C BAD CHANGEDSINCE requires CONDSTORE`)

		c.C("D UID STORE 1 (UNCHANGEDSINCE 0) +FLAGS.SILENT (\\Seen)")
		c.Sx(`^D BAD UNCHANGEDSINCE requires CONDSTORE`)

		// Without enabling, SELECT must not advertise HIGHESTMODSEQ.
		c.C("E SELECT INBOX")
		lines := drainUntil(c, "E")
		requireLine(t, lines, "E OK [READ-WRITE] SELECT")
		requireNoLine(t, lines, "HIGHESTMODSEQ")
	})
}

// TestCondStoreUnchangedSince verifies the optimistic concurrency STORE:
// messages modified after the client watermark are reported via MODIFIED and
// left untouched.
func TestCondStoreUnchangedSince(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE CONDSTORE")
		c.S("* ENABLE CONDSTORE", "A OK ENABLE")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 2@pm.me")).expect("OK")

		c.C("B SELECT INBOX (CONDSTORE)")
		c.Sxe(`\* OK \[HIGHESTMODSEQ 2\]`)
		c.OK("B")

		// Bump UID 1 to mod-sequence 3.
		c.C("C UID STORE 1 +FLAGS.SILENT (\\Seen)")
		c.Sxe(`^C OK \[MODSEQ 3\]`)

		// Conditional store with watermark 2: UID 1 fails, UID 2 succeeds.
		c.C("D UID STORE 1:2 (UNCHANGEDSINCE 2) +FLAGS.SILENT (\\Deleted)")
		c.Sx(`^D NO \[MODIFIED 1\]`)

		// UID 1 must not have been deleted; UID 2 must be deleted (m4).
		c.C("E UID FETCH 1:2 (UID FLAGS)")
		c.Sx(
			`^\* 1 FETCH \(UID 1 FLAGS \([^)]*\\Seen[^)]*\)\)\r\n`,
			`^\* 2 FETCH \(UID 2 FLAGS \([^)]*\\Deleted[^)]*\)\)\r\n`,
		)
		c.OK("E")

		// A conditional store satisfied by all messages returns OK with the
		// new watermark.
		c.C("F UID STORE 2 (UNCHANGEDSINCE 4) -FLAGS.SILENT (\\Deleted)")
		c.Sxe(`^F OK \[MODSEQ 5\]`)

		c.C("G UID FETCH 2 (UID FLAGS)")
		c.Sx(`^\* 2 FETCH \(UID 2 FLAGS \([^)]*\)\)\r\n`)
		c.OK("G")
	})
}

// TestQResyncIncremental verifies the full QRESYNC flow: VANISHED (EARLIER),
// flag-change FETCH responses and deterministic watermarks.
func TestQResyncIncremental(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		c.C("B SELECT INBOX (CONDSTORE)")
		c.OK("B")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 2@pm.me")).expect("OK")
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 3@pm.me")).expect("OK")

		// Modify UID 2 (modseq 4) and UID 3 (modseq 5).
		c.C("D UID STORE 2 +FLAGS.SILENT (\\Seen)")
		c.Sxe(`D OK`)
		c.C("E UID STORE 3 +FLAGS.SILENT (\\Flagged)")
		c.Sxe(`E OK`)

		// Expunge UID 1: deleted flag (modseq 6) and removal records modseq 7.
		c.C("F UID STORE 1 +FLAGS.SILENT (\\Deleted)")
		c.Sxe(`F OK`)
		c.C("G EXPUNGE")
		// QRESYNC sessions receive VANISHED rather than EXPUNGE.
		c.S(`* VANISHED 1`)
		c.OK("G")

		// Re-select since mod-sequence 3 without a known UID set.
		c.C("H SELECT INBOX (QRESYNC (1 3))")
		lines := drainUntil(c, "H")
		requireLine(t, lines, "* OK [HIGHESTMODSEQ 7]")
		requireLine(t, lines, "* VANISHED (EARLIER) 1")
		requireLine(t, lines, "* 1 FETCH (UID 2 FLAGS (\\Seen) MODSEQ (4))")
		requireLine(t, lines, "* 2 FETCH (UID 3 FLAGS (\\Flagged) MODSEQ (5))")
		requireLine(t, lines, "H OK [READ-WRITE] SELECT")

		// Re-select since mod-sequence 5: UID 1 vanished after 5 but UID 3's
		// flags at modseq 5 are already known to the client.
		c.C("I SELECT INBOX (QRESYNC (1 5))")
		lines = drainUntil(c, "I")
		requireLine(t, lines, "* VANISHED (EARLIER) 1")
		requireNoLine(t, lines, " FETCH (UID")
		requireLine(t, lines, "I OK [READ-WRITE] SELECT")

		// Re-select with an explicit known UID set.
		c.C("J SELECT INBOX (QRESYNC (1 0 1:3))")
		lines = drainUntil(c, "J")
		requireLine(t, lines, "* VANISHED (EARLIER) 1")
		requireLine(t, lines, "* 1 FETCH (UID 2 FLAGS (\\Seen) MODSEQ (4))")
		requireLine(t, lines, "* 2 FETCH (UID 3 FLAGS (\\Flagged) MODSEQ (5))")
		requireLine(t, lines, "J OK [READ-WRITE] SELECT")

		// Re-select at the current watermark: no VANISHED and no FETCH deltas.
		c.C("K SELECT INBOX (QRESYNC (1 7))")
		lines = drainUntil(c, "K")
		requireLine(t, lines, "* OK [HIGHESTMODSEQ 7]")
		requireNoLine(t, lines, "VANISHED")
		requireNoLine(t, lines, " FETCH (UID")
		requireLine(t, lines, "K OK [READ-WRITE] SELECT")

		// A known UID set extending past UIDNEXT does not invent vanished UIDs.
		c.C("L SELECT INBOX (QRESYNC (1 0 1:4294967295))")
		lines = drainUntil(c, "L")
		requireLine(t, lines, "* VANISHED (EARLIER) 1")
		requireLine(t, lines, "* 1 FETCH (UID 2 FLAGS (\\Seen) MODSEQ (4))")
		requireLine(t, lines, "* 2 FETCH (UID 3 FLAGS (\\Flagged) MODSEQ (5))")
		requireNoLine(t, lines, "4294967295")
		requireLine(t, lines, "L OK [READ-WRITE] SELECT")
	})
}

// TestQResyncModSeqTooHigh verifies that an impossible client watermark is
// rejected with BAD and no selection takes effect.
func TestQResyncModSeqTooHigh(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")

		c.C("B SELECT INBOX (QRESYNC (1 42))")
		c.Sx(`^B BAD .*higher than the mailbox HIGHESTMODSEQ`)
	})
}

// TestCondStoreMoveBumpsBothMailboxes verifies a MOVE advances the
// mod-sequence of the source (removal history) and the destination (add).
func TestCondStoreMoveBumpsBothMailboxes(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		c.C("B CREATE Archive")
		c.OK("B")

		c.C("C SELECT INBOX (CONDSTORE)")
		c.OK("C")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")

		// Append -> modseq 1; move removes from INBOX (2) and adds to Archive (1).
		c.C("D MOVE 1 Archive")
		c.S(`* VANISHED 1`, `* OK [COPYUID 1 1 1]`)
		c.OK("D")

		c.C("E STATUS INBOX (HIGHESTMODSEQ)")
		c.Sxe(`\* STATUS .*INBOX.* \(HIGHESTMODSEQ 2\)`)
		c.OK("E")

		c.C("F STATUS Archive (HIGHESTMODSEQ)")
		c.Sxe(`\* STATUS .*Archive.* \(HIGHESTMODSEQ 1\)`)
		c.OK("F")

		// QRESYNC INBOX without a known UID set at watermark 1 finds the removal.
		c.C("G SELECT INBOX (QRESYNC (1 1))")
		lines := drainUntil(c, "G")
		requireLine(t, lines, "* OK [HIGHESTMODSEQ 2]")
		requireLine(t, lines, "* VANISHED (EARLIER) 1")
		requireLine(t, lines, "G OK [READ-WRITE] SELECT")
	})
}

// TestQResyncUIDValidityMismatch falls back to a full synchronization.
func TestQResyncUIDValidityMismatch(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")

		c.C("B SELECT INBOX (QRESYNC (999 0))")
		lines := drainUntil(c, "B")
		requireLine(t, lines, "* OK [UIDVALIDITY 1]")
		requireLine(t, lines, "* 1 EXISTS")
		requireLine(t, lines, "B OK [READ-WRITE] SELECT")
		requireNoLine(t, lines, "VANISHED")
	})
}

// TestQResyncHistoryPruned verifies that missing change history is reported
// instead of returning an incomplete incremental response, while an explicit
// known UID set always allows exact resynchronization.
func TestQResyncHistoryPruned(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		// Message creation grows the watermark but creates no vanished history.
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")

		// QRESYNC without known UIDs cannot be answered from retained history.
		c.C("B SELECT INBOX (QRESYNC (1 0))")
		c.Sx(`^B NO .*has been pruned`)

		// Providing the known UID set makes the answer exact and succeeds.
		c.C("C SELECT INBOX (QRESYNC (1 0 1))")
		lines := drainUntil(c, "C")
		requireLine(t, lines, "FETCH (UID 1 FLAGS")
		requireLine(t, lines, "MODSEQ (1)")
		requireLine(t, lines, "C OK [READ-WRITE] SELECT")
	})
}

// TestQResyncExamine verifies QRESYNC works with read-only EXAMINE.
func TestQResyncExamine(t *testing.T) {
	runOneToOneTestWithAuth(t, condStoreOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")

		c.C("B EXAMINE INBOX (QRESYNC (1 0 1))")
		lines := drainUntil(c, "B")
		requireLine(t, lines, "* OK [HIGHESTMODSEQ 1]")
		requireLine(t, lines, "MODSEQ (1)")
		requireLine(t, lines, "B OK [READ-ONLY] EXAMINE")
	})
}

// TestQResyncPersistentAcrossRestart verifies mod-sequences survive a process
// restart and never restart from zero.
func TestQResyncPersistentAcrossRestart(t *testing.T) {
	dataDir := t.TempDir()
	dbDir := t.TempDir()

	options := func() *serverOptions {
		return defaultServerOptions(t,
			withDataDir(dataDir),
			withDatabaseDir(dbDir),
			withUIDValidityGenerator(imap.NewFixedUIDValidityGenerator(imap.UID(1))),
			withConnectorBuilder(&slowDummyConnectorBuilder{}),
		)
	}

	runOneToOneTestWithAuth(t, options(), func(c *testConnection, s *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		c.C("B SELECT INBOX (CONDSTORE)")
		c.OK("B")

		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")
		c.doAppend("INBOX", buildRFC5322TestLiteral("To: 2@pm.me")).expect("OK")
		c.C("C UID STORE 1 +FLAGS.SILENT (\\Seen)")
		c.Sx(`C OK \[MODSEQ 3\]`)

		c.C("D UID FETCH 1 (UID MODSEQ)")
		c.S(`* 1 FETCH (UID 1 MODSEQ (3))`)
		c.OK("D")

		// Discard the dummy connector's queued notification updates so the
		// persisted state at shutdown reflects exactly the client-visible
		// operations above.
		s.conns[s.userIDs["user"]].(*connector.Dummy).ClearUpdates()
	})

	// Restart the server on the same on-disk state.
	runOneToOneTestWithAuth(t, options(), func(c *testConnection, _ *testSession) {
		c.C("A ENABLE QRESYNC")
		c.S("* ENABLE CONDSTORE QRESYNC", "A OK ENABLE")

		c.C("B STATUS INBOX (HIGHESTMODSEQ)")
		c.Sxe(`\* STATUS .*INBOX.* \(HIGHESTMODSEQ 3\)`)
		c.OK("B")

		c.C("C SELECT INBOX (QRESYNC (1 0 1:2))")
		lines := drainUntil(c, "C")
		requireLine(t, lines, "* 1 FETCH (UID 1 FLAGS (\\Seen) MODSEQ (3))")
		requireLine(t, lines, "C OK [READ-WRITE] SELECT")
	})
}
