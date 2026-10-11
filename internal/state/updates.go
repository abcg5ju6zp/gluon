package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/contexts"
	"github.com/ProtonMail/gluon/internal/ids"
	"github.com/bradenaw/juniper/xslices"
)

type Update interface {
	// Filter returns true when the state can be passed into A.
	Filter(s *State) bool
	// Apply the update to a given state.
	Apply(cxt context.Context, tx db.Transaction, s *State) error

	String() string
}

type messageFlagsComboStateUpdate struct {
	AllStateFilter
	updates []Update
}

func newMessageFlagsComboStateUpdate() *messageFlagsComboStateUpdate {
	return &messageFlagsComboStateUpdate{}
}

func (u *messageFlagsComboStateUpdate) Apply(ctx context.Context, tx db.Transaction, s *State) error {
	for _, v := range u.updates {
		if err := v.Apply(ctx, tx, s); err != nil {
			return err
		}
	}

	return nil
}

func (u *messageFlagsComboStateUpdate) addUpdate(update Update) {
	u.updates = append(u.updates, update)
}

func (u *messageFlagsComboStateUpdate) String() string {
	return fmt.Sprintf("messageFlagsComboStateUpdate: %v", u.updates)
}

type messageFlagsAddedStateUpdate struct {
	AllStateFilter
	messageIDs      []imap.InternalMessageID
	flags           imap.FlagSet
	mboxID          db.MailboxIDPair
	stateID         StateID
	modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq
}

func newMessageFlagsAddedStateUpdate(flags imap.FlagSet, mboxID db.MailboxIDPair, messageIDs []imap.InternalMessageID, stateID StateID, modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq) Update {
	return &messageFlagsAddedStateUpdate{
		flags:           flags,
		mboxID:          mboxID,
		messageIDs:      messageIDs,
		stateID:         stateID,
		modSeqByMailbox: modSeqByMailbox,
	}
}

func (u *messageFlagsAddedStateUpdate) Apply(ctx context.Context, tx db.Transaction, s *State) error {
	for _, messageID := range u.messageIDs {
		newFlags := u.flags

		if err := s.PushResponder(ctx, tx, NewFetch(
			messageID,
			newFlags,
			contexts.IsUID(ctx),
			s.StateID == u.stateID && contexts.IsSilent(ctx),
			s.snap.mboxID != u.mboxID,
			FetchFlagOpAdd,
			u.modSeqByMailbox,
		)); err != nil {
			return err
		}
	}

	return nil
}

func (u *messageFlagsAddedStateUpdate) String() string {
	return fmt.Sprintf("MessagFlagsAddedStateUpdate: mbox = %v messages = %v flags = %v",
		u.mboxID.InternalID.ShortID(),
		xslices.Map(u.messageIDs, func(id imap.InternalMessageID) string {
			return id.ShortID()
		}),
		u.flags)
}

