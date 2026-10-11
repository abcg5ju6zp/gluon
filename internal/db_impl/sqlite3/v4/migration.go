package v4

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/db_impl/sqlite3/utils"
	v1 "github.com/ProtonMail/gluon/internal/db_impl/sqlite3/v1"
	"github.com/ProtonMail/gluon/internal/thread"
	"github.com/bradenaw/juniper/xslices"
	"github.com/google/uuid"
)

type Migration struct{}

// objectIDNamespace is a fixed, arbitrary namespace used to derive deterministic identifiers for
// pre-existing rows during the backfill. The derivation is pure so re-running the migration logic
// always produces the same values.
var objectIDNamespace = uuid.MustParse("3f1e9c44-7b0a-4d6a-8f2c-1e6b9a5d7042")

func deterministicID(purpose string, key string) string {
	return uuid.NewSHA1(objectIDNamespace, []byte(purpose+key)).String()
}

type envelopeMessage struct {
	id       imap.InternalMessageID
	envelope string
}

func (m Migration) Run(ctx context.Context, tx utils.QueryWrapper, _ imap.UIDValidityGenerator) error {
	// 1. Persistent MAILBOXID column on mailboxes, deterministically derived from the immutable
	// internal mailbox id so that the value is stable across restarts and never changes even if
	// the migration is re-applied.
	if err := exec(ctx, tx, fmt.Sprintf("ALTER TABLE %v ADD COLUMN %v text",
		v1.MailboxesTableName, MailboxesFieldObjectID)); err != nil {
		return fmt.Errorf("failed to add mailbox object id column: %w", err)
	}

	mailboxIDs, err := utils.MapQueryRows[imap.InternalMailboxID](ctx, tx,
		fmt.Sprintf("SELECT `%v` FROM %v", v1.MailboxesFieldID, v1.MailboxesTableName))
	if err != nil {
		return fmt.Errorf("failed to load mailbox ids: %w", err)
	}

	for _, id := range mailboxIDs {
		objectID := imap.MailboxObjectID(deterministicID("mailbox-object-id:", id.String()))

		if err := exec(ctx, tx,
			fmt.Sprintf("UPDATE %v SET `%v` = ? WHERE `%v` = ?",
				v1.MailboxesTableName, MailboxesFieldObjectID, v1.MailboxesFieldID),
			objectID, id); err != nil {
			return fmt.Errorf("failed to backfill mailbox object id: %w", err)
		}
	}

	if err := exec(ctx, tx,
		fmt.Sprintf("CREATE UNIQUE INDEX %v ON %v (%v)",
			MailboxObjectIDIndex, v1.MailboxesTableName, MailboxesFieldObjectID)); err != nil {
		return fmt.Errorf("failed to create mailbox object id index: %w", err)
	}

	// 2. Persistent EMAILID and THREADID columns on messages. Existing rows deterministically
	// reuse their existing internal message UUID as EMAILID (it is already a globally unique
	// random id), so the identity clients observe never changes after upgrading.
	if err := exec(ctx, tx, fmt.Sprintf("ALTER TABLE %v ADD COLUMN %v text",
		v1.MessagesTableName, MessagesFieldObjectID)); err != nil {
		return fmt.Errorf("failed to add message object id column: %w", err)
	}

	if err := exec(ctx, tx, fmt.Sprintf("ALTER TABLE %v ADD COLUMN %v text",
		v1.MessagesTableName, MessagesFieldThreadID)); err != nil {
		return fmt.Errorf("failed to add message thread id column: %w", err)
	}

	if err := exec(ctx, tx, fmt.Sprintf("UPDATE %v SET `%v` = `%v`",
		v1.MessagesTableName, MessagesFieldObjectID, v1.MessagesFieldID)); err != nil {
		return fmt.Errorf("failed to backfill message object ids: %w", err)
	}

	// 3. Persistent threading tokens (Message-ID / In-Reply-To; References is not part of the
	// stored IMAP envelope, so threading based on References is built up going forward).
	if err := exec(ctx, tx, fmt.Sprintf(
		"CREATE TABLE `%[1]v` ("+
			"`%[2]v` uuid NOT NULL, "+
			"`%[3]v` text NOT NULL, "+
			"CONSTRAINT `%[1]v_pkey` PRIMARY KEY (`%[3]v`, `%[2]v`), "+
			"CONSTRAINT `%[1]v_messages` FOREIGN KEY (`%[2]v`) REFERENCES `%[4]v` (`%[5]v`) ON DELETE CASCADE)",
		ThreadTokensTableName, ThreadTokensFieldMessageID, ThreadTokensFieldToken,
		v1.MessagesTableName, v1.MessagesFieldID)); err != nil {
		return fmt.Errorf("failed to create thread tokens table: %w", err)
	}

	messages, err := utils.MapQueryRowsFn(ctx, tx,
		fmt.Sprintf("SELECT `%v`, `%v` FROM %v",
			v1.MessagesFieldID, v1.MessagesFieldEnvelope, v1.MessagesTableName),
		func(scanner utils.RowScanner) (envelopeMessage, error) {
			var msg envelopeMessage
			if err := scanner.Scan(&msg.id, &msg.envelope); err != nil {
				return envelopeMessage{}, err
			}

			return msg, nil
		})
	if err != nil {
		return fmt.Errorf("failed to load messages for thread backfill: %w", err)
	}

	type tokenRow struct {
		messageID imap.InternalMessageID
		token     string
	}

	tokenRows := make([]tokenRow, 0, len(messages))

	graph := thread.NewGraph(len(messages))

	for _, msg := range messages {
		inReplyTo, messageID := parseEnvelopeThreadFields(msg.envelope)

		tokens := thread.ExtractTokensFromValues(inReplyTo, messageID)
		if len(tokens) == 0 {
			// Deterministic isolated token for messages with no usable ids; mirrors runtime
			// behavior and keeps the derivation repeatable.
			tokens = []string{"gluon-synthetic-thread-token:" + msg.id.String()}
		}

		for _, token := range tokens {
			tokenRows = append(tokenRows, tokenRow{messageID: msg.id, token: token})
		}

		graph.Add(msg.id.String(), tokens)
	}

	for _, chunk := range xslices.Chunk(tokenRows, 1000) {
		query := fmt.Sprintf("INSERT INTO %v (`%v`, `%v`) VALUES %v",
			ThreadTokensTableName, ThreadTokensFieldMessageID, ThreadTokensFieldToken,
			strings.Join(xslices.Repeat("(?,?)", len(chunk)), ","))

		args := make([]any, 0, 2*len(chunk))
		for _, row := range chunk {
			args = append(args, row.messageID, row.token)
		}

		if err := exec(ctx, tx, query, args...); err != nil {
			return fmt.Errorf("failed to backfill thread tokens: %w", err)
		}
	}

	// 4. Derive persistent THREADID for every component. The id is deterministically derived from
	// the lexicographically smallest internal message id in the component, so it never changes on
	// repeat migration.
	for _, component := range graph.Components() {
		memberIDs := component.IDs

		minID := memberIDs[0]
		for _, id := range memberIDs[1:] {
			if id < minID {
				minID = id
			}
		}

		threadID := imap.ThreadID(deterministicID("thread:", minID))

		for _, member := range memberIDs {
			internalID, err := imap.InternalMessageIDFromString(member)
			if err != nil {
				return fmt.Errorf("failed to parse backfilled message id %v: %w", member, err)
			}

			if err := exec(ctx, tx,
				fmt.Sprintf("UPDATE %v SET `%v` = ? WHERE `%v` = ?",
					v1.MessagesTableName, MessagesFieldThreadID, v1.MessagesFieldID),
				threadID, internalID); err != nil {
				return fmt.Errorf("failed to backfill thread id: %w", err)
			}
		}
	}

	if err := exec(ctx, tx,
		fmt.Sprintf("CREATE UNIQUE INDEX %v ON %v (%v)",
			MessageObjectIDIndex, v1.MessagesTableName, MessagesFieldObjectID)); err != nil {
		return fmt.Errorf("failed to create message object id index: %w", err)
	}

	if err := exec(ctx, tx,
		fmt.Sprintf("CREATE INDEX %v ON %v (%v)",
			MessageThreadIDIndex, v1.MessagesTableName, MessagesFieldThreadID)); err != nil {
		return fmt.Errorf("failed to create thread id index: %w", err)
	}

	if err := exec(ctx, tx,
		fmt.Sprintf("CREATE INDEX %v ON %v (%v)",
			ThreadTokensMessageIDIndex, ThreadTokensTableName, ThreadTokensFieldMessageID)); err != nil {
		return fmt.Errorf("failed to create thread token message id index: %w", err)
	}

	if err := exec(ctx, tx,
		fmt.Sprintf("CREATE INDEX %v ON %v (%v)",
			ThreadTokensTokenIndex, ThreadTokensTableName, ThreadTokensFieldToken)); err != nil {
		return fmt.Errorf("failed to create thread token index: %w", err)
	}

	return nil
}

