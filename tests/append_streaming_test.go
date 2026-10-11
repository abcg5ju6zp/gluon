package tests

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ProtonMail/gluon/connector"
	"github.com/ProtonMail/gluon/imap"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func itoa(v int) string {
	return strconv.Itoa(v)
}

type streamingDummyConnectorBuilder struct {
	contentAddressed bool
	quotaBytes        int64
	mu                sync.Mutex
	instances         []*connector.StreamingDummy
}

func (b *streamingDummyConnectorBuilder) New(
	usernames []string, password []byte, period time.Duration, flags, permFlags, attrs imap.FlagSet,
) Connector {
	d := connector.NewDummy(usernames, password, period, flags, permFlags, attrs)

	if b.contentAddressed {
		d.SetContentAddressedAppends(true)
	}

	if b.quotaBytes > 0 {
		d.SetQuotaBytes(b.quotaBytes)
	}

	wrapped := connector.NewStreamingDummy(d)

	b.mu.Lock()
	b.instances = append(b.instances, wrapped)
	b.mu.Unlock()

	return wrapped
}

func (b *streamingDummyConnectorBuilder) createCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	var total int64
	for _, instance := range b.instances {
		total += instance.GetAppendCreateCount()
	}

	return total
}

func makeTestLiteral(size int) string {
	return buildRFC5322TestLiteral("Subject: large\r\n\r\n" + strings.Repeat("a", size))
}

func TestStreamingAppend_LargeMessageSpillsAndRoundTrips(t *testing.T) {
	builder := &streamingDummyConnectorBuilder{}
	options := defaultServerOptions(t,
		withConnectorBuilder(builder),
		withAppendSpoolMemoryLimit(4096),
	)

	runOneToOneTestWithAuth(t, options, func(c *testConnection, _ *testSession) {
		literal := makeTestLiteral(200 * 1024)

		tag := rawAppendPlus(c, "INBOX", literal)
		c.Sx(regexp.QuoteMeta(tag) + " OK")

		c.C("S1 STATUS INBOX (MESSAGES)")
		c.Sx(`MESSAGES 1`)
		c.OK("S1")

		c.C("S2 SELECT INBOX").OK("S2")
		c.C(`S3 UID FETCH 1 (BODY[])`)
		c.Sx(regexp.QuoteMeta(literal))
		c.OK("S3")
	})
}

func TestStreamingAppend_QuotaRejection(t *testing.T) {
	literal := buildRFC5322TestLiteral("Subject: q\r\n\r\nquota body")

	builder := &streamingDummyConnectorBuilder{quotaBytes: int64(len(literal))}
	options := defaultServerOptions(t,
		withConnectorBuilder(builder),
		withAppendSpoolMemoryLimit(16),
	)

	runOneToOneTestWithAuth(t, options, func(c *testConnection, _ *testSession) {
		tag1 := rawAppendPlus(c, "INBOX", literal)
		c.Sx(regexp.QuoteMeta(tag1) + " OK")

		c.C("Q2 APPEND INBOX {" + itoa(len(literal)) + "+}")
		c.C(literal)
		c.Sx(`Q2 NO`)

		c.C("Q3 STATUS INBOX (MESSAGES)")
		c.Sx(`MESSAGES 1`)
		c.OK("Q3")

		requireZeroConnectorCreates(t, builder, 1)
	})
}

func TestStreamingAppend_ConcurrentReservationsAdmitExactlyOne(t *testing.T) {
	literal := makeTestLiteral(64 * 1024)

	builder := &streamingDummyConnectorBuilder{quotaBytes: int64(len(literal))}
	options := defaultServerOptions(t,
		withConnectorBuilder(builder),
		withAppendSpoolMemoryLimit(4096),
		withDisableParallelism(),
	)

	runManyToOneTestWithAuth(t, options, []int{1, 2}, func(conn map[int]*testConnection, _ *testSession) {
		var wg sync.WaitGroup
		results := make(chan string, 2)

		for id := range conn {
			wg.Add(1)

			go func(id int) {
				defer wg.Done()

				tag := "C" + itoa(id)
				conn[id].Cf(`%v APPEND INBOX {%v+}`, tag, len(literal))
				conn[id].C(literal)

				line := string(conn[id].read())
				if strings.Contains(line, tag+" OK") {
					results <- "OK"
				} else {
					results <- "NO"
				}
			}(id)
		}

		wg.Wait()
		close(results)

		okCount, noCount := 0, 0
		for result := range results {
			if result == "OK" {
				okCount++
			} else {
				noCount++
			}
		}

		if okCount != 1 || noCount != 1 {
			t.Fatalf("expected exactly one accepted and one rejected append, got ok=%d no=%d", okCount, noCount)
		}
	})
}

