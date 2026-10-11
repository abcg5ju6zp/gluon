package backend

import (
	"bytes"
	"context"
	"time"

	"github.com/ProtonMail/gluon/connector"
	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/ids"
	"github.com/ProtonMail/gluon/rfc822"
	"github.com/sirupsen/logrus"
)

// recoverPendingAppends cleans up interrupted APPEND receptions and completes local commits
// for messages the connector already accepted before a process exit. It runs at user load,
// before the connector update loop starts.
func (u *user) recoverPendingAppends(ctx context.Context) error {
	entries, err := db.ClientReadType(ctx, u.db, func(ctx context.Context, c db.ReadOnly) ([]*db.AppendJournalEntry, error) {
		return c.GetAllAppendJournal(ctx)
	})
	if err != nil {
		return err
	}

	for _, entry := range entries {
		entryLog := u.log.WithField("attempt_id", entry.AttemptID)

		switch entry.Status {
		case db.AppendJournalReceiving:
			// Process died while receiving the literal: nothing can have reached a mailbox.
			if err := u.deleteJournalRow(ctx, entry.AttemptID); err != nil {
				entryLog.WithError(err).Error("Failed to delete receiving append journal row")
			}

			_ = u.spool.Remove(entry.AttemptID)

		case db.AppendJournalConfirmed:
			// Locally committed; the spool object is no longer needed.
			_ = u.spool.Remove(entry.AttemptID)

		case db.AppendJournalAccepted:
			if err := u.recoverAcceptedAppend(ctx, entry); err != nil {
				entryLog.WithError(err).Error("Failed to recover accepted append")
			}

		case db.AppendJournalSubmitting:
			if err := u.recoverSubmittingAppend(ctx, entry); err != nil {
				entryLog.WithError(err).Error("Failed to recover submitting append")
			}
		}
	}

	// Any temporary file without a journal row belongs to an upload interrupted before the
	// receiving row was committed.
	if removed, err := u.spool.Sweep(); err != nil {
		u.log.WithError(err).Error("Failed to sweep append spool directory")
	} else if len(removed) > 0 {
		u.log.Debugf("Swept %v interrupted append spool objects", len(removed))
	}

	if u.journalRetention > 0 {
		cutoff := time.Now().Add(-u.journalRetention)

		if pruned, err := db.ClientWriteType(ctx, u.db, func(ctx context.Context, tx db.Transaction) (int64, error) {
			return tx.DeleteConfirmedAppendJournalBefore(ctx, cutoff)
		}); err != nil {
			u.log.WithError(err).Error("Failed to prune confirmed append journal entries")
		} else if pruned > 0 {
			u.log.Debugf("Pruned %v confirmed append journal entries", pruned)
		}
	}

	return nil
}

func (u *user) deleteJournalRow(ctx context.Context, attemptID string) error {
	return u.db.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.DeleteAppendJournal(ctx, attemptID)
	})
}

// recoverAcceptedAppend rebuilds the local commit for a connector-accepted message without
// calling the connector again.
func (u *user) recoverAcceptedAppend(ctx context.Context, entry *db.AppendJournalEntry) error {
	// Message already exists locally; just confirm the journal entry.
	if uid, err := u.messageUIDIfExists(ctx, entry); err == nil {
		return u.markJournalConfirmed(ctx, entry, uid)
	}

	literal, err := u.recoverLiteral(ctx, entry)
	if err != nil {
		return err
	}

	uid, err := db.ClientWriteType(ctx, u.db, func(ctx context.Context, tx db.Transaction) (imap.UID, error) {
		return u.commitRecoveredMessage(ctx, tx, entry, literal)
	})
	if err != nil {
		return err
	}

	_ = u.spool.Remove(entry.AttemptID)

	u.log.WithFields(logrus.Fields{
		"attempt_id": entry.AttemptID,
		"uid":        uid,
	}).Info("Recovered append accepted by connector")

	return nil
}

