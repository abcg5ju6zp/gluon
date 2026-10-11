package db

import (
	"context"
	"strings"

	"github.com/ProtonMail/gluon/imap"
)

type MailboxNameAndRemoteID struct {
	Name     string
	RemoteID imap.MailboxID
}

type MailboxReadOps interface {
	MailboxExistsWithID(ctx context.Context, mboxID imap.InternalMailboxID) (bool, error)

	MailboxExistsWithRemoteID(ctx context.Context, mboxID imap.MailboxID) (bool, error)

	MailboxExistsWithName(ctx context.Context, name string) (bool, error)

	GetMailboxIDFromRemoteID(ctx context.Context, mboxID imap.MailboxID) (imap.InternalMailboxID, error)

	GetMailboxName(ctx context.Context, mboxID imap.InternalMailboxID) (string, error)

	GetMailboxNameWithRemoteID(ctx context.Context, mboxID imap.MailboxID) (string, error)

	GetMailboxMessageIDPairs(ctx context.Context, mboxID imap.InternalMailboxID) ([]MessageIDPair, error)

	GetAllMailboxesWithAttr(ctx context.Context) ([]*MailboxWithAttr, error)

	GetAllMailboxesAsRemoteIDs(ctx context.Context) ([]imap.MailboxID, error)

	GetMailboxByName(ctx context.Context, name string) (*Mailbox, error)

	GetMailboxByID(ctx context.Context, mboxID imap.InternalMailboxID) (*Mailbox, error)

	GetMailboxByRemoteID(ctx context.Context, mboxID imap.MailboxID) (*Mailbox, error)

	GetMailboxRecentCount(ctx context.Context, mboxID imap.InternalMailboxID) (int, error)

	GetMailboxMessageCount(ctx context.Context, mboxID imap.InternalMailboxID) (int, error)

	GetMailboxMessageCountWithRemoteID(ctx context.Context, mboxID imap.MailboxID) (int, error)

	GetMailboxFlags(ctx context.Context, mboxID imap.InternalMailboxID) (imap.FlagSet, error)

	GetMailboxPermanentFlags(ctx context.Context, mboxID imap.InternalMailboxID) (imap.FlagSet, error)

	GetMailboxAttributes(ctx context.Context, mboxID imap.InternalMailboxID) (imap.FlagSet, error)

	GetMailboxUID(ctx context.Context, mboxID imap.InternalMailboxID) (imap.UID, error)

	GetMailboxMessageCountAndUID(ctx context.Context, mboxID imap.InternalMailboxID) (int, imap.UID, error)

	GetMailboxMessageForNewSnapshot(ctx context.Context, mboxID imap.InternalMailboxID) ([]SnapshotMessageResult, error)

	// GetMailboxMessagesChangedSince returns the messages of the mailbox whose
	// per-mailbox mod-sequence is strictly greater than since, ordered by UID.
	GetMailboxMessagesChangedSince(ctx context.Context, mboxID imap.InternalMailboxID, since imap.ModSeq) ([]SnapshotMessageResult, error)

	// GetMailboxHighestModSeq returns the current highest mod-sequence of the mailbox.
	GetMailboxHighestModSeq(ctx context.Context, mboxID imap.InternalMailboxID) (imap.ModSeq, error)

	// GetVanishedUIDsAfterModSeq returns the UIDs recorded as vanished with a
	// mod-sequence strictly greater than since.
	GetVanishedUIDsAfterModSeq(ctx context.Context, mboxID imap.InternalMailboxID, since imap.ModSeq) ([]imap.UID, error)

	// GetVanishedHistoryPrunedModSeq returns the highest mod-sequence for
	// which vanished history may no longer be complete. A value of zero means
	// no history has ever been pruned.
	GetVanishedHistoryPrunedModSeq(ctx context.Context, mboxID imap.InternalMailboxID) (imap.ModSeq, error)

	MailboxTranslateRemoteIDs(ctx context.Context, mboxIDs []imap.MailboxID) ([]imap.InternalMailboxID, error)

	MailboxFilterContains(ctx context.Context, mboxID imap.InternalMailboxID, messageIDs []MessageIDPair) ([]imap.InternalMessageID, error)

	GetMailboxCount(ctx context.Context) (int, error)

	GetAllMailboxesNameAndRemoteID(ctx context.Context) ([]MailboxNameAndRemoteID, error)
}

