package imap

// ModSeq is a per-mailbox monotonically increasing modification sequence value
// as defined by RFC 7162 (CONDSTORE/QRESYNC). A value of zero means that no
// metadata modification has ever been recorded for the mailbox/message.
type ModSeq uint64