// recoverSubmittingAppend handles the ambiguous window around the connector call. When the
// message can already be found locally, the entry is confirmed. With a surviving spool object
// and an idempotent streaming connector, the create is replayed; otherwise the entry is dropped.
func (u *user) recoverSubmittingAppend(ctx context.Context, entry *db.AppendJournalEntry) error {
	if uid, err := u.messageUIDIfExists(ctx, entry); err == nil {
		return u.markJournalConfirmed(ctx, entry, uid)
	}

	obj, openErr := u.spool.Open(entry.AttemptID)
	if openErr != nil {
		// Outcome unknown and no data to retry; drop the row and any stale file.
		_ = u.deleteJournalRow(ctx, entry.AttemptID)
		_ = u.spool.Remove(entry.AttemptID)

		return nil
	}
	defer obj.Close()

	streaming, ok := u.connector.(connector.StreamingConnector)
	if !ok {
		_ = u.deleteJournalRow(ctx, entry.AttemptID)
		_ = u.spool.Remove(entry.AttemptID)

		return nil
	}

	literal, err := obj.Bytes()
	if err != nil {
		return err
	}

	var msg imap.Message

	err = u.db.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		cache := DBIMAPStateWrite{
			DBIMAPStateRead: DBIMAPStateRead{rd: tx},
			tx:              tx,
			user:            u,
		}

		mbox, err := tx.GetMailboxByID(ctx, entry.MailboxInternalID)
		if err != nil {
			return err
		}

		created, _, err := streaming.CreateMessageFromReader(
			ctx, &cache, mbox.RemoteID, bytes.NewReader(literal), int64(len(literal)),
			imap.NewFlagSetFromSlice(entry.Flags), entry.Date,
		)
		if err != nil {
			return err
		}

		msg = created

		// Idempotent connector may point at an already existing local message.
		internalID, err := tx.GetMessageIDFromRemoteID(ctx, created.ID)
		if err == nil {
			uid, err := tx.GetMessageUIDInMailbox(ctx, internalID, entry.MailboxInternalID)
			if err != nil {
				return err
			}

			return tx.SetAppendJournalConfirmed(ctx, entry.AttemptID, entry.MailboxInternalID, uid, time.Now())
		}

		return nil
	})

	if err != nil {
		// Could not replay: discard the ambiguous reception so a new upload is possible.
		_ = u.deleteJournalRow(ctx, entry.AttemptID)
		_ = u.spool.Remove(entry.AttemptID)

		return err
	}

	// The remote already mapped to a local message; nothing more to commit.
	if _, err := db.ClientReadType(ctx, u.db, func(ctx context.Context, c db.ReadOnly) (struct{}, error) {
		_, err := c.GetMessageIDFromRemoteID(ctx, msg.ID)
		return struct{}{}, err
	}); err == nil {
		_ = u.spool.Remove(entry.AttemptID)
		return nil
	}

	entry.RemoteID = msg.ID
	entry.InternalID = imap.NewInternalMessageID()

	if _, err := db.ClientWriteType(ctx, u.db, func(ctx context.Context, tx db.Transaction) (imap.UID, error) {
		return u.commitRecoveredMessage(ctx, tx, entry, literal)
	}); err != nil {
		return err
	}

	_ = u.spool.Remove(entry.AttemptID)

	return nil
}

func (u *user) messageUIDIfExists(ctx context.Context, entry *db.AppendJournalEntry) (imap.UID, error) {
	return db.ClientReadType(ctx, u.db, func(ctx context.Context, c db.ReadOnly) (imap.UID, error) {
		exists, err := c.MessageExists(ctx, entry.InternalID)
		if err != nil {
			return 0, err
		}

		if !exists {
			return 0, db.ErrNotFound
		}

		return c.GetMessageUIDInMailbox(ctx, entry.InternalID, entry.MailboxInternalID)
	})
}

func (u *user) markJournalConfirmed(ctx context.Context, entry *db.AppendJournalEntry, uid imap.UID) error {
	if err := u.db.Write(ctx, func(ctx context.Context, tx db.Transaction) error {
		return tx.SetAppendJournalConfirmed(ctx, entry.AttemptID, entry.MailboxInternalID, uid, time.Now())
	}); err != nil {
		return err
	}

	_ = u.spool.Remove(entry.AttemptID)

	return nil
}

// recoverLiteral reads the message bytes from the surviving spool object or, as a fallback,
// from the connector.
func (u *user) recoverLiteral(ctx context.Context, entry *db.AppendJournalEntry) ([]byte, error) {
	obj, err := u.spool.Open(entry.AttemptID)
	if err == nil {
		defer obj.Close()

		return obj.Bytes()
	}

	// The spool file is gone (memory-backed reception crashed, or file was lost): ask the
	// connector for the literal it accepted.
	return u.connector.GetMessageLiteral(ctx, entry.RemoteID)
}

// commitRecoveredMessage performs the local store write and DB insert for an accepted append.
func (u *user) commitRecoveredMessage(
	ctx context.Context,
	tx db.Transaction,
	entry *db.AppendJournalEntry,
	literal []byte,
) (imap.UID, error) {
	parsedMessage, err := imap.NewParsedMessage(literal)
	if err != nil {
		return 0, err
	}

	literalWithHeader, literalSize, err := rfc822.SetHeaderValueNoMemCopy(literal, ids.InternalIDKey, entry.InternalID.String())
	if err != nil {
		return 0, err
	}

	if err := u.store.SetUnchecked(entry.InternalID, literalWithHeader); err != nil {
		return 0, err
	}

	req := db.CreateMessageReq{
		Message: imap.Message{
			ID:    entry.RemoteID,
			Flags: imap.NewFlagSetFromSlice(entry.Flags),
			Date:  entry.Date,
		},
		LiteralSize: literalSize,
		Body:        parsedMessage.Body,
		Structure:   parsedMessage.Structure,
		Envelope:    parsedMessage.Envelope,
		InternalID:  entry.InternalID,
	}

	uid, _, err := tx.CreateMessageAndAddToMailbox(ctx, entry.MailboxInternalID, &req)
	if err != nil {
		return 0, err
	}

	if err := tx.SetAppendJournalConfirmed(ctx, entry.AttemptID, entry.MailboxInternalID, uid, time.Now()); err != nil {
		return 0, err
	}

	return uid, nil
}
