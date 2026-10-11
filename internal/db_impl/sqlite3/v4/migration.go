package v4

import (
	"context"
	"fmt"

	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/db_impl/sqlite3/utils"
)

type Migration struct{}

// AppendJournalTableName records in-flight APPEND uploads so interrupted receptions can be
// cleaned up on startup and commits already accepted by the connector but not locally
// confirmed can be recovered without creating a second message.
const (
	AppendJournalTableName = "append_journal_v1"

	FieldAttemptID         = "attempt_id"
	FieldIdentityHash      = "identity_hash"
	FieldContentHash       = "content_hash"
	FieldMailboxInternalID = "mailbox_internal_id"
	FieldRemoteID          = "remote_id"
	FieldInternalID        = "internal_id"
	FieldOriginState       = "origin_state"
	FieldUID               = "uid"
	FieldSize              = "size"
	FieldFlags             = "flags"
	FieldDate              = "date"
	FieldStatus            = "status"
	FieldCreatedAt         = "created_at"
	FieldUpdatedAt         = "updated_at"
)

func (m Migration) Run(ctx context.Context, tx utils.QueryWrapper, generator imap.UIDValidityGenerator) error {
	query := fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%[1]v` ("+
		"`%[2]v` TEXT PRIMARY KEY,"+
		"`%[3]v` TEXT NOT NULL DEFAULT '',"+
		"`%[4]v` TEXT NOT NULL DEFAULT '',"+
		"`%[5]v` INTEGER NOT NULL DEFAULT 0,"+
		"`%[6]v` TEXT NOT NULL DEFAULT '',"+
		"`%[7]v` TEXT NOT NULL DEFAULT '',"+
		"`%[8]v` INTEGER NOT NULL DEFAULT 0,"+
		"`%[9]v` INTEGER NOT NULL DEFAULT 0,"+
		"`%[10]v` INTEGER NOT NULL,"+
		"`%[11]v` TEXT NOT NULL DEFAULT '',"+
		"`%[12]v` INTEGER NOT NULL DEFAULT 0,"+
		"`%[13]v` INTEGER NOT NULL,"+
		"`%[14]v` INTEGER NOT NULL,"+
		"`%[15]v` INTEGER NOT NULL"+
		")",
		AppendJournalTableName,
		FieldAttemptID,
		FieldIdentityHash,
		FieldContentHash,
		FieldMailboxInternalID,
		FieldRemoteID,
		FieldInternalID,
		FieldOriginState,
		FieldUID,
		FieldSize,
		FieldFlags,
		FieldDate,
		FieldStatus,
		FieldCreatedAt,
		FieldUpdatedAt,
	)

	if _, err := utils.ExecQuery(ctx, tx, query); err != nil {
		return fmt.Errorf("failed to create append journal table: %w", err)
	}

	if _, err := utils.ExecQuery(ctx, tx, fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_append_journal_identity ON %v (%v)", AppendJournalTableName, FieldIdentityHash)); err != nil {
		return fmt.Errorf("failed to create append journal identity index: %w", err)
	}

	return nil
}
