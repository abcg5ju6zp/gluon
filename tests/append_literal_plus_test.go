package tests

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// rawAppendPlus performs a non-synchronizing RFC 7888 APPEND: the header ends with "{n+}" and
// the body is sent immediately without waiting for a continuation.
func rawAppendPlus(c *testConnection, mailbox, literal string, flags ...string) string {
	tag := "T1"

	flagList := ""
	if len(flags) > 0 {
		flagList = " (" + strings.Join(flags, " ") + ")"
	}

	c.Cf(`%v APPEND %v%v {%v+}`, tag, mailbox, flagList, len(literal))
	// The body (plus its command terminator) can follow immediately.
	c.C(literal)

	return tag
}

func TestLiteralPlus_CapabilityAdvertised(t *testing.T) {
	runOneToOneTest(t, defaultServerOptions(t), func(c *testConnection, _ *testSession) {
		c.C("A001 CAPABILITY")
		c.Sx(`\* CAPABILITY .*LITERAL\+`)
		c.OK("A001")
	})
}

func TestLiteralPlus_NonSyncAppendWithoutContinuation(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t, withAppendSpoolMemoryLimit(64)), func(c *testConnection, _ *testSession) {
		literal := buildRFC5322TestLiteral("Subject: literal plus\r\n\r\nnon sync body")

		tag := rawAppendPlus(c, "INBOX", literal, `\Seen`)

		// No "+ Ready" continuation must be requested; the tagged completion arrives.
		c.Sx(regexp.QuoteMeta(tag) + ` OK`)

		c.C("A002 STATUS INBOX (MESSAGES)")
		c.Sx(`\* STATUS "?INBOX"? \(MESSAGES 1\)`)
		c.OK("A002")

		c.C("A003 SELECT INBOX").OK("A003")
		c.C(`A004 UID FETCH 1 (BODY[])`)
		c.Sx(regexp.QuoteMeta(literal))
		c.OK("A004")
	})
}

func TestLiteralPlus_SynchronousAppendStillWaitsForContinuation(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t, withAppendSpoolMemoryLimit(64)), func(c *testConnection, _ *testSession) {
		literal := buildRFC5322TestLiteral("Subject: sync\r\n\r\nsync body")

		c.C(`S1 APPEND INBOX {` + strconv.Itoa(len(literal)) + `}`)
		c.Continue()
		c.C(literal)
		c.Sx(`S1 OK`)
	})
}

func TestLiteralPlus_PipelinedSecondCommand(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t, withAppendSpoolMemoryLimit(16)), func(c *testConnection, _ *testSession) {
		literal := buildRFC5322TestLiteral("Subject: pipeline\r\n\r\npipelined body content")

		// Header, body and a second command written back-to-back; the parser must not over-read.
		c.Cf(`P1 APPEND INBOX {%v+}`, len(literal))
		c.C(literal)
		c.C("P2 NOOP")
		c.Sx(`P1 OK`)
		c.OK("P2")
	})
}

func TestLiteralPlus_RejectedOverLimitStaysConnected(t *testing.T) {
	runOneToOneTestWithAuth(t, defaultServerOptions(t), func(c *testConnection, _ *testSession) {
		c.C("R1 APPEND INBOX {73400320+}")
		c.Sx("R1 NO")

		// Connection stays usable.
		c.C("R2 NOOP")
		c.OK("R2")
	})
}
