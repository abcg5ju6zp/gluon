package command

import (
	"fmt"

	"github.com/ProtonMail/gluon/rfcparser"
)

// SelectParams holds the optional RFC 7162 SELECT/EXAMINE parameters.
type SelectParams struct {
	// CondStore is true when the CONDSTORE select parameter was present.
	CondStore bool

	// QResync holds the QRESYNC parameter when present.
	QResync *QResyncParam
}

// QResyncParam is the parsed QRESYNC select parameter:
// "QRESYNC" SP "(" uidvalidity SP mod-sequence [SP known-uids] [SP seq-match-data] ")".
type QResyncParam struct {
	UIDValidity   uint64
	ModSeq        uint64
	KnownUIDs     []SeqRange
	HasKnownUIDs  bool
}

// parseSelectParams parses the optional "(" select-param *(SP select-param) ")"
// trailing a SELECT/EXAMINE mailbox name.
func parseSelectParams(p *rfcparser.Parser) (*SelectParams, error) {
	ok, err := p.Matches(rfcparser.TokenTypeSP)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, nil
	}

	if !p.Check(rfcparser.TokenTypeLParen) {
		return nil, p.MakeError("expected ( to start SELECT parameters")
	}

	if err := p.Consume(rfcparser.TokenTypeLParen, "expected ( to start SELECT parameters"); err != nil {
		return nil, err
	}

	params := &SelectParams{}

	// First parameter.
	if err := parseOneSelectParam(p, params); err != nil {
		return nil, err
	}

	for {
		ok, err := p.Matches(rfcparser.TokenTypeSP)
		if err != nil {
			return nil, err
		}

		if !ok {
			break
		}

		if err := parseOneSelectParam(p, params); err != nil {
			return nil, err
		}
	}

	if err := p.Consume(rfcparser.TokenTypeRParen, "expected ) to end SELECT parameters"); err != nil {
		return nil, err
	}

	return params, nil
}

func parseOneSelectParam(p *rfcparser.Parser, params *SelectParams) error {
	name, err := p.CollectBytesWhileMatches(rfcparser.TokenTypeChar)
	if err != nil {
		return err
	}

	switch name.IntoString().ToLower().Value {
	case "condstore":
		params.CondStore = true

	case "qresync":
		if params.QResync != nil {
			return p.MakeError("QRESYNC parameter can only be provided once")
		}

		qresync, err := parseQResyncParam(p)
		if err != nil {
			return err
		}

		params.QResync = qresync

	default:
		return p.MakeErrorAtOffset(fmt.Sprintf("unknown SELECT parameter '%v'", name.Value), name.Offset)
	}

	return nil
}

func parseQResyncParam(p *rfcparser.Parser) (*QResyncParam, error) {
	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after QRESYNC"); err != nil {
		return nil, err
	}

	if err := p.Consume(rfcparser.TokenTypeLParen, "expected ( to start QRESYNC parameters"); err != nil {
		return nil, err
	}

	uidValidity, err := p.ParseNumber()
	if err != nil {
		return nil, err
	}

	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after QRESYNC UIDVALIDITY"); err != nil {
		return nil, err
	}

	modSeq, err := p.ParseNumber()
	if err != nil {
		return nil, err
	}

	param := &QResyncParam{
		UIDValidity: uint64(uidValidity),
		ModSeq:      uint64(modSeq),
	}

	ok, err := p.Matches(rfcparser.TokenTypeSP)
	if err != nil {
		return nil, err
	}

	if ok {
		// Either the known-uids sequence set or the seq-match-data list.
		if p.Check(rfcparser.TokenTypeLParen) {
			// seq-match-data = "(" known-sequence-set SP known-uid-set ")".
			// Search result resynchronization is not supported; parse and ignore.
			if err := parseSeqMatchData(p); err != nil {
				return nil, err
			}
		} else {
			knownUIDs, err := ParseSeqSet(p)
			if err != nil {
				return nil, err
			}

			param.KnownUIDs = knownUIDs
			param.HasKnownUIDs = true

			if ok, err := p.Matches(rfcparser.TokenTypeSP); err != nil {
				return nil, err
			} else if ok {
				if err := parseSeqMatchData(p); err != nil {
					return nil, err
				}
			}
		}
	}

	if err := p.Consume(rfcparser.TokenTypeRParen, "expected ) to end QRESYNC parameters"); err != nil {
		return nil, err
	}

	return param, nil
}

func parseSeqMatchData(p *rfcparser.Parser) error {
	if err := p.Consume(rfcparser.TokenTypeLParen, "expected ( to start seq-match-data"); err != nil {
		return err
	}

	if _, err := ParseSeqSet(p); err != nil {
		return err
	}

	if err := p.Consume(rfcparser.TokenTypeSP, "expected space in seq-match-data"); err != nil {
		return err
	}

	if _, err := ParseSeqSet(p); err != nil {
		return err
	}

	return p.Consume(rfcparser.TokenTypeRParen, "expected ) to end seq-match-data")
}
