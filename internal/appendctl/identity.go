// Package appendctl contains shared primitives for the streaming, journaled APPEND flow.
package appendctl

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"time"

	"github.com/ProtonMail/gluon/imap"
)

// IdentityHash derives the stable commit identity of an APPEND request from the received
// content hash and its target context (mailbox, flags, internal date). A request retried
// after a lost connection maps to the same identity, which lets the server return the original
// APPENDUID instead of creating a second message.
func IdentityHash(
	contentHashHex string,
	mailboxInternalID imap.InternalMailboxID,
	flags imap.FlagSet,
	date time.Time,
) string {
	normalizedFlags := flags.ToSliceUnsorted()
	sort.Strings(normalizedFlags)

	h := sha256.New()

	_, _ = h.Write([]byte(contentHashHex))
	_, _ = h.Write([]byte{0})

	var mboxBytes [8]byte
	binary.BigEndian.PutUint64(mboxBytes[:], uint64(mailboxInternalID))
	_, _ = h.Write(mboxBytes[:])
	_, _ = h.Write([]byte{0})

	for _, f := range normalizedFlags {
		_, _ = h.Write([]byte(f))
		_, _ = h.Write([]byte{1})
	}

	_, _ = h.Write([]byte{0})

	var dateBytes [8]byte
	if !date.IsZero() {
		binary.BigEndian.PutUint64(dateBytes[:], uint64(date.Unix()))
	}

	_, _ = h.Write(dateBytes[:])

	return hex.EncodeToString(h.Sum(nil))
}