// applyMessageFlagsAdded adds the flags to the given messages.
func (state *State) applyMessageFlagsAdded(ctx context.Context,
	tx db.Transaction,
	messageIDs []imap.InternalMessageID,
	addFlags imap.FlagSet) ([]Update, error) {
	if addFlags.ContainsUnchecked(imap.FlagRecentLowerCase) {
		return nil, fmt.Errorf("the recent flag is read-only")
	}

	var allUpdates []Update

	// Since DB state can be more up to date then the flag state we should only emit add flag updates for values
	// that actually changed.

	curFlags, err := tx.GetMessagesFlags(ctx, messageIDs)
	if err != nil {
		return nil, err
	}

	doFlagAdd := func(check func(*imap.FlagSet) bool, remoteDo func([]imap.MessageID) ([]Update, error)) error {
		if check(&addFlags) {
			var messagesToApply []imap.MessageID

			for _, msg := range curFlags {
				if !check(&msg.FlagSet) && !ids.IsRecoveredRemoteMessageID(msg.RemoteID) {
					messagesToApply = append(messagesToApply, msg.RemoteID)
				}
			}

			if len(messagesToApply) != 0 {
				updates, err := remoteDo(messagesToApply)
				if err != nil {
					return err
				}

				allUpdates = append(allUpdates, updates...)
			}
		}

		return nil
	}

	// If setting messages as seen, only set those messages that aren't currently seen.
	if err := doFlagAdd(func(set *imap.FlagSet) bool {
		return set.ContainsUnchecked(imap.FlagSeenLowerCase)
	}, func(ids []imap.MessageID) ([]Update, error) {
		return state.user.GetRemote().SetMessagesSeen(ctx, tx, ids, true)
	}); err != nil {
		return nil, err
	}

	// If setting messages as flagged, only set those messages that aren't currently flagged.
	if err := doFlagAdd(func(set *imap.FlagSet) bool {
		return set.ContainsUnchecked(imap.FlagFlaggedLowerCase)
	}, func(ids []imap.MessageID) ([]Update, error) {
		return state.user.GetRemote().SetMessagesFlagged(ctx, tx, ids, true)
	}); err != nil {
		return nil, err
	}

	// If setting messages as forwarded, only set those messages that aren't currently forwarded.
	if err := doFlagAdd(func(set *imap.FlagSet) bool {
		return set.ContainsAnyUnchecked(imap.ForwardFlagListLowerCase...)
	}, func(ids []imap.MessageID) ([]Update, error) {
		return state.user.GetRemote().SetMessagesForwarded(ctx, tx, ids, true)
	}); err != nil {
		return nil, err
	}

	// Add all known variations of forward flags to the list if one of them is present.
	if addFlags.ContainsAnyUnchecked(imap.ForwardFlagListLowerCase...) {
		addFlags.AddToSelf(imap.ForwardFlagList...)
	}

	flagStateUpdate := newMessageFlagsComboStateUpdate()

	var deletedChangedIDs []imap.InternalMessageID

	if addFlags.ContainsUnchecked(imap.FlagDeletedLowerCase) {
		if err := tx.SetMailboxMessagesDeletedFlag(ctx, state.snap.mboxID.InternalID, messageIDs, true); err != nil {
			return nil, err
		}

		deletedChangedIDs = state.filterSnapMessagesWithoutFlag(messageIDs, imap.FlagDeletedLowerCase)

		flagStateUpdate.addUpdate(newMessageFlagsAddedStateUpdate(imap.NewFlagSet(imap.FlagDeleted), state.snap.mboxID, messageIDs, state.StateID, nil))
	}

	globalChangedIDSet := make(map[imap.InternalMessageID]struct{})

	remainingFlags := addFlags.Remove(imap.FlagDeleted)
	for _, flag := range remainingFlags {
		flagLowerCase := strings.ToLower(flag)

		messagesToFlag := make([]imap.InternalMessageID, 0, len(messageIDs)/2)

		for _, v := range curFlags {
			if !v.FlagSet.ContainsUnchecked(flagLowerCase) {
				messagesToFlag = append(messagesToFlag, v.ID)
			}
		}

		if err := tx.AddFlagToMessages(ctx, messagesToFlag, flag); err != nil {
			return nil, err
		}

		for _, id := range messagesToFlag {
			globalChangedIDSet[id] = struct{}{}
		}

		flagStateUpdate.addUpdate(newMessageFlagsAddedStateUpdate(remainingFlags, state.snap.mboxID, messagesToFlag, state.StateID, nil))
	}

	modSeqByMailbox, err := state.assignMetadataModSeq(ctx, tx, deletedChangedIDs, globalChangedIDSet)
	if err != nil {
		return nil, err
	}

	// Propagate the allocated mod-sequences to the generated fetch responders.
	for _, update := range flagStateUpdate.updates {
		if added, ok := update.(*messageFlagsAddedStateUpdate); ok {
			added.modSeqByMailbox = modSeqByMailbox
		}
	}

	return append(allUpdates, flagStateUpdate), nil
}

type messageFlagsRemovedStateUpdate struct {
	AllStateFilter
	messageIDs      []imap.InternalMessageID
	flags           imap.FlagSet
	mboxID          db.MailboxIDPair
	stateID         StateID
	modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq
}

func NewMessageFlagsRemovedStateUpdate(flags imap.FlagSet, mboxID db.MailboxIDPair, messageIDs []imap.InternalMessageID, stateID StateID, modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq) Update {
	return &messageFlagsRemovedStateUpdate{
		flags:           flags,
		mboxID:          mboxID,
		messageIDs:      messageIDs,
		stateID:         stateID,
		modSeqByMailbox: modSeqByMailbox,
	}
}

