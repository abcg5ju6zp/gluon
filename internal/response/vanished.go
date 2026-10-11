package response

import (
	"strings"

	"github.com/ProtonMail/gluon/imap"
)

type vanished struct {
	earlier bool
	uids    imap.SeqSet
}

// Vanished creates a VANISHED response (RFC 7162). earlier controls whether the
// EARLIER tag is included (used during QRESYNC for UIDs that were already gone
// when the mailbox was selected).
func Vanished(earlier bool, uids imap.SeqSet) *vanished {
	return &vanished{earlier: earlier, uids: uids}
}

func (r *vanished) Send(s Session) error {
	return s.WriteResponse(r.String())
}

func (r *vanished) String() string {
	var sb strings.Builder

	sb.WriteString("* VANISHED")

	if r.earlier {
		sb.WriteString(" (EARLIER)")
	}

	sb.WriteString(" ")
	sb.WriteString(r.uids.String())

	return sb.String()
}

// mergeWith combines consecutive in-session (non-EARLIER) VANISHED responses
// so a single EXPUNGE of several messages yields one combined response.
func (r *vanished) mergeWith(other Response) Response {
	otherVanished, ok := other.(*vanished)
	if !ok {
		return nil
	}

	if r.earlier || otherVanished.earlier {
		return nil
	}

	return &vanished{
		earlier: false,
		uids:    NewSeqSetUnion(r.uids, otherVanished.uids),
	}
}

func (r *vanished) canSkip(_ Response) bool {
	return false
}
