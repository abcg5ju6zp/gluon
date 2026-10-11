package command

import (
	"fmt"

	"github.com/ProtonMail/gluon/rfcparser"
)

type Select struct {
	Mailbox string
	Params  *SelectParams
}

func (l Select) String() string {
	return fmt.Sprintf("SELECT '%v'", l.Mailbox)
}

func (l Select) SanitizedString() string {
	return fmt.Sprintf("SELECT '%v'", sanitizeString(l.Mailbox))
}

type SelectCommandParser struct{}

func (SelectCommandParser) FromParser(p *rfcparser.Parser) (Payload, error) {
	// select          = "SELECT" SP mailbox [select-params]
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

	return &Select{
		Mailbox: mailbox.Value,
		Params:  params,
	}, nil
}