func (u *messageFlagsRemovedStateUpdate) Apply(ctx context.Context, tx db.Transaction, s *State) error {
	for _, messageID := range u.messageIDs {
		newFlags := u.flags

		if err := s.PushResponder(ctx, tx, NewFetch(
			messageID,
			newFlags,
			contexts.IsUID(ctx),
			s.StateID == u.stateID && contexts.IsSilent(ctx),
			s.snap.mboxID != u.mboxID,
			FetchFlagOpRem,
			u.modSeqByMailbox,
		)); err != nil {
			return err
		}
	}

	return nil
}

func (u *messageFlagsRemovedStateUpdate) String() string {
	return fmt.Sprintf("MessagFlagsRemovedStateUpdate: mbox = %v messages = %v flags = %v",
		u.mboxID.InternalID,
		xslices.Map(u.messageIDs, func(id imap.InternalMessageID) string {
			return id.ShortID()
		}),
		u.flags)
}

// applyMessageFlagsRemoved removes the flags from the given messages.
func (state *State) applyMessageFlagsRemoved(ctx context.Context,
	tx db.Transaction,
	messageIDs []imap.InternalMessageID,
	remFlags imap.FlagSet) ([]Update, error) {
	if remFlags.ContainsUnchecked(imap.FlagRecentLowerCase) {
		return nil, fmt.Errorf("the recent flag is read-only")
	}

	var allUpdates []Update

	curFlags, err := tx.GetMessagesFlags(ctx, messageIDs)
	if err != nil {
		return nil, err
	}

	doRemoveFlags := func(check func(set *imap.FlagSet) bool, remoteDo func([]imap.MessageID) ([]Update, error)) error {
		if check(&remFlags) {
			var messagesToApply []imap.MessageID

			for _, msg := range curFlags {
				if check(&msg.FlagSet) && !ids.IsRecoveredRemoteMessageID(msg.RemoteID) {
					messagesToApply = append(messagesToApply, msg.RemoteID)
				}
			}

			if len(messagesToApply) != 0 {
				updates, err := remoteDo(messagesToApply)
				if err != nil {
					return err
				}

				allUpdates = append(allUpdates, updates...)
			}
		}

		return nil
	}

	// If setting messages as unseen, only set those messages that are currently seen.
	if err := doRemoveFlags(func(set *imap.FlagSet) bool {
		return set.ContainsUnchecked(imap.FlagSeenLowerCase)
	}, func(messageIDS []imap.MessageID) ([]Update, error) {
		return state.user.GetRemote().SetMessagesSeen(ctx, tx, messageIDS, false)
	}); err != nil {
		return nil, err
	}

	// If setting messages as unflagged, only set those messages that are currently flagged.
	if err := doRemoveFlags(func(set *imap.FlagSet) bool {
		return set.ContainsUnchecked(imap.FlagFlaggedLowerCase)
	}, func(messageIDS []imap.MessageID) ([]Update, error) {
		return state.user.GetRemote().SetMessagesFlagged(ctx, tx, messageIDS, false)
	}); err != nil {
		return nil, err
	}

	// If setting messages as unforwarded, only set those messages that are  currently forwarded
	if err := doRemoveFlags(func(set *imap.FlagSet) bool {
		return set.ContainsAnyUnchecked(imap.ForwardFlagListLowerCase...)
	}, func(messageIDS []imap.MessageID) ([]Update, error) {
		return state.user.GetRemote().SetMessagesForwarded(ctx, tx, messageIDS, false)
	}); err != nil {
		return nil, err
	}

	// Add all known variations of forward flags to the list if one of them is present.
	if remFlags.ContainsAnyUnchecked(imap.ForwardFlagListLowerCase...) {
		remFlags.AddToSelf(imap.ForwardFlagList...)
	}

	flagStateUpdate := newMessageFlagsComboStateUpdate()

	var deletedChangedIDs []imap.InternalMessageID

	if remFlags.ContainsUnchecked(imap.FlagDeletedLowerCase) {
		if err := tx.SetMailboxMessagesDeletedFlag(ctx, state.snap.mboxID.InternalID, messageIDs, false); err != nil {
			return nil, err
		}

		deletedChangedIDs = state.filterSnapMessagesWithFlag(messageIDs, imap.FlagDeletedLowerCase)

		flagStateUpdate.addUpdate(NewMessageFlagsRemovedStateUpdate(imap.NewFlagSet(imap.FlagDeleted), state.snap.mboxID, messageIDs, state.StateID, nil))
	}

	globalChangedIDSet := make(map[imap.InternalMessageID]struct{})

	remainingFlags := remFlags.Remove(imap.FlagDeleted)
	for _, flag := range remainingFlags {
		flagLowerCase := strings.ToLower(flag)

		messagesToFlag := make([]imap.InternalMessageID, 0, len(messageIDs)/2)

		for _, v := range curFlags {
			if v.FlagSet.ContainsUnchecked(flagLowerCase) {
				messagesToFlag = append(messagesToFlag, v.ID)
			}
		}

		if err := tx.RemoveFlagFromMessages(ctx, messagesToFlag, flag); err != nil {
			return nil, err
		}

		for _, id := range messagesToFlag {
			globalChangedIDSet[id] = struct{}{}
		}

		flagStateUpdate.addUpdate(NewMessageFlagsRemovedStateUpdate(remainingFlags, state.snap.mboxID, messagesToFlag, state.StateID, nil))
	}

	modSeqByMailbox, err := state.assignMetadataModSeq(ctx, tx, deletedChangedIDs, globalChangedIDSet)
	if err != nil {
		return nil, err
	}

	for _, update := range flagStateUpdate.updates {
		if removed, ok := update.(*messageFlagsRemovedStateUpdate); ok {
			removed.modSeqByMailbox = modSeqByMailbox
		}
	}

	return append(allUpdates, flagStateUpdate), nil
}

