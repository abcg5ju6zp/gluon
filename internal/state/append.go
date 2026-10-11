package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ProtonMail/gluon/connector"
	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/appendctl"
	"github.com/ProtonMail/gluon/internal/ids"
	"github.com/ProtonMail/gluon/rfc822"
)

// AppendUpload commits an APPEND literal streamed into the given Upload handle. It mirrors
// AppendRegular/Append but keeps the journaled lifecycle of the streaming reception:
// quota/spool are handled by the Upload, and the append journal makes retries idempotent and
// allows recovery of connector-accepted but locally unconfirmed commits.
func (m *Mailbox) AppendUpload(ctx context.Context, upload Upload, flags imap.FlagSet, date time.Time) (imap.UID, error) {
	isSelectedMailbox := m.snap != nil && m.snap == m.state.snap

	attr, err := m.Attributes(ctx)
	if err != nil {
		return 0, err
	}

	appendIntoDrafts := attr.Contains(imap.AttrDrafts)

	literal, err := upload.Materialize(ctx)
	if err != nil {
		return 0, err
	}

	effectiveLiteral := literal

	// Same X-Pm-Gluon-Id based shortcut as AppendRegular (non-drafts only).
	if !appendIntoDrafts {
		internalIDString, err := rfc822.GetHeaderValue(literal, ids.InternalIDKey)
		if err != nil {
			return 0, err
		}

		if len(internalIDString) > 0 {
			msgID, err := imap.InternalMessageIDFromString(internalIDString)
			if err != nil {
				return 0, err
			}

			messageDeleted, err := stateDBReadResult(ctx, m.state, func(ctx context.Context, client db.ReadOnly) (bool, error) {
				return client.GetMessageDeletedFlag(ctx, msgID)
			})
			if err != nil {
				if !errors.Is(err, db.ErrNotFound) {
					return 0, err
				}

				m.log.WithError(err).Warn("The message has an unknown internal ID")
			} else if !messageDeleted {
				m.log.Debugf("Appending duplicate message with Internal ID:%v", msgID.ShortID())

				result, err := stateDBWriteResult(ctx, m.state, func(ctx context.Context, tx db.Transaction) ([]Update, []db.UIDWithFlags, error) {
					remoteID, err := tx.GetMessageRemoteID(ctx, msgID)
					if err != nil {
						return nil, nil, err
					}

					return m.state.actionAddMessagesToMailbox(
						ctx,
						tx,
						[]db.MessageIDPair{{InternalID: msgID, RemoteID: remoteID}},
						m.id,
						isSelectedMailbox,
					)
				})
				if err != nil {
					return 0, err
				}

				// No new message is created; the in-flight reception is fully discarded.
				upload.Abort(ctx)

				return result[0].UID, nil
			}
		}
	} else {
		newLiteral, err := rfc822.EraseHeaderValue(literal, ids.InternalIDKey)
		if err != nil {
			m.log.WithError(err).Error("Failed to erase Gluon internal id from draft")
		} else {
			effectiveLiteral = newLiteral
		}
	}

	identityHash := appendctl.IdentityHash(upload.ContentHash(), m.id.InternalID, flags, date)

	// Replays are only idempotent across distinct states/connections so that repeated
	// identical APPENDs within the same session still create distinct messages.
	originState := int64(0)
	if stateID, ok := GetStateIDFromContext(ctx); ok {
		originState = int64(stateID)
	}

	// Idempotent replay: a previous identical APPEND may already be done/partly done.
	if uid, handled, err := m.replayJournaledAppend(ctx, upload, identityHash, originState, isSelectedMailbox); handled || err != nil {
		return uid, err
	}

	// Mailbox message count and UID limit pre-checks (same as AppendRegular).
	if err := stateDBRead(ctx, m.state, func(ctx context.Context, client db.ReadOnly) error {
		messageCount, uid, err := client.GetMailboxMessageCountAndUID(ctx, m.snap.mboxID.InternalID)
		if err != nil {
			return err
		}

		if err := m.state.imapLimits.CheckMailBoxMessageCount(messageCount, 1); err != nil {
			return err
		}

		return m.state.imapLimits.CheckUIDCount(uid, 1)
	}); err != nil {
		return 0, err
	}

	var updates []Update

	var acceptedInfo *acceptedCommitInfo

	messageUID, err := db.ClientWriteType(ctx, m.state.user.GetDB(), func(ctx context.Context, tx db.Transaction) (imap.UID, error) {
		up, uid, info, actionErr := m.state.actionCreateMessageFromUpload(
			ctx,
			tx,
			m.snap.mboxID,
			upload,
			effectiveLiteral,
			flags,
			date,
			isSelectedMailbox,
			appendIntoDrafts,
			identityHash,
			originState,
		)
		if actionErr != nil {
			return 0, actionErr
		}

		updates = up
		acceptedInfo = info

		return uid, nil
	})
	if err != nil {
		// The connector accepted the message before the local transaction failed: persist the
		// "accepted" fact so a repeated identical APPEND only completes the local commit
		// (class-c failure).
		if acceptedInfo != nil {
			m.persistAcceptedAfterCommitFailure(ctx, upload.AttemptID(), acceptedInfo)
			upload.ReleaseReservation()

			return 0, fmt.Errorf("%w: %v", ErrAppendAcceptedButNotLocallyConfirmed, err)
		}

		// Reception did not produce any local message.
		upload.Abort(ctx)

		if errors.Is(err, connector.ErrMessageSizeExceedsLimits) {
			return 0, err
		}

		// Fall back to the recovery mailbox, exactly like Mailbox.Append.
		knownMessage, recoverErr := stateDBWriteResult(ctx, m.state, func(ctx context.Context, tx db.Transaction) ([]Update, bool, error) {
			return m.state.actionCreateRecoveredMessage(ctx, tx, effectiveLiteral, flags, date)
		})
		if recoverErr != nil && !knownMessage {
			m.log.WithError(recoverErr).Error("Failed to insert message into recovery mailbox")
		}

		if knownMessage {
			err = fmt.Errorf("%v: %w", err, ErrKnownRecoveredMessage)
		}

		return 0, err
	}

	// Apply state updates in a separate transaction, mirroring stateDBWriteResult.
	if len(updates) != 0 {
		if updateErr := m.state.user.GetDB().Write(ctx, func(ctx context.Context, tx db.Transaction) error {
			return m.state.user.QueueOrApplyStateUpdate(ctx, tx, updates...)
		}); updateErr != nil {
			// The message is already committed locally; keep journal+spool and let the retry
			// return the same UID, but release the reservation.
			upload.ReleaseReservation()

			return 0, fmt.Errorf("%w: %v", ErrAppendAcceptedButNotLocallyConfirmed, updateErr)
		}
	}

	// The spool object is retained until the tagged response is confirmed sent; the session
	// removes it afterwards. Only the reservation is released here.
	upload.ReleaseReservation()

	return messageUID, nil
}