type MailboxWriteOps interface {
	MailboxReadOps

	CreateMailbox(ctx context.Context,
		mboxID imap.MailboxID,
		name string,
		flags, permFlags, attrs imap.FlagSet,
		uidValidity imap.UID) (*Mailbox, error)

	GetOrCreateMailbox(ctx context.Context,
		mboxID imap.MailboxID,
		name string,
		flags, permFlags, attrs imap.FlagSet,
		uidValidity imap.UID) (*Mailbox, error)

	GetOrCreateMailboxAlt(ctx context.Context,
		mbox imap.Mailbox,
		delimiter string,
		uidValidity imap.UID) (*Mailbox, error)

	RenameMailboxWithRemoteID(ctx context.Context, mboxID imap.MailboxID, name string) error

	DeleteMailboxWithRemoteID(ctx context.Context, mboxID imap.MailboxID) error

	DeleteMailboxWithRemoteIDSilent(ctx context.Context, mboxID imap.MailboxID) error

	AddMessagesToMailbox(ctx context.Context, mboxID imap.InternalMailboxID, messageIDs []MessageIDPair) ([]UIDWithFlags, error)

	// RemoveMessagesFromMailbox deletes messages from the mailbox's per-mailbox
	// table without hard deleting the message. The returned mod-sequence is the
	// new highest watermark allocated for the mailbox.
	RemoveMessagesFromMailbox(ctx context.Context, mboxID imap.InternalMailboxID, messageIDs []imap.InternalMessageID) (imap.ModSeq, error)

	ClearRecentFlagInMailboxOnMessage(ctx context.Context, mboxID imap.InternalMailboxID, messageID imap.InternalMessageID) error

	ClearRecentFlagsInMailbox(ctx context.Context, mboxID imap.InternalMailboxID) error

	CreateMailboxIfNotExists(ctx context.Context, mbox imap.Mailbox, delimiter string, uidValidity imap.UID) error

	SetMailboxMessagesDeletedFlag(ctx context.Context, mboxID imap.InternalMailboxID, messageIDs []imap.InternalMessageID, deleted bool) error

	SetMailboxSubscribed(ctx context.Context, mboxID imap.InternalMailboxID, subscribed bool) error

	UpdateRemoteMailboxID(ctx context.Context, mobxID imap.InternalMailboxID, remoteID imap.MailboxID) error

	SetMailboxUIDValidity(ctx context.Context, mboxID imap.InternalMailboxID, uidValidity imap.UID) error

	// AssignModSeqForMessageMetadata allocates the next mod-sequence of every
	// mailbox affected by a (global) message metadata change:
	//   - the origin mailbox gets a single new mod-sequence if originChangedIDs
	//     is non-empty and its rows are stamped with it;
	//   - every other mailbox containing any of globalChangedIDs gets its own
	//     new mod-sequence and the affected rows are stamped with it.
	// It returns the origin mod-sequence (0 if none) and a map of every stamped
	// mailbox (including the origin) to its new mod-sequence.
	AssignModSeqForMessageMetadata(ctx context.Context,
		originMboxID imap.InternalMailboxID,
		originChangedIDs []imap.InternalMessageID,
		globalChangedIDs []imap.InternalMessageID,
	) (originModSeq imap.ModSeq, modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq, err error)

	AddFlagsToAllMailboxes(ctx context.Context, flags ...string) error

	AddPermFlagsToAllMailboxes(ctx context.Context, flags ...string) error
}

type SnapshotMessageResult struct {
	InternalID imap.InternalMessageID `json:"uid_message"`
	RemoteID   imap.MessageID         `json:"remote_id"`
	UID        imap.UID               `json:"uid"`
	Recent     bool                   `json:"recent"`
	Deleted    bool                   `json:"deleted"`
	Flags      string                 `json:"flags"`
	ModSeq     imap.ModSeq            `json:"mod_seq"`
}

func (msg *SnapshotMessageResult) GetFlagSet() imap.FlagSet {
	var flagSet imap.FlagSet

	if len(msg.Flags) > 0 {
		flags := strings.Split(msg.Flags, ",")
		flagSet = imap.NewFlagSetFromSlice(flags)
	} else {
		flagSet = imap.NewFlagSet()
	}

	if msg.Deleted {
		flagSet.AddToSelf(imap.FlagDeleted)
	}

	if msg.Recent {
		flagSet.AddToSelf(imap.FlagRecent)
	}

	return flagSet
}

type UIDWithFlags struct {
	InternalID imap.InternalMessageID `json:"uid_message"`
	RemoteID   imap.MessageID         `json:"remote_id"`
	UID        imap.UID               `json:"uid"`
	Recent     bool                   `json:"recent"`
	Deleted    bool                   `json:"deleted"`
	Flags      string                 `json:"flags"`
	ModSeq     imap.ModSeq            `json:"mod_seq"`
}

func (u *UIDWithFlags) GetFlagSet() imap.FlagSet {
	var flagSet imap.FlagSet

	if len(u.Flags) > 0 {
		flags := strings.Split(u.Flags, ",")
		flagSet = imap.NewFlagSetFromSlice(flags)
	} else {
		flagSet = imap.NewFlagSet()
	}

	if u.Deleted {
		flagSet.AddToSelf(imap.FlagDeleted)
	}

	if u.Recent {
		flagSet.AddToSelf(imap.FlagRecent)
	}

	return flagSet
}
