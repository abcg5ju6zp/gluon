package command

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/ProtonMail/gluon/rfcparser"
	"github.com/stretchr/testify/require"
)

type testSink struct {
	buf    bytes.Buffer
	handle any
	abort  bool
}

func (s *testSink) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *testSink) Close() error                { return nil }
func (s *testSink) Abort(_ error)               { s.abort = true }
func (s *testSink) Handle() any                 { return s.handle }
func (s *testSink) Literal() []byte             { return nil }

func parseWithSink(t *testing.T, input []byte, factory AppendLiteralSinkFactory, continuation func(string) error) Command {
	t.Helper()

	s := rfcparser.NewScanner(bytes.NewReader(input))
	opts := []Option{WithAppendLiteralSink(factory)}
	if continuation != nil {
		opts = append(opts, WithLiteralContinuationCallback(continuation))
	}

	p := NewParser(s, opts...)

	cmd, err := p.Parse()
	require.NoError(t, err)

	return cmd
}

func TestAppendLiteralSink_NonSyncDoesNotContinue(t *testing.T) {
	var got *testSink
	continued := 0

	factory := func(size int, nonSync bool) (AppendLiteralSink, error) {
		require.Equal(t, 5, size)
		require.True(t, nonSync)
		got = &testSink{handle: "upload-x"}
		return got, nil
	}

	cmd := parseWithSink(t, []byte("A001 APPEND INBOX {5+}\r\nhello\r\n"), factory, func(string) error {
		continued++
		return nil
	})

	appendCmd := cmd.Payload.(*Append)
	require.Equal(t, 5, appendCmd.LiteralSize)
	require.True(t, appendCmd.NonSynchronous)
	require.Equal(t, "upload-x", appendCmd.Upload)
	require.Equal(t, "hello", got.buf.String())
	require.Equal(t, 0, continued)
}

func TestAppendLiteralSink_SyncContinues(t *testing.T) {
	var got *testSink
	continued := 0

	factory := func(size int, nonSync bool) (AppendLiteralSink, error) {
		require.False(t, nonSync)
		got = &testSink{}
		return got, nil
	}

	cmd := parseWithSink(t, []byte("A001 APPEND INBOX {5}\r\nhello\r\n"), factory, func(string) error {
		continued++
		return nil
	})

	appendCmd := cmd.Payload.(*Append)
	require.False(t, appendCmd.NonSynchronous)
	require.Equal(t, "hello", got.buf.String())
	require.Equal(t, 1, continued)
}

func TestAppendLiteralSink_BeginFailureNonSyncDrainsBody(t *testing.T) {
	reject := errors.New("quota exceeded")
	factory := func(int, bool) (AppendLiteralSink, error) { return nil, reject }

	// Body plus a second, valid command: the rejected literal body must be drained so the
	// next command still parses.
	input := []byte("A001 APPEND INBOX {5+}\r\nhello\r\nA002 NOOP\r\n")
	s := rfcparser.NewScanner(bytes.NewReader(input))
	p := NewParser(s, WithAppendLiteralSink(factory))

	_, err := p.Parse()
	require.Error(t, err)
	var rejected *AppendRejectedError
	require.ErrorAs(t, err, &rejected)
	require.ErrorIs(t, err, reject)
	require.True(t, rejected.DrainTrailer)

	// Body drained; the command CRLF trailer's LF remains in the source.
	require.NoError(t, p.ConsumeInvalidInput())

	cmd, err := p.Parse()
	require.NoError(t, err)
	require.IsType(t, &Noop{}, cmd.Payload)
}

func TestAppendLiteralSink_BeginFailureSyncConsumesLF(t *testing.T) {
	reject := errors.New("spool down")
	factory := func(int, bool) (AppendLiteralSink, error) { return nil, reject }

	// After the rejected command, a next command is available; the pending header LF must
	// have been consumed for the parser to resync.
	s := rfcparser.NewScanner(bytes.NewReader([]byte("A001 APPEND INBOX {5}\r\nA002 NOOP\r\n")))
	p := NewParser(s, WithAppendLiteralSink(factory))

	_, err := p.Parse()
	require.Error(t, err)
	var rejected *AppendRejectedError
	require.ErrorAs(t, err, &rejected)

	cmd, err := p.Parse()
	require.NoError(t, err)
	require.IsType(t, &Noop{}, cmd.Payload)
}

func TestAppendLiteralSink_StreamWriteErrorAborts(t *testing.T) {
	failing := &failingAppendSink{}
	factory := func(int, bool) (AppendLiteralSink, error) { return failing, nil }

	s := rfcparser.NewScanner(bytes.NewReader([]byte("A001 APPEND INBOX {5+}\r\nhello\r\n")))
	p := NewParser(s, WithAppendLiteralSink(factory))

	_, err := p.Parse()
	require.Error(t, err)
	require.True(t, failing.aborted)
}

func TestAppendLiteralSink_NilFactoryNonSyncBuffered(t *testing.T) {
	s := rfcparser.NewScanner(bytes.NewReader([]byte("A001 APPEND INBOX {5+}\r\nhello\r\n")))
	p := NewParser(s)

	cmd, err := p.Parse()
	require.NoError(t, err)

	appendCmd := cmd.Payload.(*Append)
	require.Equal(t, []byte("hello"), appendCmd.Literal)
	require.Nil(t, appendCmd.Upload)
	require.True(t, appendCmd.NonSynchronous)
}

type failingAppendSink struct {
	aborted bool
}

func (f *failingAppendSink) Write(_ []byte) (int, error) { return 0, io.ErrClosedPipe }
func (f *failingAppendSink) Close() error                { return nil }
func (f *failingAppendSink) Abort(_ error)               { f.aborted = true }
func (f *failingAppendSink) Handle() any                 { return nil }
func (f *failingAppendSink) Literal() []byte             { return nil }
