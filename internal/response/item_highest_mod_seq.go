package response

import (
	"fmt"

	"github.com/ProtonMail/gluon/imap"
)

type itemHighestModSeq struct {
	val imap.ModSeq
}

func ItemHighestModSeq(n imap.ModSeq) *itemHighestModSeq {
	return &itemHighestModSeq{val: n}
}

func (c *itemHighestModSeq) String() string {
	return fmt.Sprintf("HIGHESTMODSEQ %v", c.val)
}
