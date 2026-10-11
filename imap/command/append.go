package command

import (
	"fmt"
	"time"

	"github.com/ProtonMail/gluon/rfcparser"
)

type Append struct {
	Mailbox  string
	Flags    []string
	DateTime time.Time
	Literal  []byte

	// LiteralSize is the declared size of the literal, including streamed uploads where
	// Literal is nil.
	LiteralSize int

	// NonSynchronous is true when the client used the RFC 7888 non-synchronizing literal
	// form ("{<n>+}").
	NonSynchronous bool

	// Upload holds the opaque, session-owned handle for a streamed literal. It is nil when
	// the literal was buffered the legacy way (Literal populated).
	Upload any
}

func (l Append) String() string {
	return fmt.Sprintf("APPEND '%v' Flags='%v' DateTime='%v' Literal=%v",
		l.Mailbox,
		l.Flags,
		l.DateTime,
		l.Literal,
	)
}

func (l Append) SanitizedString() string {
	return fmt.Sprintf("APPEND '%v' Flags='%v' DateTime='%v' Size=%v NonSync=%v",
		sanitizeString(l.Mailbox),
		l.Flags,
		l.DateTime,
		l.LiteralSize,
		l.NonSynchronous,
	)
}

func (l Append) HasDateTime() bool {
	return l.DateTime != time.Time{}
}

type AppendCommandParser struct {
	sinkFactory AppendLiteralSinkFactory
}

func (a *AppendCommandParser) FromParser(p *rfcparser.Parser) (Payload, error) {
	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after command"); err != nil {
		return nil, err
	}

	mailbox, err := ParseMailbox(p)
	if err != nil {
		return nil, err
	}

	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after mailbox"); err != nil {
		return nil, err
	}

	var appendFlags []string

	// check if we have flags.
	flagList, hasFlagList, err := TryParseFlagList(p)
	if err != nil {
		return nil, err
	} else if hasFlagList {
		appendFlags = flagList
	}

	if hasFlagList {
		if err := p.Consume(rfcparser.TokenTypeSP, "expected space after flag list"); err != nil {
			return nil, err
		}
	}

	var dateTime time.Time
	// check date time.
	if !p.Check(rfcparser.TokenTypeLCurly) {
		dt, err := ParseDateTime(p)
		if err != nil {
			return nil, err
		}

		dateTime = dt

		if err := p.Consume(rfcparser.TokenTypeSP, "expected space after flag list"); err != nil {
			return nil, err
		}
	}

	// read literal, optionally streaming it through a sink.
	literal, upload, size, nonSync, err := parseAppendLiteral(p, a.sinkFactory)
	if err != nil {
		return nil, err
	}

	return &Append{
		Mailbox:        mailbox.Value,
		Literal:        literal,
		Flags:          appendFlags,
		DateTime:       dateTime,
		LiteralSize:    size,
		NonSynchronous: nonSync,
		Upload:         upload,
	}, nil
}
