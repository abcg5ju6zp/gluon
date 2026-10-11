package state

import (
	"context"
	"io"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/utils"
	"github.com/ProtonMail/gluon/store"
)

// UserInterface represents the expected behaviour for interacting with a remote user.
// Sadly, due to Go's cyclic dependencies, this needs to be an interface. The implementation of this interface
// is available in the backend package.
type UserInterface interface {
	GetUserID() string

	GetDelimiter() string

	GetDB() db.Client

	GetRemote() Connector

	GetStore() *store.WriteControlledStore

	QueueOrApplyStateUpdate(ctx context.Context, tx db.Transaction, update ...Update) error

	ReleaseState(ctx context.Context, st *State) error

	GetRecoveryMailboxID() db.MailboxIDPair

	GenerateUIDValidity() (imap.UID, error)

	GetRecoveredMessageHashesMap() *utils.MessageHashesMap

	// BeginAppendUpload reserves quota, creates the reception spool object and records the
	// receiving journal entry for a streaming APPEND, without calling the connector.
	BeginAppendUpload(ctx context.Context, declaredSize int64) (Upload, error)

	// OpenSpool opens a previously received spool object by attempt ID (crash/reply recovery).
	OpenSpool(attemptID string) (io.ReadSeekCloser, error)

	// RemoveSpool removes a spool object by attempt ID.
	RemoveSpool(attemptID string) error
}
