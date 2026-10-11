package session

import (
	"bytes"
	"context"
	"sync"
	"time"

	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/response"
	"github.com/ProtonMail/gluon/internal/state"
)

// appendSink streams an APPEND body into a state.Upload (spool object + quota reservation +
// receiving journal row). It is created on the command reader goroutine and ownership passes
// to the command handler through the parsed command.
type appendSink struct {
	session *Session
	upload  state.Upload
	once    sync.Once
}

func (s *appendSink) Write(p []byte) (int, error) {
	return s.upload.Write(p)
}

func (s *appendSink) Close() error {
	if err := s.upload.CloseReception(); err != nil {
		return err
	}

	s.disarm()

	return nil
}

func (s *appendSink) Abort(cause error) {
	s.once.Do(func() {
		s.session.disarmAppendReadDeadline()
		s.session.inputCollector.SetCollecting(true)
		s.upload.Abort(context.Background())
	})
}

func (s *appendSink) Handle() any {
	return s.upload
}

func (s *appendSink) Literal() []byte {
	return nil
}

// disarm is invoked after a successful Close so subsequent reads (command framing, next
// commands) are not bound by the append read timeout.
func (s *appendSink) disarm() {
	s.session.disarmAppendReadDeadline()
	s.session.inputCollector.SetCollecting(true)
}

// bufferedAppendSink keeps the legacy fully-buffered behaviour. It is used before the session
// is authenticated (the handler rejects the APPEND anyway) when no quota/spool context exists.
type bufferedAppendSink struct {
	buf bytes.Buffer
}

func (b *bufferedAppendSink) Write(p []byte) (int, error) {
	return b.buf.Write(p)
}

func (b *bufferedAppendSink) Close() error { return nil }

func (b *bufferedAppendSink) Abort(_ error) {}

func (b *bufferedAppendSink) Handle() any { return nil }

func (b *bufferedAppendSink) Literal() []byte {
	return b.buf.Bytes()
}

// appendLiteralSinkFactory builds the sink used by the command parser for each APPEND
// literal. It runs on the command reader goroutine.
func (s *Session) appendLiteralSinkFactory(ctx context.Context) command.AppendLiteralSinkFactory {
	return func(size int, _ bool) (command.AppendLiteralSink, error) {
		s.userLock.Lock()
		stateRef := s.state
		s.userLock.Unlock()

		if stateRef == nil {
			return &bufferedAppendSink{}, nil
		}

		upload, err := stateRef.BeginAppendUpload(ctx, int64(size))
		if err != nil {
			return nil, err
		}

		if s.appendReadTimeout > 0 {
			if deadlineErr := s.conn.SetReadDeadline(time.Now().Add(s.appendReadTimeout)); deadlineErr != nil {
				upload.Abort(ctx)
				return nil, deadlineErr
			}
		}

		s.inputCollector.SetCollecting(false)

		return &appendSink{session: s, upload: upload}, nil
	}
}

func (s *Session) disarmAppendReadDeadline() {
	if s.appendReadTimeout > 0 {
		_ = s.conn.SetReadDeadline(time.Time{})
	}
}

// afterSendResponse wraps a response and runs after once Send completed. It is used to delete
// the append spool object only after the tagged response was confirmed written.
type afterSendResponse struct {
	inner response.Response
	after func(error)
}

func (r *afterSendResponse) Send(s response.Session) error {
	err := r.inner.Send(s)
	r.after(err)

	return err
}

func (r *afterSendResponse) String() string {
	return r.inner.String()
}
