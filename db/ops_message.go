package db

import (
	"context"
	"time"

	"github.com/ProtonMail/gluon/imap"
)

type MessageReadOps interface {
	MessageExists(ctx context.Context, id imap.InternalMessageID) (bool, error)

	MessageExistsWithRemoteID(ctx context.Context, id imap.MessageID) (bool, error)

	GetMessageNoEdges(ctx context.Context, id imap.InternalMessageID) (*Message, error)

	GetTotalMessageCount(ctx context.Context) (int, error)

	GetMessageRemoteID(ctx context.Context, id imap.InternalMessageID) (imap.MessageID, error)

	GetImportedMessageData(ctx context.Context, id imap.InternalMessageID) (*MessageWithFlags, error)

	GetMessageDateAndSize(ctx context.Context, id imap.InternalMessageID) (time.Time, int, error)

	GetMessageMailboxIDs(ctx context.Context, id imap.InternalMessageID) ([]imap.InternalMailboxID, error)

	GetMessagesFlags(ctx context.Context, ids []imap.InternalMessageID) ([]MessageFlagSet, error)

	GetMessageIDsMarkedAsDelete(ctx context.Context) ([]imap.InternalMessageID, error)

	GetMessageIDFromRemoteID(ctx context.Context, id imap.MessageID) (imap.InternalMessageID, error)

	GetMessageDeletedFlag(ctx context.Context, id imap.InternalMessageID) (bool, error)

	GetAllMessagesIDsAsMap(ctx context.Context) (map[imap.InternalMessageID]struct{}, error)
}

type MessageWriteOps interface {
	MessageReadOps

	CreateMessages(ctx context.Context, reqs ...*CreateMessageReq) error

	CreateMessageAndAddToMailbox(ctx context.Context, mbox imap.InternalMailboxID, req *CreateMessageReq) (imap.UID, imap.FlagSet, error)

	// MarkMessageAsDeleted tombstones the message, removes its threading tokens and returns the
	// other live messages that were connected through those tokens so thread membership can be
	// (re)computed.
	MarkMessageAsDeleted(ctx context.Context, id imap.InternalMessageID) ([]imap.InternalMessageID, error)

	// MarkMessageAsDeletedWithRemoteID is MarkMessageAsDeleted keyed by remote ID.
	MarkMessageAsDeletedWithRemoteID(ctx context.Context, id imap.MessageID) ([]imap.InternalMessageID, error)

	// ReplaceMessageLiteral tombstones the old message row (releasing its remote ID and object
	// id constraints) and inserts a fresh row carrying the SAME persistent EMAILID and THREADID
	// as the old one. Threading tokens are replaced from req.Body and thread membership is
	// recomputed over the old and new token neighborhoods; the resulting assignments are
	// returned. All changes happen inside the caller's transaction.
	ReplaceMessageLiteral(ctx context.Context, oldInternalID imap.InternalMessageID, req *CreateMessageReq) ([]ThreadAssignment, error)

	// RecomputeMessageThreads recomputes thread membership for the components reachable from the
	// given seed messages through shared Message-ID tokens and returns the resulting assignments.
	RecomputeMessageThreads(ctx context.Context, seed []imap.InternalMessageID) ([]ThreadAssignment, error)

	DeleteMessages(ctx context.Context, ids []imap.InternalMessageID) error

	UpdateRemoteMessageID(ctx context.Context, internalID imap.InternalMessageID, remoteID imap.MessageID) error

	AddFlagToMessages(ctx context.Context, ids []imap.InternalMessageID, flag string) error

	RemoveFlagFromMessages(ctx context.Context, ids []imap.InternalMessageID, flag string) error

	SetFlagsOnMessages(ctx context.Context, ids []imap.InternalMessageID, flags imap.FlagSet) error
}

type CreateMessageReq struct {
	Message     imap.Message
	InternalID  imap.InternalMessageID
	LiteralSize int
	Body        string
	Structure   string
	Envelope    string
	// ObjectID is the persistent EMAILID for the new message. When empty, the database assigns a
	// fresh random object id. It is only set explicitly when a message row is recreated while the
	// logical message identity must be preserved (e.g. a connector-driven literal update).
	ObjectID imap.EmailID
	// ThreadTokens are the Message-ID tokens used to derive the persistent THREADID. They are
	// computed from the message literal by the caller and stored alongside the message.
	ThreadTokens []string
}

type MessageFlagSet struct {
	ID       imap.InternalMessageID
	RemoteID imap.MessageID
	FlagSet  imap.FlagSet
}