func TestStreamingAppend_DisconnectMidBodyLeavesNoMessage(t *testing.T) {
	options := defaultServerOptions(t, withAppendSpoolMemoryLimit(16))

	runOneToOneTestWithAuth(t, options, func(_ *testConnection, s *testSession) {
		// Dedicated connection which we abandon while the body is incomplete.
		aborted := s.newConnection().Login("user", s.options.password("user"))

		raw := []byte("D1 APPEND INBOX {100+}\r\npartial")
		_, err := aborted.conn.Write(raw)
		require.NoError(t, err)
		require.NoError(t, aborted.conn.Close())

		// Give the server time to observe the disconnect and abort the reception.
		time.Sleep(300 * time.Millisecond)

		s.withConnection("user", func(cc *testConnection) {
			cc.C("D2 STATUS INBOX (MESSAGES)")
			cc.Sx(`MESSAGES 0`)
			cc.OK("D2")

			// A subsequent append still works.
			literal := buildRFC5322TestLiteral("Subject: after\r\n\r\nok")
			tag := rawAppendPlus(cc, "INBOX", literal)
			cc.Sx(regexp.QuoteMeta(tag) + " OK")
		})

		// No temporary object may survive.
		root := filepath.Join(s.options.dataDir, "..", "appends")
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(path, ".tmp") {
				t.Fatalf("spool object left behind: %v", path)
			}
			return nil
		})
	})
}

func TestStreamingAppend_RetryAfterLostResponseReturnsSameUID(t *testing.T) {
	builder := &streamingDummyConnectorBuilder{contentAddressed: true}
	options := defaultServerOptions(t,
		withConnectorBuilder(builder),
		withAppendSpoolMemoryLimit(16),
	)

	runOneToOneTestWithAuth(t, options, func(_ *testConnection, s *testSession) {
		literal := buildRFC5322TestLiteral("Subject: idem\r\n\r\nidem body")

		// First append on its own connection.
		first := s.newConnection().Login("user", s.options.password("user"))
		tag := rawAppendPlus(first, "INBOX", literal)
		first.Sx(regexp.QuoteMeta(tag) + ` OK \[APPENDUID \d+ 1\]`)
		require.NoError(t, first.disconnect())

		// Simulate a response that never reached the client: the spool object is normally
		// deleted only once the tagged response was written. Recreate it next to the
		// confirmed journal row.
		userID := s.userIDs["user"]
		dbPath := s.userDBPaths[userID]
		spoolDir := filepath.Join(s.options.dataDir, "appends", userID)
		attemptID := latestJournalAttempt(t, dbPath)
		require.NotEmpty(t, attemptID)
		require.NoError(t, os.WriteFile(filepath.Join(spoolDir, attemptID+".tmp"), []byte(literal), 0o600))

		// Retry the identical APPEND on a new connection: the existing commit is returned.
		retryConn := s.newConnection().Login("user", s.options.password("user"))
		retryTag := rawAppendPlus(retryConn, "INBOX", literal)
		retryConn.Sx(regexp.QuoteMeta(retryTag) + ` OK \[APPENDUID \d+ 1\]`)

		retryConn.C("I2 STATUS INBOX (MESSAGES UIDNEXT)")
		retryConn.Sx(`MESSAGES 1 UIDNEXT 2`)
		retryConn.OK("I2")
		require.NoError(t, retryConn.disconnect())

		requireZeroConnectorCreates(t, builder, 1)
	})
}

func latestJournalAttempt(t *testing.T, dbPath string) string {
	t.Helper()

	conn, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer conn.Close()

	var attemptID string
	err = conn.QueryRow(
		`SELECT attempt_id FROM append_journal_v1 WHERE status = 4 ORDER BY updated_at DESC LIMIT 1`,
	).Scan(&attemptID)
	require.NoError(t, err)

	return attemptID
}

func TestStreamingAppend_ReadTimeoutClosesStalledConnection(t *testing.T) {
	options := defaultServerOptions(t,
		withAppendSpoolMemoryLimit(16),
		withAppendReadTimeout(time.Second),
	)

	runOneToOneTestWithAuth(t, options, func(_ *testConnection, s *testSession) {
		stalled := s.newConnection().Login("user", s.options.password("user"))
		_, err := stalled.conn.Write([]byte("T1 APPEND INBOX {100+}\r\n"))
		require.NoError(t, err)

		// The server must not wait forever for the remaining body.
		time.Sleep(2 * time.Second)

		_, err = stalled.conn.Write([]byte("\r\n"))
		// Either the connection is already closed or the server stops accepting commands.
		if err == nil {
			_ = stalled.conn.SetReadDeadline(time.Now().Add(time.Second))
			buf := make([]byte, 16)
			_, readErr := stalled.conn.Read(buf)
			require.Error(t, readErr, "stalled connection should have been closed")
		}
	})
}

func requireZeroConnectorCreates(t *testing.T, builder *streamingDummyConnectorBuilder, want int64) {
	t.Helper()
	if got := builder.createCount(); got != want {
		t.Fatalf("expected %d remote message creates, got %d", want, got)
	}
}
