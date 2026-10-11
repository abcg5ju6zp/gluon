package v4

import (
	"context"
	"fmt"

	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/db_impl/sqlite3/utils"
	v1 "github.com/ProtonMail/gluon/internal/db_impl/sqlite3/v1"
)

type Migration struct{}

func (m Migration) Run(ctx context.Context, tx utils.QueryWrapper, _ imap.UIDValidityGenerator) error {
	// Add the per-mailbox highest mod-sequence column and the vanished history
	// prune boundary. Existing mailboxes start with a highest mod-sequence of 0.
	{
		query := fmt.Sprintf("ALTER TABLE `%v` ADD COLUMN `%v` INTEGER NOT NULL DEFAULT 0",
			v1.MailboxesTableName,
			MailboxesFieldHighestModSeq,
		)

		if _, err := utils.ExecQuery(ctx, tx, query); err != nil {
			return fmt.Errorf("failed to add highest mod-sequence column to mailboxes table: %w", err)
		}

		query = fmt.Sprintf("ALTER TABLE `%v` ADD COLUMN `%v` INTEGER NOT NULL DEFAULT 0",
			v1.MailboxesTableName,
			MailboxesFieldVanishedHistoryPrunedModSeq,
		)

		if _, err := utils.ExecQuery(ctx, tx, query); err != nil {
			return fmt.Errorf("failed to add vanished history prune boundary column to mailboxes table: %w", err)
		}
	}

	// Add the per-message mod-sequence column to all existing per-mailbox
	// message tables.
	{
		query := fmt.Sprintf("SELECT `%v` FROM `%v`", v1.MailboxesFieldID, v1.MailboxesTableName)

		mailboxIDs, err := utils.MapQueryRows[imap.InternalMailboxID](ctx, tx, query)
		if err != nil {
			return fmt.Errorf("failed to load mailbox ids for mod-sequence migration: %w", err)
		}

		for _, mboxID := range mailboxIDs {
			query := fmt.Sprintf("ALTER TABLE `%v` ADD COLUMN `%v` INTEGER NOT NULL DEFAULT 0",
				v1.MailboxMessageTableName(mboxID),
				MailboxMessagesFieldModSeq,
			)

			if _, err := utils.ExecQuery(ctx, tx, query); err != nil {
				return fmt.Errorf("failed to add mod-sequence column to mailbox message table %v: %w", mboxID, err)
			}
		}
	}

	// Create the vanished history table used for QRESYNC.
	if err := createVanishedHistoryTable(ctx, tx); err != nil {
		return err
	}

	return nil
}

func createVanishedHistoryTable(ctx context.Context, tx utils.QueryWrapper) error {
	query := fmt.Sprintf(
		"CREATE TABLE `%[1]v` ("+
			"`%[2]v` INTEGER NOT NULL, "+
			"`%[3]v` INTEGER NOT NULL, "+
			"`%[4]v` INTEGER NOT NULL, "+
			"CONSTRAINT `%[1]v_mailbox_id` FOREIGN KEY (`%[2]v`) REFERENCES `%[5]v` (`%[6]v`) ON DELETE CASCADE, "+
			"PRIMARY KEY (%[2]v, %[3]v)"+
			")",
		VanishedHistoryTableName,
		VanishedHistoryFieldMailboxID,
		VanishedHistoryFieldUID,
		VanishedHistoryFieldModSeq,
		v1.MailboxesTableName,
		v1.MailboxesFieldID,
	)

	if _, err := utils.ExecQuery(ctx, tx, query); err != nil {
		return fmt.Errorf("failed to create vanished history table: %w", err)
	}

	indexQuery := fmt.Sprintf("CREATE INDEX `%v` ON `%v` (`%v`, `%v`)",
		VanishedHistoryMailboxIDIndex,
		VanishedHistoryTableName,
		VanishedHistoryFieldMailboxID,
		VanishedHistoryFieldModSeq,
	)

	if _, err := utils.ExecQuery(ctx, tx, indexQuery); err != nil {
		return fmt.Errorf("failed to create vanished history index: %w", err)
	}

	return nil
}

// CreateMailboxMessageTableQuery returns the DDL used when creating per-mailbox
// message tables at runtime. It includes the mod-sequence column introduced by v4.
func CreateMailboxMessageTableQuery(id imap.InternalMailboxID) string {
	tableName := v1.MailboxMessageTableName(id)

	return fmt.Sprintf("CREATE TABLE `%[1]v` ("+
		"`%[2]v` integer NOT NULL PRIMARY KEY AUTOINCREMENT, `%[3]v` bool NOT NULL DEFAULT false, `%[4]v` bool NOT NULL DEFAULT true, "+
		"`%[5]v` uuid NOT NULL UNIQUE, "+
		"`%[6]v` string NOT NULL UNIQUE, "+
		"`%[7]v` integer NOT NULL DEFAULT 0, "+
		"CONSTRAINT `%[1]v_message_id` FOREIGN KEY (`%[5]v`) REFERENCES `%[8]v` (`%[9]v`) ON DELETE SET NULL)",
		tableName,
		v1.MailboxMessagesFieldUID,
		v1.MailboxMessagesFieldDeleted,
		v1.MailboxMessagesFieldRecent,
		v1.MailboxMessagesFieldMessageID,
		v1.MailboxMessagesFieldMessageRemoteID,
		MailboxMessagesFieldModSeq,
		v1.MessagesTableName,
		v1.MessagesFieldID,
	)
}

// CreateVanishedHistoryTableQuery returns the DDL for the vanished history table.
func CreateVanishedHistoryTableQuery() string {
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS `%[1]v` ("+
			"`%[2]v` INTEGER NOT NULL, "+
			"`%[3]v` INTEGER NOT NULL, "+
			"`%[4]v` INTEGER NOT NULL, "+
			"CONSTRAINT `%[1]v_mailbox_id` FOREIGN KEY (`%[2]v`) REFERENCES `%[5]v` (`%[6]v`) ON DELETE CASCADE, "+
			"PRIMARY KEY (%[2]v, %[3]v)"+
			")",
		VanishedHistoryTableName,
		VanishedHistoryFieldMailboxID,
		VanishedHistoryFieldUID,
		VanishedHistoryFieldModSeq,
		v1.MailboxesTableName,
		v1.MailboxesFieldID,
	)
}
