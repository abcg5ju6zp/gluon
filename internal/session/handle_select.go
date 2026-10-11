package session

import (
	"context"
	"errors"

	"github.com/ProtonMail/gluon/events"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/response"
	"github.com/ProtonMail/gluon/internal/state"
	"github.com/ProtonMail/gluon/profiling"
)

func (s *Session) handleSelect(ctx context.Context, tag string, cmd *command.Select, ch chan response.Response) error {
	profiling.Start(ctx, profiling.CmdTypeSelect)
	defer profiling.Stop(ctx, profiling.CmdTypeSelect)

	return s.handleSelectOrExamine(ctx, tag, cmd.Mailbox, cmd.Params, false, ch)
}

func (s *Session) handleSelectOrExamine(
	ctx context.Context,
	tag string,
	mailboxName string,
	params *command.SelectParams,
	readOnly bool,
	ch chan response.Response,
) error {
	nameUTF8, err := s.decodeMailboxName(mailboxName)
	if err != nil {
		return err
	}

	if params != nil && params.CondStore {
		s.state.EnableCondStore()
	}

	var qresyncParams *state.QResyncParameters

	if params != nil && params.QResync != nil {
		if !s.state.QResyncEnabled() {
			ch <- response.Bad(tag).WithError(errors.New("QRESYNC SELECT parameter used without QRESYNC being ENABLEd"))
			return nil
		}

		qresyncParams = state.NewQResyncParameters(
			imap.UID(params.QResync.UIDValidity),
			imap.ModSeq(params.QResync.ModSeq),
			params.QResync.KnownUIDs,
			params.QResync.HasKnownUIDs,
		)
	}

	condstoreActive := s.state.CondStoreEnabled()

	selectFunc := s.state.Select
	if readOnly {
		selectFunc = s.state.Examine
	}

	qresyncResult, selectErr := selectFunc(ctx, nameUTF8, qresyncParams, func(mailbox *state.Mailbox) error {
		flags, err := mailbox.Flags(ctx)
		if err != nil {
			return err
		}

		permFlags, err := mailbox.PermanentFlags(ctx)
		if err != nil {
			return err
		}

		uidNext, err := mailbox.UIDNext(ctx)
		if err != nil {
			return err
		}

		ch <- response.Flags().WithFlags(flags)
		ch <- response.Exists().WithCount(imap.SeqID(uint32(mailbox.Count())))
		ch <- response.Recent().WithCount(uint32(mailbox.GetMessagesWithFlagCount(imap.FlagRecent)))

		if readOnly {
			ch <- response.Ok().WithItems(response.ItemPermanentFlags(permFlags))
			ch <- response.Ok().WithItems(response.ItemUIDNext(uidNext))
			ch <- response.Ok().WithItems(response.ItemUIDValidity(mailbox.UIDValidity()))
		} else {
			ch <- response.Ok().WithItems(response.ItemPermanentFlags(permFlags)).WithMessage("Flags permitted")
			ch <- response.Ok().WithItems(response.ItemUIDNext(uidNext)).WithMessage("Predicted next UID")
			ch <- response.Ok().WithItems(response.ItemUIDValidity(mailbox.UIDValidity())).WithMessage("UIDs valid")
		}

		if condstoreActive {
			ch <- response.Ok().WithItems(response.ItemHighestModSeq(mailbox.HighestModSeq())).WithMessage("Highest")
		}

		if unseen, ok := mailbox.GetFirstMessageWithoutFlag(imap.FlagSeen); ok {
			ch <- response.Ok().WithItems(response.ItemUnseen(uint32(unseen.Seq))).WithMessage("Unseen messages")
		}

		return nil
	})

	// Resolve the result regardless of a later callback error where possible.
	if selectErr != nil {
		if errors.Is(selectErr, state.ErrQResyncModSeqTooHigh) {
			ch <- response.Bad(tag).WithError(selectErr)
			return nil
		}

		if errors.Is(selectErr, state.ErrQResyncHistoryPruned) {
			ch <- response.No(tag).WithError(selectErr)
			return nil
		}

		return selectErr
	}

	if qresyncResult != nil && qresyncResult.Applied {
		if len(qresyncResult.VanishedEarlier) > 0 {
			ch <- response.Vanished(true, imap.NewSeqSetFromUID(qresyncResult.VanishedEarlier))
		}

		for _, changed := range qresyncResult.Changed {
			items := []response.Item{
				response.ItemUID(changed.UID),
				response.ItemFlags(changed.Flags),
				response.ItemModSeq(changed.ModSeq),
			}

			ch <- response.Fetch(changed.Seq).WithItems(items...)
		}
	}

	if readOnly {
		ch <- response.Ok(tag).WithItems(response.ItemReadOnly()).WithMessage("EXAMINE")
	} else {
		ch <- response.Ok(tag).WithItems(response.ItemReadWrite()).WithMessage("SELECT")
	}

	if !readOnly {
		s.eventCh <- events.Select{
			SessionID: s.sessionID,
			Mailbox:   nameUTF8,
		}
	}

	return nil
}