type messageFlagsSetStateUpdate struct {
	AllStateFilter
	messageIDs      []imap.InternalMessageID
	flags           imap.FlagSet
	mboxID          db.MailboxIDPair
	stateID         StateID
	modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq
}

func NewMessageFlagsSetStateUpdate(flags imap.FlagSet, mboxID db.MailboxIDPair, messageIDs []imap.InternalMessageID, stateID StateID, modSeqByMailbox map[imap.InternalMailboxID]imap.ModSeq) Update {
	return &messageFlagsSetStateUpdate{
		flags:           flags,
		mboxID:          mboxID,
		messageIDs:      messageIDs,
		stateID:         stateID,
		modSeqByMailbox: modSeqByMailbox,
	}
}

func (u *messageFlagsSetStateUpdate) Apply(ctx context.Context, tx db.Transaction, state *State) error {
	for _, messageID := range u.messageIDs {
		newFlags := u.flags

		if err := state.PushResponder(ctx, tx, NewFetch(
			messageID,
			newFlags,
			contexts.IsUID(ctx),
			state.StateID == u.stateID && contexts.IsSilent(ctx),
			state.snap.mboxID != u.mboxID,
			FetchFlagOpSet,
			u.modSeqByMailbox,
		)); err != nil {
			return err
		}
	}

	return nil
}

func (u *messageFlagsSetStateUpdate) String() string {
	return fmt.Sprintf("MessageFlagsSetStateUpdate: mbox = %v messages = %v flags=%v",
		u.mboxID.InternalID.ShortID(),
		xslices.Map(u.messageIDs, func(id imap.InternalMessageID) string {
			return id.ShortID()
		}),
		u.flags)
}

