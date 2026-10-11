package state

import (
	"context"
	"fmt"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/contexts"
)

type RemoteAddMessageFlagsStateUpdate struct {
	MessageIDStateFilter
	flag            string
	modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq
}

func NewRemoteAddMessageFlagsStateUpdate(messageID imap.InternalMessageID, flag string, modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq) Update {
	return &RemoteAddMessageFlagsStateUpdate{
		MessageIDStateFilter: MessageIDStateFilter{MessageID: messageID},
		flag:                 flag,
		modSeqByMailbox:       modSeqByMailbox,
	}
}

func (u *RemoteAddMessageFlagsStateUpdate) Apply(ctx context.Context, tx db.Transaction, s *State) error {
	return s.PushResponder(ctx, tx, NewFetch(u.MessageID, imap.NewFlagSet(u.flag), contexts.IsUID(ctx), contexts.IsSilent(ctx), false, FetchFlagOpAdd, u.modSeqByMailbox))
}

func (u *RemoteAddMessageFlagsStateUpdate) String() string {
	return fmt.Sprintf("RemoteAddMessageFlagsStateUpdate %v flag = %v", u.MessageIDStateFilter.String(), u.flag)
}

type RemoteRemoveMessageFlagsStateUpdate struct {
	MessageIDStateFilter
	flag            string
	modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq
}

func NewRemoteRemoveMessageFlagsStateUpdate(messageID imap.InternalMessageID, flag string, modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq) Update {
	return &RemoteRemoveMessageFlagsStateUpdate{
		MessageIDStateFilter: MessageIDStateFilter{MessageID: messageID},
		flag:                 flag,
		modSeqByMailbox:       modSeqByMailbox,
	}
}

func (u *RemoteRemoveMessageFlagsStateUpdate) Apply(ctx context.Context, tx db.Transaction, s *State) error {
	return s.PushResponder(ctx, tx, NewFetch(u.MessageID, imap.NewFlagSet(u.flag), contexts.IsUID(ctx), contexts.IsSilent(ctx), false, FetchFlagOpRem, u.modSeqByMailbox))
}

func (u *RemoteRemoveMessageFlagsStateUpdate) String() string {
	return fmt.Sprintf("RemoteRemoveMessageFlagsStateUpdate %v flag = %v", u.MessageIDStateFilter.String(), u.flag)
}

type RemoteMessageDeletedStateUpdate struct {
	MessageIDStateFilter
	remoteID imap.MessageID //nolint: unused
}
