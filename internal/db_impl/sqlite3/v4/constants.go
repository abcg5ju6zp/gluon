package v4

const (
	// MailboxesFieldHighestModSeq is the per-mailbox monotonically increasing
	// highest mod-sequence column added to the v1 mailboxes table.
	MailboxesFieldHighestModSeq = "highest_mod_seq"

	// MailboxesFieldVanishedHistoryPrunedModSeq records the highest mod-sequence
	// whose vanished entries may no longer be fully retained: incremental
	// QRESYNC (without an explicit known UID set) is only possible strictly above
	// this value. Zero means no history has ever been pruned.
	MailboxesFieldVanishedHistoryPrunedModSeq = "vanished_history_pruned_modseq"

	// MailboxMessagesFieldModSeq is the per-message (per-mailbox membership)
	// mod-sequence column added to each mailbox_message_<id> table.
	MailboxMessagesFieldModSeq = "mod_seq"
)

const (
	// VanishedHistoryTableName records the UIDs that vanished (expunged / moved
	// out) from a mailbox at a given mod-sequence. It is the data source for
	// QRESYNC VANISHED (EARLIER) responses when a client did not provide its
	// full set of known UIDs.
	VanishedHistoryTableName = "mailbox_message_vanished_history"

	VanishedHistoryFieldMailboxID = "mailbox_id"
	VanishedHistoryFieldUID       = "uid"
	VanishedHistoryFieldModSeq    = "mod_seq"

	VanishedHistoryMailboxIDIndex = "mailbox_message_vanished_history_mailbox_id_mod_seq_index"

	// VanishedHistoryLimit is the maximum number of vanished entries retained
	// per mailbox. Older entries are pruned; a QRESYNC request with a
	// mod-sequence older than the retained history must fail incrementally.
	VanishedHistoryLimit = 1000
)
