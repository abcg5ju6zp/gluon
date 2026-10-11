package tests

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ProtonMail/gluon/imap"
	"github.com/stretchr/testify/require"
)

type fetchedIDs struct {
	seq    int
	uid    int
	email  string
	thread string
}

var (
	emailIDItemRE  = regexp.MustCompile(`EMAILID "([^"]*)"`)
	threadIDItemRE = regexp.MustCompile(`THREADID "([^"]*)"`)
	uidItemRE      = regexp.MustCompile(`(?:^| )UID (\d+)`)
	fetchLineRE    = regexp.MustCompile(`^\* (\d+) FETCH \((.*)\)\r\n$`)
)

// runFetch sends a FETCH command and parses the returned UID/EMAILID/THREADID items.
func runFetch(c *testConnection, tag, set string, attrs ...string) []fetchedIDs {
	c.Cf("%v FETCH %v (%v)", tag, set, strings.Join(attrs, " "))

	var result []fetchedIDs

	for {
		line := string(c.read())
		if strings.HasPrefix(line, tag+" ") {
			break
		}

		if m := fetchLineRE.FindStringSubmatch(line); m != nil {
			seq, err := strconv.Atoi(m[1])
			require.NoError(c.tb, err)

			entry := fetchedIDs{seq: seq}

			if m := uidItemRE.FindStringSubmatch(m[2]); m != nil {
				entry.uid, err = strconv.Atoi(m[1])
				require.NoError(c.tb, err)
			}

			if m := emailIDItemRE.FindStringSubmatch(m[2]); m != nil {
				entry.email = m[1]
			}

			if m := threadIDItemRE.FindStringSubmatch(m[2]); m != nil {
				entry.thread = m[1]
			}

			result = append(result, entry)
		}
	}

	return result
}

// selectMailbox drains the full untagged SELECT/EXAMINE response until the tagged OK.
func selectMailbox(c *testConnection, tag, mailbox string) {
	c.Cf("%v SELECT %v", tag, mailbox)
	c.Sxe(regexp.QuoteMeta(tag) + ` OK \[READ-WRITE\] SELECT`)
}

func literalWithMessageID(messageID string) string {
	return buildRFC5322TestLiteral("Message-ID: " + messageID + "\r\nTo: 1@pm.me\r\nSubject: test\r\n\r\nbody")
}

func literalReferences(messageID, references string) string {
	return buildRFC5322TestLiteral("Message-ID: " + messageID + "\r\nReferences: " + references +
		"\r\nTo: 1@pm.me\r\nSubject: test\r\n\r\nbody")
}

func TestRFC8474FetchEmailAndThreadID(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A001 CREATE Other")
		c.OK("A001")

		c.doAppend("INBOX", literalWithMessageID("<m1@example>")).expect("OK")
		c.doAppend("INBOX", literalWithMessageID("<m2@example>")).expect("OK")

		selectMailbox(c, "A002", "INBOX")

		fetched := runFetch(c, "A003", "1:*", "UID", "EMAILID", "THREADID")
		require.Len(t, fetched, 2)

		e1, e2 := fetched[0].email, fetched[1].email
		t1 := fetched[0].thread

		require.Regexp(t, `^[0-9a-f-]{36}$`, e1)
		require.Regexp(t, `^[0-9a-f-]{36}$`, e2)
		require.NotEqual(t, e1, e2)
		require.NotEmpty(t, t1)
		// Unrelated messages do not share a thread.
		require.NotEqual(t, fetched[0].thread, fetched[1].thread)

		// COPY keeps the same EMAILID and THREADID across mailboxes.
		c.C("A004 UID COPY 1 Other")
		c.Sx(`A004 OK \[COPYUID`)

		selectMailbox(c, "A005", "Other")

		copied := runFetch(c, "A006", "1", "UID", "EMAILID", "THREADID")
		require.Len(t, copied, 1)
		require.Equal(t, e1, copied[0].email)
		require.Equal(t, t1, copied[0].thread)
	})
}

func TestRFC8474MailboxID(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A001 CREATE M1")
		c.OK("A001")

		// STATUS exposes the persistent MAILBOXID as an nstring.
		c.C("A002 STATUS M1 (MAILBOXID)")
		line := string(c.read())
		m := regexp.MustCompile(`^\* STATUS "M1" \(MAILBOXID "([0-9a-f-]{36})"\)\r\n$`).FindStringSubmatch(line)
		require.NotNil(t, m, line)
		mailboxID := m[1]
		c.S("A002 OK STATUS")

		// Renaming keeps the identifier stable.
		c.C("A003 RENAME M1 M2")
		c.OK("A003")

		c.C("A004 STATUS M2 (MAILBOXID)")
		line = string(c.read())
		require.Contains(t, line, `MAILBOXID "`+mailboxID+`"`)
		c.S("A004 OK STATUS")

		// Every newly created mailbox gets its own fresh identifier which is never reused.
		c.C("A005 CREATE M3")
		c.OK("A005")

		c.C("A006 STATUS M3 (MAILBOXID)")
		line = string(c.read())
		m = regexp.MustCompile(`MAILBOXID "([0-9a-f-]{36})"`).FindStringSubmatch(line)
		require.NotNil(t, m, line)
		require.NotEqual(t, mailboxID, m[1])
		c.S("A006 OK STATUS")

		// SELECT exposes the same value as a MAILBOXID response code.
		var selectedID string

		c.C("A007 SELECT M2")
		for {
			l := string(c.read())
			if strings.HasPrefix(l, "A007 ") {
				break
			}

			if m := regexp.MustCompile(`MAILBOXID \(([0-9a-f-]+)\)`).FindStringSubmatch(l); m != nil {
				selectedID = m[1]
			}
		}

		require.Equal(t, mailboxID, selectedID)
	})
}

