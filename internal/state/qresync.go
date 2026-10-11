package state

import (
	"context"
	"slices"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/imap/command"
	"github.com/bradenaw/juniper/xmaps"
)

// QResyncParameters holds the QRESYNC SELECT/EXAMINE parameters (RFC 7162):
// the UIDVALIDITY the client knows, its last known highest mod-sequence and,
// optionally, the set of UIDs the client believes the mailbox contains.
type QResyncParameters struct {
	UIDValidity    imap.UID
	ModSeq         imap.ModSeq
	KnownUIDRanges []command.SeqRange // nil when the client omitted the known-uids set
	hasKnownUIDs   bool
}

// NewQResyncParameters creates parameters with an optional known UID range set.
func NewQResyncParameters(uidValidity imap.UID, modSeq imap.ModSeq, knownUIDRanges []command.SeqRange, hasKnownUIDs bool) *QResyncParameters {
	return &QResyncParameters{
		UIDValidity:    uidValidity,
		ModSeq:         modSeq,
		KnownUIDRanges: knownUIDRanges,
		hasKnownUIDs:   hasKnownUIDs,
	}
}

// QResyncChangedMessage describes a message present in the mailbox whose
// metadata changed after the client's last known mod-sequence.
type QResyncChangedMessage struct {
	Seq    imap.SeqID
	UID    imap.UID
	Flags  imap.FlagSet
	ModSeq imap.ModSeq
}

// QResyncResult is the result of an incremental QRESYNC synchronization.
// Applied is false when the client's UIDVALIDITY did not match and a full
// synchronization response must be sent instead.
type QResyncResult struct {
	Applied         bool
	HighestModSeq   imap.ModSeq
	VanishedEarlier []imap.UID
	Changed         []QResyncChangedMessage
}

func (state *State) computeQResync(
	ctx context.Context,
	client db.ReadOnly,
	mbox *db.Mailbox,
	snap *snapshot,
	params *QResyncParameters,
) (*QResyncResult, error) {
	result := &QResyncResult{HighestModSeq: mbox.HighestModSeq}

	// UIDVALIDITY mismatch: fall back to a full synchronization.
	if params.UIDValidity != mbox.UIDValidity {
		return result, nil
	}

	// A client watermark above the server watermark is an impossible state.
	if params.ModSeq > mbox.HighestModSeq {
		return nil, ErrQResyncModSeqTooHigh
	}

	result.Applied = true

	// Messages still present whose metadata changed.
	if params.ModSeq < mbox.HighestModSeq {
		changed, err := client.GetMailboxMessagesChangedSince(ctx, mbox.ID, params.ModSeq)
		if err != nil {
			return nil, err
		}

		for _, msg := range changed {
			flags := msg.GetFlagSet()
			flags.RemoveFromSelf(imap.FlagRecent)

			changedMessage := QResyncChangedMessage{
				UID:    msg.UID,
				Flags:  flags,
				ModSeq: msg.ModSeq,
			}

			if snapMsg, ok := snap.messages.getWithUID(msg.UID); ok {
				changedMessage.Seq = snapMsg.Seq
			}

			result.Changed = append(result.Changed, changedMessage)
		}
	}

	// UIDs that vanished since the client's watermark.
	if params.hasKnownUIDs {
		knownUIDs := expandKnownUIDRanges(params.KnownUIDRanges, snap)

		currentUIDs := make(xmaps.Set[imap.UID], snap.len())
		for _, msg := range snap.messages.all() {
			currentUIDs.Add(msg.UID)
		}

		for _, uid := range knownUIDs {
			if !currentUIDs.Contains(uid) {
				result.VanishedEarlier = append(result.VanishedEarlier, uid)
			}
		}
	} else if params.ModSeq < mbox.HighestModSeq {
		// Without a known UID set the server needs complete vanished history
		// for everything after the client watermark.
		prunedModSeq, err := client.GetVanishedHistoryPrunedModSeq(ctx, mbox.ID)
		if err != nil {
			return nil, err
		}

		if params.ModSeq <= prunedModSeq {
			return nil, ErrQResyncHistoryPruned
		}

		vanished, err := client.GetVanishedUIDsAfterModSeq(ctx, mbox.ID, params.ModSeq)
		if err != nil {
			return nil, err
		}

		result.VanishedEarlier = vanished
	}

	return result, nil
}

// expandKnownUIDRanges expands the QRESYNC known-uids sequence set into a
// de-duplicated, ordered list of UIDs. "*" resolves to the highest UID
// currently present in the mailbox.
func expandKnownUIDRanges(ranges []command.SeqRange, snap *snapshot) []imap.UID {
	uidSet := make(map[imap.UID]struct{})

	var lastUID imap.UID

	if snap.len() > 0 {
		lastUID = snap.messages.last().UID
	}

	resolve := func(n command.SeqNum) imap.UID {
		if n == command.SeqNumValueAsterisk {
			return lastUID
		}

		return imap.UID(uint32(n))
	}

	for _, r := range ranges {
		begin := resolve(r.Begin)
		end := resolve(r.End)

		if r.Begin == command.SeqNumValueAsterisk && r.End != command.SeqNumValueAsterisk {
			begin, end = end, begin
		}

		// UIDs above the highest UID ever assigned in the mailbox never
		// existed and must neither be expanded nor reported as VANISHED.
		if begin < 1 {
			begin = 1
		}

		if end > lastUID {
			end = lastUID
		}

		for uid := begin; uid <= end; uid++ {
			uidSet[uid] = struct{}{}
		}
	}

	uids := make([]imap.UID, 0, len(uidSet))

	for uid := range uidSet {
		uids = append(uids, uid)
	}

	slices.Sort(uids)

	return uids
}