func exec(ctx context.Context, tx utils.QueryWrapper, query string, args ...any) error {
	_, err := utils.ExecQuery(ctx, tx, query, args...)

	return err
}

// parseEnvelopeThreadFields extracts the In-Reply-To and Message-ID fields (positions 9 and 10)
// from a serialized IMAP envelope. It is paren/quote aware so nested address lists do not confuse
// field boundaries.
func parseEnvelopeThreadFields(envelope string) (inReplyTo string, messageID string) {
	fields := parseEnvelopeFields(envelope)

	// Envelope: date, subject, from, sender, reply-to, to, cc, bcc, in-reply-to, message-id.
	const (
		inReplyToField = 8
		messageIDField = 9
	)

	if len(fields) > inReplyToField {
		inReplyTo = unquoteEnvelopeString(fields[inReplyToField])
	}

	if len(fields) > messageIDField {
		messageID = unquoteEnvelopeString(fields[messageIDField])
	}

	return inReplyTo, messageID
}

func parseEnvelopeFields(envelope string) []string {
	var fields []string

	i := 0
	for i < len(envelope) && envelope[i] != '(' {
		i++
	}

	if i >= len(envelope) {
		return nil
	}

	i++ // skip '('
	depth := 1

	var current strings.Builder
	currentStarted := false

	for i < len(envelope) && depth > 0 {
		switch c := envelope[i]; c {
		case '(':
			depth++
			current.WriteByte(c)
			currentStarted = true
			i++
		case ')':
			depth--
			if depth == 0 {
				if currentStarted {
					fields = append(fields, current.String())
				}

				return fields
			}

			current.WriteByte(c)
			i++
		case '"':
			// Quoted string at top level is a field; consume it verbatim (escapes included).
			if depth == 1 && !currentStarted {
				j := i + 1
				for j < len(envelope) {
					if envelope[j] == '\\' {
						j += 2
						continue
					}

					if envelope[j] == '"' {
						break
					}

					j++
				}

				fields = append(fields, envelope[i:j+1])
				i = j + 1
			} else {
				current.WriteByte(c)
				i++
			}
		case ' ':
			if depth == 1 {
				if currentStarted {
					fields = append(fields, current.String())
					current.Reset()
					currentStarted = false
				}

				i++
			} else {
				current.WriteByte(c)
				i++
			}
		default:
			if depth == 1 && !currentStarted {
				// Bare atom (NIL, number).
				j := i
				for j < len(envelope) && envelope[j] != ' ' && envelope[j] != ')' {
					j++
				}

				fields = append(fields, envelope[i:j])
				i = j
			} else {
				current.WriteByte(c)
				currentStarted = true
				i++
			}
		}
	}

	return fields
}

func unquoteEnvelopeString(field string) string {
	if field == "NIL" || len(field) == 0 {
		return ""
	}

	value, err := strconv.Unquote(field)
	if err != nil {
		return ""
	}

	return value
}
