package db

import (
	"context"
	"time"

	"github.com/ProtonMail/gluon/imap"
)

// AppendJournalStatus is the state of an APPEND upload recorded in the append journal.
type AppendJournalStatus int

const (
	// AppendJournalReceiving: literal is (or was) being streamed into a spool object.
	AppendJournalReceiving AppendJournalStatus = 1
	// AppendJournalSubmitting: full literal received and validated; connector is being called.
	AppendJournalSubmitting AppendJournalStatus = 2
	// AppendJournalAccepted: connector accepted the message; local store/DB commit pending.
	AppendJournalAccepted AppendJournalStatus = 3
	// AppendJournalConfirmed: local DB commit succeeded and a UID was assigned.
	AppendJournalConfirmed AppendJournalStatus = 4
)

// AppendJournalEntry is a persisted APPEND upload record.
type AppendJournalEntry struct {
	AttemptID   string
	IdentityHash string
	ContentHash string

	MailboxInternalID imap.InternalMailboxID
	RemoteID          imap.MessageID
	InternalID        imap.InternalMessageID
	OriginState       int64
	UID               imap.UID

	Size  int64
	Flags []string
	Date  time.Time

	Status AppendJournalStatus

	CreatedAt time.Time
	UpdatedAt time.Time
}

type AppendJournalReadOps interface {
	GetAppendJournal(ctx context.Context, attemptID string) (*AppendJournalEntry, error)
	GetAppendJournalByIdentity(ctx context.Context, identityHash string) (*AppendJournalEntry, error)
	GetAllAppendJournal(ctx context.Context) ([]*AppendJournalEntry, error)
}

type AppendJournalWriteOps interface {
	AppendJournalReadOps

	InsertAppendJournalReceiving(ctx context.Context, attemptID string, size int64, now time.Time) error

	SetAppendJournalSubmitting(
		ctx context.Context,
		attemptID string,
		identityHash string,
		contentHash string,
		mailboxID imap.InternalMailboxID,
		originState int64,
		flags []string,
		date time.Time,
		now time.Time,
	) error

	SetAppendJournalAccepted(
		ctx context.Context,
		attemptID string,
		remoteID imap.MessageID,
		internalID imap.InternalMessageID,
		now time.Time,
	) error

	SetAppendJournalConfirmed(
		ctx context.Context,
		attemptID string,
		mailboxID imap.InternalMailboxID,
		uid imap.UID,
		now time.Time,
	) error

	DeleteAppendJournal(ctx context.Context, attemptID string) error

	// DeleteConfirmedAppendJournalBefore deletes confirmed rows older than before and returns
	// the number of deleted rows.
	DeleteConfirmedAppendJournalBefore(ctx context.Context, before time.Time) (int64, error)
}