func TestRFC8474SearchKeysAndReturn(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t), func(c *testConnection, _ *testSession) {
		c.doAppend("INBOX", literalWithMessageID("<s1@example>")).expect("OK")
		c.doAppend("INBOX", literalWithMessageID("<s2@example>")).expect("OK")

		selectMailbox(c, "A001", "INBOX")

		fetched := runFetch(c, "A002", "1:*", "EMAILID", "THREADID")
		require.Len(t, fetched, 2)

		// SEARCH EMAILID.
		c.Cf("A003 SEARCH EMAILID %q", fetched[0].email)
		c.S(`* SEARCH 1`)
		c.Sx(`A003 OK`)

		// UID SEARCH THREADID.
		c.Cf("A004 UID SEARCH THREADID %q", fetched[1].thread)
		c.S(`* SEARCH 2`)
		c.Sx(`A004 OK`)

		// SEARCH RETURN (EMAILID THREADID) correlators.
		c.C("A005 SEARCH ALL RETURN (EMAILID THREADID)")
		c.S(`* SEARCH 1 2`)
		c.S(`* EMAILID (1 "` + fetched[0].email + `")`)
		c.S(`* THREADID (1 "` + fetched[0].thread + `")`)
		c.S(`* EMAILID (2 "` + fetched[1].email + `")`)
		c.S(`* THREADID (2 "` + fetched[1].thread + `")`)
		c.Sx(`A005 OK`)

		// Unknown object ids don't match.
		c.C(`A006 SEARCH EMAILID "does-not-exist"`)
		c.S(`* SEARCH`)
		c.Sx(`A006 OK`)
	})
}

func TestRFC8474ThreadMergeAndSplit(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t), func(c *testConnection, s *testSession) {
		mbox := s.mailboxCreated("user", []string{"ThreadBox"})

		// a <- b(bridge) <- c: all three end up in one thread.
		idBRemote := s.messageCreatedWithMailboxes("user", []imap.MailboxID{mbox},
			[]byte(literalWithMessageID("<a@thread>")), time.Now())
		idB := s.messageCreatedWithMailboxes("user", []imap.MailboxID{mbox},
			[]byte(literalReferences("<b@thread>", "<a@thread>")), time.Now())
		s.messageCreatedWithMailboxes("user", []imap.MailboxID{mbox},
			[]byte(literalReferences("<c@thread>", "<b@thread>")), time.Now())

		selectMailbox(c, "A001", "ThreadBox")

		fetched := runFetch(c, "A002", "1:*", "EMAILID", "THREADID")
		require.Len(t, fetched, 3)
		require.Equal(t, fetched[0].thread, fetched[1].thread)
		require.Equal(t, fetched[1].thread, fetched[2].thread)

		threadRoot := fetched[0].thread
		emailB := fetched[1].email

		// Threading is consistent across mailboxes: a message referencing the root from
		// another mailbox joins the same thread.
		mbox2 := s.mailboxCreated("user", []string{"OtherThreadBox"})
		s.messageCreatedWithMailboxes("user", []imap.MailboxID{mbox2},
			[]byte(literalReferences("<d@thread>", "<a@thread>")), time.Now())

		selectMailbox(c, "A003", "OtherThreadBox")

		other := runFetch(c, "A004", "1", "THREADID")
		require.Len(t, other, 1)
		require.Equal(t, threadRoot, other[0].thread)

		// Removing the bridge message splits the thread: a and c are no longer connected.
		s.messageDeleted("user", idB)

		selectMailbox(c, "A005", "ThreadBox")

		after := runFetch(c, "A006", "1:*", "EMAILID", "THREADID")
		require.Len(t, after, 2)
		require.NotEqual(t, after[0].thread, after[1].thread, "threads must not be mixed after split")

		// The bridge's EMAILID is gone from the visible mailbox.
		for _, entry := range after {
			require.NotEqual(t, emailB, entry.email)
		}

		// Keep the test honest about the two remote identities.
		require.NotEmpty(t, idBRemote)
		require.NotEmpty(t, idB)
	})
}

func TestRFC8474ConnectorUpdatePreservesEmailID(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t), func(c *testConnection, s *testSession) {
		mbox := s.mailboxCreated("user", []string{"UpdateBox"})
		messageID := s.messageCreated("user", mbox,
			[]byte(buildRFC5322TestLiteral("Message-ID: <upd@thread>\r\nTo: 1@pm.me\r\nSubject: old\r\n\r\nold body")),
			time.Now())

		selectMailbox(c, "A001", "UpdateBox")

		before := runFetch(c, "A002", "1", "EMAILID", "THREADID")
		require.Len(t, before, 1)

		// Connector pushes a structurally different literal for the same remote message.
		s.messageUpdatedWithID("user", messageID, mbox,
			[]byte(buildRFC5322TestLiteral("Message-ID: <upd@thread>\r\nTo: 1@pm.me\r\nSubject: new\r\n\r\nnew body which is different")),
			time.Now())

		selectMailbox(c, "A003", "UpdateBox")

		after := runFetch(c, "A004", "1", "EMAILID", "THREADID")
		require.Len(t, after, 1)
		require.Equal(t, before[0].email, after[0].email, "EMAILID must survive connector updates")
		require.Equal(t, before[0].thread, after[0].thread, "THREADID must survive connector updates")
	})
}
