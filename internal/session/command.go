package session

import (
	"bytes"
	"context"
	"errors"

	"github.com/ProtonMail/gluon/async"
	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/response"
	"github.com/ProtonMail/gluon/logging"
	"github.com/ProtonMail/gluon/observability"
	"github.com/ProtonMail/gluon/observability/metrics"
	"github.com/ProtonMail/gluon/rfcparser"
)

type commandResult struct {
	command command.Command
	err     error
}

func (s *Session) startCommandReader(ctx context.Context) <-chan commandResult {
	cmdCh := make(chan commandResult)

	async.GoAnnotated(ctx, s.panicHandler, func(ctx context.Context) {
		defer close(cmdCh)

		tlsHeaders := [][]byte{
			{0x16, 0x03, 0x01}, // 1.0
			{0x16, 0x03, 0x02}, // 1.1
			{0x16, 0x03, 0x03}, // 1.2
			{0x16, 0x03, 0x04}, // 1.3
			{0x16, 0x00, 0x00}, // 0.0
		}

		options := []command.Option{
			command.WithLiteralContinuationCallback(func(message string) error { return response.Continuation().Send(s, message) }),
			command.WithAppendLiteralSink(s.appendLiteralSinkFactory(ctx)),
		}
		if s.disableIMAPAuthenticate {
			options = append(options, command.WithDisableIMAPAuthenticate())
		}

		parser := command.NewParser(s.scanner, options...)

		for {
			s.inputCollector.Reset()

			cmd, err := parser.Parse()
			s.logIncoming(string(s.inputCollector.Bytes()))
			if err != nil {
				var rejected *command.AppendRejectedError
				if errors.As(err, &rejected) {
					// Rejected before/during reception (size cap, quota, spool failure). The
					// tagged NO is sent before draining so a client that waits for the
					// response before sending more data cannot deadlock the reader.
					if sendErr := response.No(parser.LastParsedTag()).WithError(rejected.Cause).Send(s); sendErr != nil {
						return
					}

					if rejected.DrainLine || rejected.DrainTrailer {
						// For DrainLine the remainder of the header line is still in the
						// source; for DrainTrailer the body was drained and only the command
						// CRLF remains (its CR is tokenized; ReadBytes consumes the LF).
						if drainErr := parser.ConsumeInvalidInput(); drainErr != nil {
							return
						}
					}

					s.log.WithError(rejected.Cause).Warn("APPEND rejected")

					continue
				}

				var parserError *rfcparser.Error
				if !errors.As(err, &parserError) {
					return
				}

				if parserError.IsEOF() {
					return
				}

				if err := parser.ConsumeInvalidInput(); err != nil {
					return
				}

				bytesRead := s.inputCollector.Bytes()
				// check if we are receiving raw TLS requests, if so skip.
				for _, tlsHeader := range tlsHeaders {
					if bytes.HasPrefix(bytesRead, tlsHeader) {
						s.log.Errorf("TLS Handshake detected while not running with TLS/SSL")
						return
					}
				}

				s.log.WithError(err).WithField("type", parser.LastParsedCommand()).Error("Failed to parse IMAP command")
				observability.AddImapMetric(ctx, metrics.GenerateFailedParseIMAPCommandMetric())
			} else {
				s.log.Debug(cmd.SanitizedString())
			}

			switch c := cmd.Payload.(type) {
			case *command.StartTLS:
				// TLS needs to be handled here to ensure that next command read is over the TLS connection.
				if err = s.handleStartTLS(cmd.Tag, c); err != nil {
					s.log.WithError(err).Error("Cannot upgrade connection")
					return
				} else {
					continue
				}
			}

			select {
			case cmdCh <- commandResult{command: cmd, err: err}:
				// ...

			case <-ctx.Done():
				return
			}
		}
	}, logging.Labels{
		"Action":    "Reading commands",
		"SessionID": s.sessionID,
	})

	return cmdCh
}