// applyMessageFlagsSet sets the flags of the given messages.
func (state *State) applyMessageFlagsSet(ctx context.Context,
	tx db.Transaction,
	messageIDs []imap.InternalMessageID,
	setFlags imap.FlagSet) ([]Update, error) {
	if setFlags.ContainsUnchecked(imap.FlagRecentLowerCase) {
		return nil, fmt.Errorf("the recent flag is read-only")
	}

	if state.snap == nil {
		return nil, nil
	}

	curFlags, err := tx.GetMessagesFlags(ctx, messageIDs)
	if err != nil {
		return nil, err
	}

	var allUpdates []Update

	doSetFlag := func(check func(set *imap.FlagSet) bool, remoteDo func([]imap.MessageID, bool) ([]Update, error)) error {
		setList := map[bool][]imap.MessageID{true: {}, false: {}}

		for _, msg := range curFlags {
			if hasValue := check(&setFlags); hasValue != check(&msg.FlagSet) && !ids.IsRecoveredRemoteMessageID(msg.RemoteID) {
				setList[hasValue] = append(setList[hasValue], msg.RemoteID)
			}
		}

		for seen, messageIDs := range setList {
			updates, err := remoteDo(messageIDs, seen)
			if err != nil {
				return err
			}

			allUpdates = append(allUpdates, updates...)
		}

		return nil
	}

	// If setting messages as seen, only set those messages that aren't currently seen, and vice versa.
	if err := doSetFlag(func(set *imap.FlagSet) bool {
		return set.ContainsUnchecked(imap.FlagSeenLowerCase)
	}, func(messageIDs []imap.MessageID, b bool) ([]Update, error) {
		return state.user.GetRemote().SetMessagesSeen(ctx, tx, messageIDs, b)
	}); err != nil {
		return nil, err
	}

	// If setting messages as flagged, only set those messages that aren't currently flagged, and vice versa.
	if err := doSetFlag(func(set *imap.FlagSet) bool {
		return set.ContainsUnchecked(imap.FlagFlaggedLowerCase)
	}, func(messageIDs []imap.MessageID, b bool) ([]Update, error) {
		return state.user.GetRemote().SetMessagesFlagged(ctx, tx, messageIDs, b)
	}); err != nil {
		return nil, err
	}

	// If setting messages as forwarded, only set those messages that aren't currently forwarded, and vice versa.
	if err := doSetFlag(func(set *imap.FlagSet) bool {
		return set.ContainsAnyUnchecked(imap.ForwardFlagListLowerCase...)
	}, func(messageIDs []imap.MessageID, b bool) ([]Update, error) {
		return state.user.GetRemote().SetMessagesForwarded(ctx, tx, messageIDs, b)
	}); err != nil {
		return nil, err
	}

	// Add all known variations of forward flags to the list if one of them is present.
	if setFlags.ContainsAnyUnchecked(imap.ForwardFlagListLowerCase...) {
		setFlags.AddToSelf(imap.ForwardFlagList...)
	}

	wantDeleted := setFlags.Contains(imap.FlagDeleted)

	var deletedChangedIDs []imap.InternalMessageID

	if wantDeleted {
		deletedChangedIDs = state.filterSnapMessagesWithoutFlag(messageIDs, imap.FlagDeletedLowerCase)
	} else {
		deletedChangedIDs = state.filterSnapMessagesWithFlag(messageIDs, imap.FlagDeletedLowerCase)
	}

	if err := tx.SetMailboxMessagesDeletedFlag(ctx, state.snap.mboxID.InternalID, messageIDs, wantDeleted); err != nil {
		return nil, err
	}

	remainingFlags := setFlags.Remove(imap.FlagDeleted)

	globalChangedIDSet := make(map[imap.InternalMessageID]struct{})

	for _, cur := range curFlags {
		if !cur.FlagSet.Equals(remainingFlags) {
			globalChangedIDSet[cur.ID] = struct{}{}
		}
	}

	if remainingFlags.Len() != 0 || len(globalChangedIDSet) != 0 {
		if err := tx.SetFlagsOnMessages(ctx, messageIDs, remainingFlags); err != nil {
			return nil, err
		}
	}

	modSeqByMailbox, err := state.assignMetadataModSeq(ctx, tx, deletedChangedIDs, globalChangedIDSet)
	if err != nil {
		return nil, err
	}

	return append(allUpdates, NewMessageFlagsSetStateUpdate(setFlags, state.snap.mboxID, messageIDs, state.StateID, modSeqByMailbox)), nil
}

// filterSnapMessagesWithFlag returns the given messages whose snapshot entry
// currently contains the given flag.
func (state *State) filterSnapMessagesWithFlag(messageIDs []imap.InternalMessageID, flagLowerCase string) []imap.InternalMessageID {
	var result []imap.InternalMessageID

	for _, id := range messageIDs {
		msg, ok := state.snap.messages.get(id)
		if !ok {
			continue
		}

		if msg.flags.ContainsUnchecked(flagLowerCase) {
			result = append(result, id)
		}
	}

	return result
}

