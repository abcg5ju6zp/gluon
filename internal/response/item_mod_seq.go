package response

import (
	"fmt"

	"github.com/ProtonMail/gluon/imap"
)

type itemModSeq struct {
	val imap.ModSeq
}

// ItemModSeq is the FETCH MODSEQ message attribute: "MODSEQ (n)".
func ItemModSeq(n imap.ModSeq) *itemModSeq {
	return &itemModSeq{val: n}
}

func (c *itemModSeq) String() string {
	return fmt.Sprintf("MODSEQ (%v)", c.val)
}

type itemStoreModSeq struct {
	val imap.ModSeq
}

// ItemStoreModSeq is the MODSEQ response code in a tagged STORE OK response:
// "MODSEQ n" (RFC 4551 uses no parentheses for the response code).
func ItemStoreModSeq(n imap.ModSeq) *itemStoreModSeq {
	return &itemStoreModSeq{val: n}
}

func (c *itemStoreModSeq) String() string {
	return fmt.Sprintf("MODSEQ %v", c.val)
}
