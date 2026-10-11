package imap

// EmailID is the server-assigned persistent object identifier of a message (RFC 8474 EMAILID).
// It is stable across sessions, mailbox COPY/MOVE operations and connector-driven updates of the
// same remote message. It must not be confused with MessageID, which is the connector-assigned
// remote ID and can change.
type EmailID string

func (e EmailID) String() string {
	return string(e)
}

// MailboxObjectID is the server-assigned persistent object identifier of a mailbox
// (RFC 8474 MAILBOXID). It survives renames and restarts and is never reused when a mailbox with
// the same name is deleted and recreated.
type MailboxObjectID string

func (m MailboxObjectID) String() string {
	return string(m)
}

// ThreadID is the server-assigned persistent object identifier of a conversation thread
// (RFC 8474 THREADID). Messages belonging to the same thread share the same ThreadID across all
// mailboxes.
type ThreadID string

func (t ThreadID) String() string {
	return string(t)
}
