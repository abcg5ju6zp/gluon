package sqlite3

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/db_impl/sqlite3/utils"
	v1 "github.com/ProtonMail/gluon/internal/db_impl/sqlite3/v1"
	v4 "github.com/ProtonMail/gluon/internal/db_impl/sqlite3/v4"
)

const journalAllColumns = "`" + v4.FieldAttemptID + "`,`" +
	v4.FieldIdentityHash + "`,`" +
	v4.FieldContentHash + "`,`" +
	v4.FieldMailboxInternalID + "`,`" +
	v4.FieldRemoteID + "`,`" +
	v4.FieldInternalID + "`,`" +
	v4.FieldOriginState + "`,`" +
	v4.FieldUID + "`,`" +
	v4.FieldSize + "`,`" +
	v4.FieldFlags + "`,`" +
	v4.FieldDate + "`,`" +
	v4.FieldStatus + "`,`" +
	v4.FieldCreatedAt + "`,`" +
	v4.FieldUpdatedAt + "`"

func scanAppendJournalEntry(scanner utils.RowScanner) (*db.AppendJournalEntry, error) {
	var (
		entry          db.AppendJournalEntry
		attemptID      string
		identityHash   string
		contentHash    string
		remoteID       string
		internalIDText string
		flagsJSON      string
		dateUnix       int64
		createdUnix    int64
		updatedUnix    int64
	)

	if err := scanner.Scan(
		&attemptID,
		&identityHash,
		&contentHash,
		&entry.MailboxInternalID,
		&remoteID,
		&internalIDText,
		&entry.OriginState,
		&entry.UID,
		&entry.Size,
		&flagsJSON,
		&dateUnix,
		&entry.Status,
		&createdUnix,
		&updatedUnix,
	); err != nil {
		return nil, err
	}

	if internalIDText != "" {
		id, err := imap.InternalMessageIDFromString(internalIDText)
		if err != nil {
			return nil, err
		}

		entry.InternalID = id
	}

	entry.AttemptID = attemptID
	entry.IdentityHash = identityHash
	entry.ContentHash = contentHash
	entry.RemoteID = imap.MessageID(remoteID)

	if flagsJSON != "" {
		if err := json.Unmarshal([]byte(flagsJSON), &entry.Flags); err != nil {
			return nil, fmt.Errorf("failed to unmarshal journal flags: %w", err)
		}
	}

	if dateUnix != 0 {
		entry.Date = time.Unix(dateUnix, 0).UTC()
	}

	entry.CreatedAt = time.Unix(createdUnix, 0).UTC()
	entry.UpdatedAt = time.Unix(updatedUnix, 0).UTC()

	return &entry, nil
}

func (r readOps) GetAppendJournal(ctx context.Context, attemptID string) (*db.AppendJournalEntry, error) {
	query := fmt.Sprintf("SELECT %v FROM %v WHERE `%v` = ?",
		journalAllColumns,
		v4.AppendJournalTableName,
		v4.FieldAttemptID,
	)

	return utils.MapQueryRowFn(ctx, r.qw, query, scanAppendJournalEntry, attemptID)
}

func (r readOps) GetAppendJournalByIdentity(ctx context.Context, identityHash string) (*db.AppendJournalEntry, error) {
	query := fmt.Sprintf("SELECT %v FROM %v WHERE `%v` = ? ORDER BY `%v` DESC LIMIT 1",
		journalAllColumns,
		v4.AppendJournalTableName,
		v4.FieldIdentityHash,
		v4.FieldUpdatedAt,
	)

	return utils.MapQueryRowFn(ctx, r.qw, query, scanAppendJournalEntry, identityHash)
}

func (r readOps) GetAllAppendJournal(ctx context.Context) ([]*db.AppendJournalEntry, error) {
	query := fmt.Sprintf("SELECT %v FROM %v",
		journalAllColumns,
		v4.AppendJournalTableName,
	)

	return utils.MapQueryRowsFn(ctx, r.qw, query, scanAppendJournalEntry)
}

func (r readOps) GetTotalMessageSize(ctx context.Context) (int64, error) {
	query := fmt.Sprintf(
		"SELECT COALESCE(SUM(`%[1]v`), 0) FROM `%[2]v` WHERE `%[3]v` = FALSE",
		v1.MessagesFieldSize,
		v1.MessagesTableName,
		v1.MessagesFieldDeleted,
	)

	return utils.MapQueryRow[int64](ctx, r.qw, query)
}

func (r readOps) GetMessageUIDInMailbox(ctx context.Context, id imap.InternalMessageID, mbox imap.InternalMailboxID) (imap.UID, error) {
	query := fmt.Sprintf(
		"SELECT `%[1]v` FROM `%[2]v` WHERE `%[3]v` = ? AND `%[4]v` = FALSE",
		v1.MailboxMessagesFieldUID,
		v1.MailboxMessageTableName(mbox),
		v1.MailboxMessagesFieldMessageID,
		v1.MailboxMessagesFieldDeleted,
	)

	return utils.MapQueryRow[imap.UID](ctx, r.qw, query, id)
}

