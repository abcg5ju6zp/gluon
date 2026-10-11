package command

import (
	"fmt"

	"github.com/ProtonMail/gluon/rfcparser"
)

type StoreAction int

const (
	StoreActionAddFlags StoreAction = iota
	StoreActionRemFlags
	StoreActionSetFlags
)

func (s StoreAction) String() string {
	switch s {
	case StoreActionAddFlags:
		return "+FLAGS"
	case StoreActionRemFlags:
		return "+FLAGS"
	case StoreActionSetFlags:
		return "FLAGS"
	default:
		return "UNKNOWN"
	}
}

type Store struct {
	SeqSet []SeqRange
	Action StoreAction
	Flags  []string
	Silent bool

	// UnchangedSince is non-nil for a CONDSTORE conditional STORE
	// ((UNCHANGEDSINCE mod-sequence)).
	UnchangedSince *uint64
}

func (s Store) String() string {
	silentStr := ""
	if s.Silent {
		silentStr = ".SILENT"
	}

	return fmt.Sprintf("STORE %v %v%v %v", s.SeqSet, s.Action.String(), silentStr, s.Flags)
}

func (s Store) SanitizedString() string {
	return s.String()
}

type StoreCommandParser struct{}

func (StoreCommandParser) FromParser(p *rfcparser.Parser) (Payload, error) {
	//nolint:dupword
	// store           = "STORE" SP sequence-set SP store-att-flags
	// store-att-flags = condstore-flags / (["+" / "-"] "FLAGS" [".SILENT"]) SP
	//                  (flag-list / (flag *(SP flag)))
	// condstore-flags = "(" UNCHANGEDSINCE mod-sequence-value ")" SP
	//                  (["+" / "-"] "FLAGS" [".SILENT"]) SP (flag-list / ...)
	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after command"); err != nil {
		return nil, err
	}

	seqSet, err := ParseSeqSet(p)
	if err != nil {
		return nil, err
	}

	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after sequence set"); err != nil {
		return nil, err
	}

	var unchangedSince *uint64

	// Optional CONDSTORE UNCHANGEDSINCE predicate.
	if p.Check(rfcparser.TokenTypeLParen) {
		if err := p.Consume(rfcparser.TokenTypeLParen, "expected ( for UNCHANGEDSINCE"); err != nil {
			return nil, err
		}

		if err := p.ConsumeBytesFold('U', 'N', 'C', 'H', 'A', 'N', 'G', 'E', 'D', 'S', 'I', 'N', 'C', 'E'); err != nil {
			return nil, err
		}

		if err := p.Consume(rfcparser.TokenTypeSP, "expected space after UNCHANGEDSINCE"); err != nil {
			return nil, err
		}

		modSeq, err := p.ParseNumber()
		if err != nil {
			return nil, err
		}

		modSeq64 := uint64(modSeq)
		unchangedSince = &modSeq64

		if err := p.Consume(rfcparser.TokenTypeRParen, "expected ) after UNCHANGEDSINCE value"); err != nil {
			return nil, err
		}

		if err := p.Consume(rfcparser.TokenTypeSP, "expected space after UNCHANGEDSINCE predicate"); err != nil {
			return nil, err
		}
	}
	var action StoreAction

	if ok, err := p.Matches(rfcparser.TokenTypePlus); err != nil {
		return nil, err
	} else if !ok {
		if ok, err := p.Matches(rfcparser.TokenTypeMinus); err != nil {
			return nil, err
		} else if ok {
			action = StoreActionRemFlags
		} else {
			action = StoreActionSetFlags
		}
	} else {
		action = StoreActionAddFlags
	}

	if err := p.ConsumeBytesFold('F', 'L', 'A', 'G', 'S'); err != nil {
		return nil, err
	}

	var silent bool

	if ok, err := p.Matches(rfcparser.TokenTypePeriod); err != nil {
		return nil, err
	} else if ok {
		if err := p.ConsumeBytesFold('S', 'I', 'L', 'E', 'N', 'T'); err != nil {
			return nil, err
		}

		silent = true
	}

	if err := p.Consume(rfcparser.TokenTypeSP, "expected space after FLAGS"); err != nil {
		return nil, err
	}

	flags, err := parseStoreFlags(p)
	if err != nil {
		return nil, err
	}

	return &Store{
		SeqSet:         seqSet,
		Action:         action,
		Flags:          flags,
		Silent:         silent,
		UnchangedSince: unchangedSince,
	}, nil
}

func parseStoreFlags(p *rfcparser.Parser) ([]string, error) {
	//                  (flag-list / (flag *(SP flag)))
	fl, ok, err := TryParseFlagList(p)
	if err != nil {
		return nil, err
	} else if ok {
		return fl, nil
	}

	var flags []string

	// first flag.
	{
		f, err := ParseFlag(p)
		if err != nil {
			return nil, err
		}

		flags = append(flags, f)
	}

	// remaining.
	for {
		if ok, err := p.Matches(rfcparser.TokenTypeSP); err != nil {
			return nil, err
		} else if !ok {
			break
		}

		f, err := ParseFlag(p)
		if err != nil {
			return nil, err
		}

		flags = append(flags, f)
	}

	return flags, nil
}
