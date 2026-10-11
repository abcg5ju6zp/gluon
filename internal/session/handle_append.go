package session

import (
	"context"
	"errors"

	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/response"
	"github.com/ProtonMail/gluon/internal/state"
	"github.com/ProtonMail/gluon/profiling"
	"github.com/ProtonMail/gluon/reporter"
	"github.com/ProtonMail/gluon/rfcvalidation"
)

func (s *Session) handleAppend(ctx context.Context, tag string, cmd *command.Append, ch chan response.Response) error {
	profiling.Start(ctx, profiling.CmdTypeAppend)
	defer profiling.Stop(ctx, profiling.CmdTypeAppend)

	nameUTF8, err := s.decodeMailboxName(cmd.Mailbox)
	if err != nil {
		return err
	}

	flags, err := validateStoreFlags(cmd.Flags)
	if err != nil {
		return response.Bad(tag).WithError(err)
	}

	// A non-nil upload means the literal was streamed into a spool object (LITERAL+ or the
	// streaming sink); otherwise the legacy buffered path is used.
	var upload state.Upload
	if cmd.Upload != nil {
		var ok bool

		upload, ok = cmd.Upload.(state.Upload)
		if !ok {
			return response.Bad(tag).WithError(errors.New("internal error: invalid append upload handle"))
		}
	}

	var messageUID imap.UID

	err = s.state.AppendOnlyMailbox(ctx, nameUTF8, func(mailbox state.AppendOnlyMailbox, isSameMBox bool) error {
		isDrafts, err := mailbox.IsDrafts(ctx)
		if err != nil {
			return err
		}

		if !isDrafts {
			literal := cmd.Literal

			if upload != nil {
				literal, err = upload.Materialize(ctx)
				if err != nil {
					return err
				}
			}

			if err := rfcvalidation.ValidateMessageHeaderFields(literal); err != nil {
				return response.Bad(tag).WithError(err)
			}
		}

		if upload != nil {
			messageUID, err = mailbox.AppendUpload(ctx, upload, flags, cmd.DateTime)
		} else {
			messageUID, err = mailbox.Append(ctx, cmd.Literal, flags, cmd.DateTime)
		}
		if err != nil {
			// no events in sentinel so far.
			if shouldReportIMAPCommandError(err) {
				reporter.MessageWithContext(ctx,
					"Failed to append message to mailbox from state",
					reporter.Context{"error": err, "mailbox": nameUTF8},
				)
			}

			return err
		}

		if isSameMBox {
			if err := flush(ctx, mailbox, true, ch); err != nil {
				return err
			}
		}

		var okResponse response.Response = response.Ok(tag).
			WithItems(response.ItemAppendUID(mailbox.UIDValidity(), messageUID)).
			WithMessage("APPEND")

		if upload != nil {
			// Keep the spool object until the tagged response is confirmed written; a lost
			// connection before that point lets the client retry and receive the same UID.
			okResponse = &afterSendResponse{
				inner: okResponse,
				after: func(sendErr error) {
					if sendErr == nil {
						if removeErr := upload.RemoveSpool(); removeErr != nil {
							s.log.WithError(removeErr).Error("Failed to remove append spool object")
						}
					}
				},
			}
		}

		ch <- okResponse

		return nil
	})
	if errors.Is(err, state.ErrNoSuchMailbox) {
		return response.No(tag).WithError(err).WithItems(response.ItemTryCreate())
	}

	return err
}
