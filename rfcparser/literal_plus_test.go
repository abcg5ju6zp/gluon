package rfcparser

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParser_LiteralHeader(t *testing.T) {
	t.Run("synchronous", func(t *testing.T) {
		p := newTestParser([]byte("{5}\r\nhello"))

		size, nonSync, err := p.ParseLiteralHeader()
		require.NoError(t, err)
		require.Equal(t, 5, size)
		require.False(t, nonSync)

		continued := 0
		p.literalContinuationCb = func(string) error { continued++; return nil }

		require.NoError(t, p.ConsumeLiteralLF(nonSync))
		require.Equal(t, 1, continued)
	})

	t.Run("non synchronous", func(t *testing.T) {
		p := newTestParser([]byte("{5+}\r\nhello"))

		size, nonSync, err := p.ParseLiteralHeader()
		require.NoError(t, err)
		require.Equal(t, 5, size)
		require.True(t, nonSync)

		continued := 0
		p.literalContinuationCb = func(string) error { continued++; return nil }

		require.NoError(t, p.ConsumeLiteralLF(nonSync))
		require.Equal(t, 0, continued, "LITERAL+ must not trigger a continuation")
	})

	t.Run("rejects zero size", func(t *testing.T) {
		p := newTestParser([]byte("{0}\r\n"))
		_, _, err := p.ParseLiteralHeader()
		require.Error(t, err)
	})

	t.Run("rejects max size", func(t *testing.T) {
		p := newTestParser([]byte("{73400320+}\r\n"))
		_, _, err := p.ParseLiteralHeader()
		require.Error(t, err)
		require.ErrorContains(t, err, "70MB")
	})
}

func TestScanner_StreamLiteralBody(t *testing.T) {
	t.Run("exact bytes without overread", func(t *testing.T) {
		// Body followed by command terminator and a pipelined next command.
		input := []byte("{5+}\r\nhello\r\nA002 NOOP\r\n")
		hp := newTestParser(input)

		_, nonSync, err := hp.ParseLiteralHeader()
		require.NoError(t, err)
		require.True(t, nonSync)
		require.NoError(t, hp.ConsumeLiteralLF(true))

		var buf bytes.Buffer
		require.NoError(t, hp.StreamLiteralBody(&buf, 5))
		require.Equal(t, "hello", buf.String())
		require.Equal(t, TokenTypeCR, hp.currentToken.TType)

		require.NoError(t, hp.Consume(TokenTypeCR, "expected CR"))
		require.NoError(t, hp.Consume(TokenTypeLF, "expected LF"))
	})

	t.Run("short read returns EOF", func(t *testing.T) {
		reader, writer := io.Pipe()
		s := NewScanner(reader)

		go func() { _, _ = writer.Write([]byte("hi")); _ = writer.Close() }()

		_, err := s.advance()
		require.NoError(t, err)

		require.ErrorIs(t, s.StreamLiteralBody(io.Discard, 5), io.EOF)
	})

	t.Run("writer error propagates", func(t *testing.T) {
		s := NewScanner(bytes.NewReader([]byte("hello")))
		_, err := s.advance()
		require.NoError(t, err)
		require.Error(t, s.StreamLiteralBody(failingWriter{}, 5))
	})
}

func TestParser_ParseLiteralNonSyncBuffered(t *testing.T) {
	continued := 0
	p := NewParserWithLiteralContinuationCb(
		NewScanner(bytes.NewReader([]byte("{5+}\r\nhello"))),
		func(string) error { continued++; return nil },
	)
	require.NoError(t, p.Advance())

	literal, err := p.ParseLiteral()
	require.NoError(t, err)
	require.Equal(t, []byte("hello"), literal)
	require.Equal(t, 0, continued)
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, io.ErrClosedPipe }