// filterSnapMessagesWithoutFlag returns the given messages whose snapshot entry
// does not currently contain the given flag.
func (state *State) filterSnapMessagesWithoutFlag(messageIDs []imap.InternalMessageID, flagLowerCase string) []imap.InternalMessageID {
	var result []imap.InternalMessageID

	for _, id := range messageIDs {
		msg, ok := state.snap.messages.get(id)
		if !ok {
			continue
		}

		if !msg.flags.ContainsUnchecked(flagLowerCase) {
			result = append(result, id)
		}
	}

	return result
}

// assignMetadataModSeq allocates a single new mod-sequence per affected mailbox
// for one flag-changing operation and stamps the affected rows. The origin
// mailbox receives one value for the union of its deleted-only and global
// changes; every other mailbox containing a globally changed message receives
// its own value.
func (state *State) assignMetadataModSeq(
	ctx context.Context,
	tx db.Transaction,
	deletedChangedIDs []imap.InternalMessageID,
	globalChangedIDSet map[imap.InternalMessageID]struct{},
) (map[imap.InternalMailboxID]imap.ModSeq, error) {
	if len(deletedChangedIDs) == 0 && len(globalChangedIDSet) == 0 {
		return nil, nil
	}

	globalChangedIDs := make([]imap.InternalMessageID, 0, len(globalChangedIDSet))
	for id := range globalChangedIDSet {
		globalChangedIDs = append(globalChangedIDs, id)
	}

	originChangedSet := make(map[imap.InternalMessageID]struct{}, len(deletedChangedIDs)+len(globalChangedIDs))

	for _, id := range deletedChangedIDs {
		originChangedSet[id] = struct{}{}
	}

	for _, id := range globalChangedIDs {
		originChangedSet[id] = struct{}{}
	}

	originChangedIDs := make([]imap.InternalMessageID, 0, len(originChangedSet))
	for id := range originChangedSet {
		originChangedIDs = append(originChangedIDs, id)
	}

	_, modSeqByMailbox, err := tx.AssignModSeqForMessageMetadata(
		ctx,
		state.snap.mboxID.InternalID,
		originChangedIDs,
		globalChangedIDs,
	)
	if err != nil {
		return nil, err
	}

	if modSeq := modSeqByMailbox[state.snap.mboxID.InternalID]; modSeq != 0 {
		state.snap.advanceHighestModSeq(modSeq)
	}

	return modSeqByMailbox, nil
}

type mailboxRemoteIDUpdateStateUpdate struct {
	SnapFilter
	remoteID imap.MailboxID
}

func NewMailboxRemoteIDUpdateStateUpdate(internalID imap.InternalMailboxID, remoteID imap.MailboxID) Update {
	return &mailboxRemoteIDUpdateStateUpdate{
		SnapFilter: NewMBoxIDStateFilter(internalID),
		remoteID:   remoteID,
	}
}

func (u *mailboxRemoteIDUpdateStateUpdate) Apply(_ context.Context, _ db.Transaction, s *State) error {
	s.snap.mboxID.RemoteID = u.remoteID

	return nil
}

func (u *mailboxRemoteIDUpdateStateUpdate) String() string {
	return fmt.Sprintf("MailboxRemoteIDUpdateStateUpdate: %v remote = %v", u.SnapFilter.String(), u.remoteID.ShortID())
}

type mailboxDeletedStateUpdate struct {
	MBoxIDStateFilter
}

func NewMailboxDeletedStateUpdate(mboxID imap.InternalMailboxID) Update {
	return &mailboxDeletedStateUpdate{MBoxIDStateFilter: MBoxIDStateFilter{MboxID: mboxID}}
}

func (u *mailboxDeletedStateUpdate) Apply(_ context.Context, _ db.Transaction, s *State) error {
	s.markInvalid()

	return nil
}

func (u *mailboxDeletedStateUpdate) String() string {
	return fmt.Sprintf("MailboxDeletedStateUpdate: %v", u.MBoxIDStateFilter.String())
}

type uidValidityBumpedStateUpdate struct {
	AllStateFilter
}

func NewUIDValidityBumpedStateUpdate() Update {
	return &uidValidityBumpedStateUpdate{}
}

func (u *uidValidityBumpedStateUpdate) Apply(_ context.Context, _ db.Transaction, s *State) error {
	s.markInvalid()

	return nil
}

func (u *uidValidityBumpedStateUpdate) String() string {
	return "UIDValidityBumpedStateUpdate"
}
