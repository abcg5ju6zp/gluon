package session

import (
	"context"
	"errors"

	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/contexts"
	"github.com/ProtonMail/gluon/internal/response"
	"github.com/ProtonMail/gluon/internal/state"
	"github.com/ProtonMail/gluon/observability"
	"github.com/ProtonMail/gluon/observability/metrics"
	"github.com/ProtonMail/gluon/profiling"
)

func (s *Session) handleStore(ctx context.Context, tag string, cmd *command.Store, mailbox *state.Mailbox, ch chan response.Response) (response.Response, error) {
	if contexts.IsUID(ctx) {
		profiling.Start(ctx, profiling.CmdTypeUIDStore)
		defer profiling.Stop(ctx, profiling.CmdTypeUIDStore)
	} else {
		profiling.Start(ctx, profiling.CmdTypeStore)
		defer profiling.Stop(ctx, profiling.CmdTypeStore)
	}

	if cmd.Silent {
		ctx = contexts.AsSilent(ctx)
	}

	if mailbox.ReadOnly() {
		return nil, ErrReadOnly
	}

	// UNCHANGEDSINCE requires CONDSTORE to be enabled.
	if cmd.UnchangedSince != nil && !s.state.CondStoreEnabled() {
		return response.Bad(tag).WithError(errors.New("UNCHANGEDSINCE requires CONDSTORE to be enabled")), nil
	}

	flags, err := validateStoreFlags(cmd.Flags)
	if err != nil {
		return response.Bad(tag).WithError(err), nil
	}

	var unchangedSince *imap.ModSeq

	if cmd.UnchangedSince != nil {
		v := imap.ModSeq(*cmd.UnchangedSince)
		unchangedSince = &v
	}

	modified, err := mailbox.Store(ctx, cmd.SeqSet, cmd.Action, flags, unchangedSince)
	if errors.Is(err, state.ErrNoSuchMessage) {
		return response.Bad(tag).WithError(err), nil
	} else if err != nil {
		// A result of either a failed request (API unreachable), or the message does not exist on remote.
		observability.AddMessageRelatedMetric(ctx, metrics.GenerateFailedToStoreFlagsOnMessages())

		return nil, err
	}

	if err := flush(ctx, mailbox, false, ch); err != nil {
		return nil, err
	}

	// CONDSTORE: messages that failed the UNCHANGEDSINCE predicate are reported
	// with MODIFIED and the command finishes with a NO response.
	if unchangedSince != nil && len(modified) != 0 {
		modifiedSet := modifiedSeqSet(modified, contexts.IsUID(ctx))

		return response.No(tag).
			WithItems(response.ItemModified(modifiedSet)).
			WithError(errors.New("conditional STORE failed, messages were modified")), nil
	}

	var items []response.Item

	if mailbox.ExpungeIssued() {
		items = append(items, response.ItemExpungeIssued())
	}

	// CONDSTORE: report the resulting mailbox watermark, including for a silent
	// STORE which otherwise returns no per-message FETCH responses.
	if s.state.CondStoreEnabled() {
		items = append(items, response.ItemStoreModSeq(mailbox.HighestModSeq()))
	}

	return response.Ok(tag).
		WithItems(items...).
		WithMessage(okMessage(ctx)), nil
}

func modifiedSeqSet(modified []state.StoreModifiedMessage, asUID bool) string {
	if asUID {
		uids := make([]imap.UID, 0, len(modified))
		for _, m := range modified {
			uids = append(uids, m.UID)
		}

		return imap.NewSeqSetFromUID(uids).String()
	}

	seqs := make([]imap.SeqID, 0, len(modified))
	for _, m := range modified {
		seqs = append(seqs, m.Seq)
	}

	return imap.NewSeqSet(seqs).String()
}
