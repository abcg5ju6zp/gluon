package session

import (
	"context"

	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/contexts"
	"github.com/ProtonMail/gluon/internal/response"
	"github.com/ProtonMail/gluon/internal/state"
	"github.com/ProtonMail/gluon/profiling"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
)

func (s *Session) handleSearch(ctx context.Context, tag string, cmd *command.Search, mailbox *state.Mailbox, ch chan response.Response) (response.Response, error) {
	if contexts.IsUID(ctx) {
		profiling.Start(ctx, profiling.CmdTypeUIDSearch)
		defer profiling.Stop(ctx, profiling.CmdTypeUIDSearch)
	} else {
		profiling.Start(ctx, profiling.CmdTypeSearch)
		defer profiling.Stop(ctx, profiling.CmdTypeSearch)
	}

	var decoder *encoding.Decoder

	if len(cmd.Charset) != 0 {
		encoding, err := ianaindex.IANA.Encoding(cmd.Charset)
		if err != nil {
			return nil, response.No(tag).WithItems(response.ItemBadCharset())
		}

		decoder = encoding.NewDecoder()
	} else {
		decoder = encoding.Nop.NewDecoder()
	}

	matches, err := mailbox.Search(ctx, cmd.Keys, decoder)
	if err != nil {
		return nil, err
	}

	asUID := contexts.IsUID(ctx)

	seqs := make([]uint32, 0, len(matches))

	for _, match := range matches {
		seqs = append(seqs, match.Number(asUID))
	}

	select {
	case ch <- response.Search(seqs...):

	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// RFC 8474 SEARCH RETURN (EMAILID)/(THREADID) correlators. The sequence number in the
	// correlator is always the message sequence number, even for UID SEARCH.
	var (
		wantEmailID  bool
		wantThreadID bool
	)

	for _, option := range cmd.ReturnOptions {
		switch option {
		case command.SearchReturnOptionEmailID:
			wantEmailID = true
		case command.SearchReturnOptionThreadID:
			wantThreadID = true
		}
	}

	if wantEmailID || wantThreadID {
		for _, match := range matches {
			// Emit correlators in the order the options were requested.
			for _, option := range cmd.ReturnOptions {
				var item response.Response

				switch option {
				case command.SearchReturnOptionEmailID:
					item = response.EmailIDSearchCorrelator(match.Seq, match.EmailID)
				case command.SearchReturnOptionThreadID:
					item = response.ThreadIDSearchCorrelator(match.Seq, match.ThreadID)
				}

				select {
				case ch <- item:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}
	}

	var items []response.Item

	if mailbox.ExpungeIssued() {
		items = append(items, response.ItemExpungeIssued())
	}

	return response.Ok(tag).
		WithItems(items...).
		WithMessage(okMessage(ctx)), nil
}
