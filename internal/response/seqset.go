package response

import (
	"github.com/ProtonMail/gluon/imap"
)

// NewSeqSetUnion merges two sequence sets into a single canonical set.
func NewSeqSetUnion(a, b imap.SeqSet) imap.SeqSet {
	values := make(map[imap.SeqID]struct{})
	add := func(set imap.SeqSet) {
		for _, v := range set {
			if v.Begin == v.End {
				values[v.Begin] = struct{}{}
			} else {
				for i := v.Begin; i <= v.End; i++ {
					values[i] = struct{}{}
				}
			}
		}
	}

	add(a)
	add(b)

	ids := make([]imap.SeqID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}

	return imap.NewSeqSet(ids)
}
