package connector

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/ProtonMail/gluon/imap"
	"github.com/stretchr/testify/require"
)

func newTestDummy(t *testing.T) *Dummy {
	t.Helper()

	d := NewDummy([]string{"user"}, []byte("pass"), time.Hour, imap.NewFlagSet(), imap.NewFlagSet(), imap.NewFlagSet())
	t.Cleanup(func() { _ = d.Close(context.Background()) })

	return d
}

func TestDummy_ContentAddressedAppendsAreIdempotent(t *testing.T) {
	d := newTestDummy(t)
	d.SetContentAddressedAppends(true)

	var (
		ctx     = context.Background()
		literal = []byte("From: a@b.c\r\nSubject: draft\r\n\r\nhello")
		flags   = imap.NewFlagSet()
		date    = time.Unix(1_600_000_000, 0)
		calls   []imap.Message
	)

	for i := 0; i < 3; i++ {
		msg, newLiteral, err := d.CreateMessage(ctx, nil, "0", literal, flags, date)
		require.NoError(t, err)
		require.Equal(t, literal, newLiteral)
		calls = append(calls, msg)
	}

	require.Equal(t, calls[0].ID, calls[1].ID)
	require.Equal(t, calls[1].ID, calls[2].ID)
	require.Equal(t, int64(1), d.GetAppendCreateCount())

	// Different flags produce a different remote identity.
	msgOther, _, err := d.CreateMessage(ctx, nil, "0", literal, imap.NewFlagSet(`\Seen`), date)
	require.NoError(t, err)
	require.NotEqual(t, calls[0].ID, msgOther.ID)
}

func TestDummy_GetQuotaBytes(t *testing.T) {
	d := newTestDummy(t)

	limit, err := d.GetQuotaBytes(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(0), limit, "default is unlimited")

	d.SetQuotaBytes(4096)
	limit, err = d.GetQuotaBytes(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(4096), limit)
}

func TestStreamingDummy_CreateMessageFromReader(t *testing.T) {
	base := newTestDummy(t)
	d := NewStreamingDummy(base)

	literal := []byte("From: a@b.c\r\n\r\nbody")

	msg, newLiteral, err := d.CreateMessageFromReader(
		context.Background(), nil, "0", bytes.NewReader(literal), int64(len(literal)), imap.NewFlagSet(), time.Now(),
	)
	require.NoError(t, err)
	require.Equal(t, literal, newLiteral)
	require.NotEmpty(t, msg.ID)

	got, err := d.GetMessageLiteral(context.Background(), msg.ID)
	require.NoError(t, err)
	require.Equal(t, literal, got)

	// Implements the streaming connector interface.
	var _ interface {
		CreateMessageFromReader(context.Context, IMAPStateWrite, imap.MailboxID, io.Reader, int64, imap.FlagSet, time.Time) (imap.Message, []byte, error)
	} = d
}
