package connector

import (
	"context"
	"io"
	"time"

	"github.com/ProtonMail/gluon/imap"
)

// StreamingDummy wraps a Dummy connector to additionally implement StreamingConnector. It is
// opt-in: a plain Dummy keeps the buffered CreateMessage path so that test doubles overriding
// CreateMessage on an embedding struct remain effective.
type StreamingDummy struct {
	*Dummy
}

func NewStreamingDummy(dummy *Dummy) *StreamingDummy {
	return &StreamingDummy{Dummy: dummy}
}

func (s *StreamingDummy) CreateMessageFromReader(
	ctx context.Context,
	cache IMAPStateWrite,
	mboxID imap.MailboxID,
	r io.Reader,
	_ int64,
	flags imap.FlagSet,
	date time.Time,
) (imap.Message, []byte, error) {
	literal, err := io.ReadAll(r)
	if err != nil {
		return imap.Message{}, nil, err
	}

	return s.CreateMessage(ctx, cache, mboxID, literal, flags, date)
}
