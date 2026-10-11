package db

import "context"

type ReadOnly interface {
	MailboxReadOps
	MessageReadOps
	SubscriptionReadOps
	AppendJournalReadOps

	// GetConnectorSettings returns true if no previous setting was ever stored before.
	GetConnectorSettings(ctx context.Context) (string, bool, error)
}

type Transaction interface {
	ReadOnly
	MailboxWriteOps
	MessageWriteOps
	SubscriptionWriteOps
	AppendJournalWriteOps

	StoreConnectorSettings(ctx context.Context, settings string) error
}
