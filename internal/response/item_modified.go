package response

import "fmt"

type itemModified struct {
	seqSet string
}

// ItemModified is the MODIFIED response code (RFC 4551) listing the messages
// that failed a conditional STORE (UNCHANGEDSINCE). The set contains UIDs for a
// UID STORE command and sequence numbers otherwise.
func ItemModified(seqSet string) *itemModified {
	return &itemModified{seqSet: seqSet}
}

func (c *itemModified) String() string {
	return fmt.Sprintf("MODIFIED %v", c.seqSet)
}
