package response

import (
	"fmt"
	"strconv"

	"github.com/ProtonMail/gluon/imap"
)

// nstring renders a persistent object identifier value per RFC 8474: a quoted string, or NIL when
// the server cannot provide the value.
func renderNString(value string) string {
	if len(value) == 0 {
		return "NIL"
	}

	return strconv.Quote(value)
}

type itemEmailID struct {
	id imap.EmailID
}

func ItemEmailID(id imap.EmailID) Item {
	return &itemEmailID{id: id}
}

func (c *itemEmailID) String() string {
	return fmt.Sprintf("EMAILID %v", renderNString(string(c.id)))
}

type itemThreadID struct {
	id imap.ThreadID
}

func ItemThreadID(id imap.ThreadID) Item {
	return &itemThreadID{id: id}
}

func (c *itemThreadID) String() string {
	return fmt.Sprintf("THREADID %v", renderNString(string(c.id)))
}

// itemMailboxIDStatus renders the MAILBOXID STATUS attribute, whose value is an nstring.
type itemMailboxIDStatus struct {
	id imap.MailboxObjectID
}

func ItemStatusMailboxID(id imap.MailboxObjectID) Item {
	return &itemMailboxIDStatus{id: id}
}

func (c *itemMailboxIDStatus) String() string {
	return fmt.Sprintf("MAILBOXID %v", renderNString(string(c.id)))
}

// itemMailboxIDCode renders the MAILBOXID response code of SELECT/EXAMINE, whose value is a bare
// objectid wrapped in parentheses.
type itemMailboxIDCode struct {
	id imap.MailboxObjectID
}

func ItemMailboxIDCode(id imap.MailboxObjectID) Item {
	return &itemMailboxIDCode{id: id}
}

func (c *itemMailboxIDCode) String() string {
	return fmt.Sprintf("MAILBOXID (%v)", c.id)
}
