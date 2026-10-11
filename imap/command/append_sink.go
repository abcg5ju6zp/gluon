package command

import (
	"errors"
	"fmt"
	"io"

	"github.com/ProtonMail/gluon/rfcparser"
)

// AppendLiteralSink receives the streamed body of an APPEND literal. It is created by the
// session once the literal header ("{<n>}" or "{<n>+}") has been parsed and before any body
// byte is read. Exactly one of Close or Abort is called.
type AppendLiteralSink interface {
	io.Writer

	// Close is called after the whole declared body has been written successfully.
	Close() error

	// Abort is called when reading the literal failed or it was rejected after the sink had
	// already been created. It must be idempotent and release all acquired resources
	// (quota reservations, temporary objects, journal entries).
	Abort(cause error)

	// Handle returns the session-owned, opaque upload handle that is attached to the parsed
	// Append command. A nil handle means the body was buffered the legacy way (see Literal).
	Handle() any

	// Literal returns the buffered literal bytes when the sink kept the body in memory.
	// It returns nil when the body is backed by an upload handle.
	Literal() []byte
}

// AppendLiteralSinkFactory creates a sink for an APPEND literal of the given declared size.
// nonSynchronous is true when the client used the RFC 7888 "{<n>+}" form. A rejection
// returned here happens before any message can be created and must not have side effects.
type AppendLiteralSinkFactory func(size int, nonSynchronous bool) (AppendLiteralSink, error)

// AppendRejectedError signals that an APPEND literal was rejected before/during reception
// (e.g. server size cap, quota, temporary object failure). The error is delivered as a tagged
// NO response and, unlike protocol parse errors, must not tear the connection down.
//
// DrainLine asks the reader to consume the remainder of the current line (rejection before
// the literal header was fully consumed).
//
// DrainTrailer asks the reader to consume the CRLF following a fully drained literal body.
type AppendRejectedError struct {
	Cause       error
	DrainLine   bool
	DrainTrailer bool
}

func (e *AppendRejectedError) Error() string {
	return e.Cause.Error()
}

func (e *AppendRejectedError) Unwrap() error {
	return e.Cause
}

// parseAppendLiteral reads the APPEND literal, streaming it through sinkFactory when one is
// configured and falling back to the buffered behaviour otherwise. It returns the buffered
// bytes (small/legacy path), the opaque sink handle, the declared size and the non-synchronous
// flag.
func parseAppendLiteral(
	p *rfcparser.Parser,
	factory AppendLiteralSinkFactory,
) (literal []byte, upload any, size int, nonSynchronous bool, err error) {
	size, nonSynchronous, err = p.ParseLiteralHeader()
	if err != nil {
		// With a sink configured, an over-large declared size is an operational rejection
		// (tagged NO, connection stays alive) rather than a fatal parse error.
		if factory != nil && errors.Is(err, rfcparser.ErrLiteralTooLarge) {
			return nil, nil, size, nonSynchronous, &AppendRejectedError{Cause: err, DrainLine: true}
		}

		return nil, nil, size, nonSynchronous, err
	}

	if factory == nil {
		if err := p.ConsumeLiteralLF(nonSynchronous); err != nil {
			return nil, nil, size, nonSynchronous, err
		}

		literal, err = p.ReadLiteralBody(size)

		return literal, nil, size, nonSynchronous, err
	}

	sink, beginErr := factory(size, nonSynchronous)
	if beginErr != nil {
		if nonSynchronous {
			// The client is allowed to stream the body immediately. Consume the pending LF
			// and drain the declared body so the stream stays in sync. The command trailer
			// CRLF is consumed by the reader after the NO response is sent.
			if err := p.ConsumeLiteralLF(true); err != nil {
				return nil, nil, size, true, err
			}

			if err := p.StreamLiteralBody(io.Discard, size); err != nil {
				return nil, nil, size, true, err
			}

			return nil, nil, size, nonSynchronous, &AppendRejectedError{Cause: beginErr, DrainTrailer: true}
		}

		// For synchronous literals the header CR has been consumed and the LF is already
		// the current token; a compliant client is still waiting for the continuation.
		_ = p.Consume(rfcparser.TokenTypeLF, "expected LF")

		return nil, nil, size, nonSynchronous, &AppendRejectedError{Cause: beginErr}
	}

	if err := p.ConsumeLiteralLF(nonSynchronous); err != nil {
		sink.Abort(err)
		return nil, nil, size, nonSynchronous, err
	}

	if err := p.StreamLiteralBody(sink, size); err != nil {
		sink.Abort(err)
		return nil, nil, size, nonSynchronous, fmt.Errorf("failed to read append literal body: %w", err)
	}

	if err := sink.Close(); err != nil {
		sink.Abort(err)
		return nil, nil, size, nonSynchronous, fmt.Errorf("failed to finalize append literal: %w", err)
	}

	return sink.Literal(), sink.Handle(), size, nonSynchronous, nil
}
