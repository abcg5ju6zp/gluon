package command

import (
	"fmt"

	"github.com/ProtonMail/gluon/rfcparser"
)

// Enable implements RFC 5161's ENABLE command, used by clients to enable
// extensions such as CONDSTORE and QRESYNC after authentication.
type Enable struct {
	Capabilities []string
}

func (l Enable) String() string {
	return fmt.Sprintf("ENABLE %v", l.Capabilities)
}

func (l Enable) SanitizedString() string {
	return l.String()
}

type EnableCommandParser struct{}

func (EnableCommandParser) FromParser(p *rfcparser.Parser) (Payload, error) {
	// enable          = "ENABLE" 1*(SP capability)
	var capabilities []string

	for {
		if ok, err := p.Matches(rfcparser.TokenTypeSP); err != nil {
			return nil, err
		} else if !ok {
			break
		}

		capability, err := p.CollectBytesWhileMatches(rfcparser.TokenTypeChar)
		if err != nil {
			return nil, err
		}

		capabilities = append(capabilities, capability.IntoString().Value)
	}

	if len(capabilities) == 0 {
		return nil, p.MakeError("expected at least one capability after ENABLE")
	}

	return &Enable{Capabilities: capabilities}, nil
}
