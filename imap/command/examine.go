package command

import (
	"fmt"

	"github.com/ProtonMail/gluon/rfcparser"
)

type Examine struct {
	Mailbox string
	Params  *SelectParams
}

func (l Examine) String() string {
	return fmt.Sprintf("EXAMINE '%v'", l.Mailbox)
}

func (l Examine) SanitizedString() string {
	return fmt.Sprintf("EXAMINE '%v'", sanitizeString(l.Mailbox))
}

type ExamineCommandParser struct{}

func (ExamineCommandParser) FromParser(p *rfcparser.Parser) (Payload, error) {
	// examine          = "EXAMINE" SP mailbox [select-params]
	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after command"); err != nil {
		return nil, err
	}

	mailbox, err := ParseMailbox(p)
	if err != nil {
		return nil, err
	}

	params, err := parseSelectParams(p)
	if err != nil {
		return nil, err
	}

	return &Examine{
		Mailbox: mailbox.Value,
		Params:  params,
	}, nil
}
