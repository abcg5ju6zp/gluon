package tests

import (
	"sync"
	"testing"

	"github.com/ProtonMail/gluon/imap"
)

// TestCondStoreConcurrentStores verifies that concurrent flag updates from two
// sessions are serialized into one shared, deterministic mod-sequence order.
func TestCondStoreConcurrentStores(t *testing.T) {
	runManyToOneTestWithAuth(t, defaultServerOptions(t,
		withUIDValidityGenerator(imap.NewFixedUIDValidityGenerator(imap.UID(1))),
		withConnectorBuilder(&slowDummyConnectorBuilder{}),
	), []int{1, 2}, func(conns map[int]*testConnection, _ *testSession) {
		conns[1].C("A1 ENABLE CONDSTORE")
		conns[1].S("* ENABLE CONDSTORE", "A1 OK ENABLE")
		conns[2].C("A2 ENABLE CONDSTORE")
		conns[2].S("* ENABLE CONDSTORE", "A2 OK ENABLE")

		conns[1].C("B1 SELECT INBOX (CONDSTORE)")
		conns[1].OK("B1")
		conns[2].C("B2 SELECT INBOX (CONDSTORE)")
		conns[2].OK("B2")

		for i := 0; i < 10; i++ {
			conns[1].doAppend("INBOX", buildRFC5322TestLiteral("To: x@pm.me")).expect("OK")
		}

		// Let the second session learn about the 10 messages.
		conns[2].C("N2 NOOP")
		conns[2].OK("N2")

		var wg sync.WaitGroup

		wg.Add(1)

		go func() {
			defer wg.Done()
			// 5 disjoint messages each get one flag: exactly 10 changes total.
			conns[1].C("C1 UID STORE 1:5 +FLAGS.SILENT (\\Flagged)")
			conns[1].Sxe(`C1 OK \[MODSEQ \d+\]`)
		}()

		conns[2].C("C2 UID STORE 6:10 +FLAGS.SILENT (\\Seen)")
		conns[2].Sxe(`C2 OK \[MODSEQ \d+\]`)

		wg.Wait()

		// Flush cross-session FETCH responders queued on both connections.
		conns[1].C("F1 NOOP")
		conns[1].Sxe(`F1 OK`)
		conns[2].C("F2 NOOP")
		conns[2].Sxe(`F2 OK`)

		// Two STORE commands -> two additional watermarks above the 10
		// creates, regardless of interleaving.
		conns[1].C("D1 STATUS INBOX (HIGHESTMODSEQ)")
		conns[1].Sxe(`\* STATUS .*INBOX.* \(HIGHESTMODSEQ 12\)`)
		conns[1].OK("D1")

		// Each command shares a single mod-sequence across its 5 messages.
		conns[1].C("E1 UID FETCH 1:10 (UID FLAGS MODSEQ)")
		conns[1].Sx(
			`^\* 1 FETCH \(UID 1 FLAGS \([^)]*\\Flagged[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 2 FETCH \(UID 2 FLAGS \([^)]*\\Flagged[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 3 FETCH \(UID 3 FLAGS \([^)]*\\Flagged[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 4 FETCH \(UID 4 FLAGS \([^)]*\\Flagged[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 5 FETCH \(UID 5 FLAGS \([^)]*\\Flagged[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 6 FETCH \(UID 6 FLAGS \([^)]*\\Seen[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 7 FETCH \(UID 7 FLAGS \([^)]*\\Seen[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 8 FETCH \(UID 8 FLAGS \([^)]*\\Seen[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 9 FETCH \(UID 9 FLAGS \([^)]*\\Seen[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
			`^\* 10 FETCH \(UID 10 FLAGS \([^)]*\\Seen[^)]*\) MODSEQ \(1[12]\)\)\r\n`,
		)
		conns[1].OK("E1")
	})
}

// TestQResyncOtherSessionVanished verifies that a QRESYNC-enabled session
// receives VANISHED (not EXPUNGE) when another session expunges a message.
func TestQResyncOtherSessionVanished(t *testing.T) {
	runManyToOneTestWithAuth(t, defaultServerOptions(t,
		withUIDValidityGenerator(imap.NewFixedUIDValidityGenerator(imap.UID(1))),
		withConnectorBuilder(&slowDummyConnectorBuilder{}),
	), []int{1, 2}, func(conns map[int]*testConnection, _ *testSession) {
		conns[2].C("A2 ENABLE QRESYNC")
		conns[2].S("* ENABLE CONDSTORE QRESYNC", "A2 OK ENABLE")

		conns[1].doAppend("INBOX", buildRFC5322TestLiteral("To: 1@pm.me")).expect("OK")
		conns[1].doAppend("INBOX", buildRFC5322TestLiteral("To: 2@pm.me")).expect("OK")

		conns[2].C("B2 SELECT INBOX")
		conns[2].OK("B2")

		conns[1].C("B1 SELECT INBOX")
		conns[1].OK("B1")

		conns[1].C("C1 UID STORE 1 +FLAGS.SILENT (\\Deleted)")
		conns[1].Sxe(`C1 OK`)
		conns[1].C("D1 EXPUNGE")
		conns[1].S(`* 1 EXPUNGE`)
		conns[1].OK("D1")

		// The QRESYNC session learns about the removal as VANISHED (along with
		// the preceding \Deleted flag update).
		conns[2].C("N2 NOOP")
		conns[2].Sx(
			`^\* 1 FETCH \(FLAGS \([^)]*\\Deleted[^)]*\) MODSEQ \(3\)\)\r\n`,
			`^\* VANISHED 1\r\n`,
		)
		conns[2].OK("N2")

		// A QRESYNC re-selection of session 2 against the current state reports
		// the vanished UID as EARLIER.
		conns[2].C("E2 SELECT INBOX (QRESYNC (1 0 1:2))")
		lines := drainUntil(conns[2], "E2")
		requireLine(t, lines, "* VANISHED (EARLIER) 1")
		requireLine(t, lines, "E2 OK [READ-WRITE] SELECT")
	})
}