// replayJournaledAppend handles a journal entry matching the current upload's identity.
func (m *Mailbox) replayJournaledAppend(
	ctx context.Context,
	upload Upload,
	identityHash string,
	originState int64,
	isSelectedMailbox bool,
) (imap.UID, bool, error) {
	entry, err := stateDBReadResult(ctx, m.state, func(ctx context.Context, client db.ReadOnly) (*db.AppendJournalEntry, error) {
		return client.GetAppendJournalByIdentity(ctx, identityHash)
	})
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return 0, false, nil
		}

		return 0, false, err
	}

	switch entry.Status {
	case db.AppendJournalReceiving:
		// A receiving row matching an identity should not be returned here; proceed.
		return 0, false, nil

	case db.AppendJournalAccepted:
		// Connector already accepted this message; rebuild the local commit without calling
		// the connector again, regardless of which session retried.
		if uid, ok := m.confirmedEntryUID(ctx, upload, entry); ok {
			return uid, true, nil
		}

		uid, rebuildErr := stateDBWriteResult(ctx, m.state, func(ctx context.Context, tx db.Transaction) ([]Update, imap.UID, error) {
			return m.state.rebuildAcceptedAppend(ctx, tx, entry, isSelectedMailbox)
		})
		if rebuildErr != nil {
			return 0, false, rebuildErr
		}

		// The new reception attempt is unused; discard it and drop the recovered spool.
		upload.Abort(ctx)
		_ = m.state.user.RemoveSpool(entry.AttemptID)

		return uid, true, nil

	case db.AppendJournalConfirmed:
		// A confirmed identical append is treated as a retry only when it originated on a
		// different state/connection AND its spool object still exists, i.e. the tagged
		// response was never confirmed written. Repeated APPENDs after delivery create new
		// messages.
		if originState != 0 && entry.OriginState != 0 && entry.OriginState != originState {
			if reader, openErr := m.state.user.OpenSpool(entry.AttemptID); openErr == nil {
				_ = reader.Close()
				if uid, ok := m.confirmedEntryUID(ctx, upload, entry); ok {
					return uid, true, nil
				}
			}
		}

		return 0, false, nil

	case db.AppendJournalSubmitting:
		if uid, ok := m.confirmedEntryUID(ctx, upload, entry); ok {
			return uid, true, nil
		}

		// Ambiguous remote outcome; proceed with a fresh connector call (idempotent
		// connectors return the same remote ID).
		return 0, false, nil
	}

	// Still receiving: a stale own row should not happen; proceed normally.
	return 0, false, nil
}

func (m *Mailbox) confirmedEntryUID(ctx context.Context, upload Upload, entry *db.AppendJournalEntry) (imap.UID, bool) {
	uid, err := stateDBReadResult(ctx, m.state, func(ctx context.Context, client db.ReadOnly) (imap.UID, error) {
		exists, err := client.MessageExists(ctx, entry.InternalID)
		if err != nil {
			return 0, err
		}

		if !exists {
			return 0, db.ErrNotFound
		}

		return client.GetMessageUIDInMailbox(ctx, entry.InternalID, entry.MailboxInternalID)
	})
	if err != nil {
		m.log.WithError(err).WithField("attempt_id", entry.AttemptID).
			Warn("confirmed journal entry lookup failed")
		return 0, false
	}

	upload.Abort(ctx)
	_ = m.state.user.RemoveSpool(entry.AttemptID)

	return uid, true
}

func (m *Mailbox) persistAcceptedAfterCommitFailure(ctx context.Context, attemptID string, info *acceptedCommitInfo) {
	if err := m.state.user.GetDB().Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.SetAppendJournalAccepted(ctx, attemptID, info.remoteID, info.internalID, time.Now())
	}); err != nil {
		m.log.WithError(err).WithField("attempt_id", attemptID).
			Error("Failed to persist accepted append journal state after commit failure")
	}
}