func (w writeOps) InsertAppendJournalReceiving(ctx context.Context, attemptID string, size int64, now time.Time) error {
	nowUnix := now.Unix()

	query := fmt.Sprintf(
		"INSERT INTO %v (`%v`,`%v`,`%v`,`%v`,`%v`) VALUES (?,?,?,?,?)",
		v4.AppendJournalTableName,
		v4.FieldAttemptID,
		v4.FieldSize,
		v4.FieldStatus,
		v4.FieldCreatedAt,
		v4.FieldUpdatedAt,
	)

	_, err := utils.ExecQuery(
		ctx, w.qw, query,
		attemptID,
		size,
		db.AppendJournalReceiving,
		nowUnix,
		nowUnix,
	)

	return err
}

func (w writeOps) SetAppendJournalSubmitting(
	ctx context.Context,
	attemptID string,
	identityHash string,
	contentHash string,
	mailboxID imap.InternalMailboxID,
	originState int64,
	flags []string,
	date time.Time,
	now time.Time,
) error {
	flagsJSON, err := json.Marshal(flags)
	if err != nil {
		return err
	}

	query := fmt.Sprintf(
		"UPDATE %v SET `%v`=?,`%v`=?,`%v`=?,`%v`=?,`%v`=?,`%v`=?,`%v`=?,`%v`=? WHERE `%v`=?",
		v4.AppendJournalTableName,
		v4.FieldIdentityHash,
		v4.FieldContentHash,
		v4.FieldMailboxInternalID,
		v4.FieldOriginState,
		v4.FieldFlags,
		v4.FieldDate,
		v4.FieldStatus,
		v4.FieldUpdatedAt,
		v4.FieldAttemptID,
	)

	dateUnix := int64(0)
	if !date.IsZero() {
		dateUnix = date.Unix()
	}

	return utils.ExecQueryAndCheckUpdatedNotZero(
		ctx, w.qw, query,
		identityHash,
		contentHash,
		mailboxID,
		originState,
		string(flagsJSON),
		dateUnix,
		db.AppendJournalSubmitting,
		now.Unix(),
		attemptID,
	)
}

func (w writeOps) SetAppendJournalAccepted(
	ctx context.Context,
	attemptID string,
	remoteID imap.MessageID,
	internalID imap.InternalMessageID,
	now time.Time,
) error {
	query := fmt.Sprintf(
		"UPDATE %v SET `%v`=?,`%v`=?,`%v`=?,`%v`=? WHERE `%v`=?",
		v4.AppendJournalTableName,
		v4.FieldRemoteID,
		v4.FieldInternalID,
		v4.FieldStatus,
		v4.FieldUpdatedAt,
		v4.FieldAttemptID,
	)

	return utils.ExecQueryAndCheckUpdatedNotZero(
		ctx, w.qw, query,
		remoteID,
		internalID.String(),
		db.AppendJournalAccepted,
		now.Unix(),
		attemptID,
	)
}

func (w writeOps) SetAppendJournalConfirmed(
	ctx context.Context,
	attemptID string,
	mailboxID imap.InternalMailboxID,
	uid imap.UID,
	now time.Time,
) error {
	query := fmt.Sprintf(
		"UPDATE %v SET `%v`=?,`%v`=?,`%v`=?,`%v`=? WHERE `%v`=?",
		v4.AppendJournalTableName,
		v4.FieldMailboxInternalID,
		v4.FieldUID,
		v4.FieldStatus,
		v4.FieldUpdatedAt,
		v4.FieldAttemptID,
	)

	return utils.ExecQueryAndCheckUpdatedNotZero(
		ctx, w.qw, query,
		mailboxID,
		uid,
		db.AppendJournalConfirmed,
		now.Unix(),
		attemptID,
	)
}

func (w writeOps) DeleteAppendJournal(ctx context.Context, attemptID string) error {
	query := fmt.Sprintf("DELETE FROM %v WHERE `%v` = ?",
		v4.AppendJournalTableName,
		v4.FieldAttemptID,
	)

	_, err := utils.ExecQuery(ctx, w.qw, query, attemptID)

	return err
}

func (w writeOps) DeleteConfirmedAppendJournalBefore(ctx context.Context, before time.Time) (int64, error) {
	query := fmt.Sprintf(
		"DELETE FROM %v WHERE `%v` = ? AND `%v` < ?",
		v4.AppendJournalTableName,
		v4.FieldStatus,
		v4.FieldUpdatedAt,
	)

	affected, err := utils.ExecQuery(
		ctx, w.qw, query,
		db.AppendJournalConfirmed,
		before.Unix(),
	)

	return int64(affected), err
}
