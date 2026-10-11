package response

import (
	"fmt"

	"github.com/ProtonMail/gluon/imap"
)

// RFC 8474 §5.3:
//
//	emailid-search-correlator = "EMAILID" SP "(" sequence-number SP nstring ")"
//	threadid-search-correlator = "THREADID" SP "(" sequence-number SP nstring ")"
//
// The correlators map the sequence numbers of the SEARCH results to persistent object ids when
// requested through SEARCH RETURN (EMAILID) / (THREADID).

type emailIDSearchCorrelator struct {
	seq imap.SeqID
	id  imap.EmailID
}

func EmailIDSearchCorrelator(seq imap.SeqID, id imap.EmailID) Response {
	return &emailIDSearchCorrelator{seq: seq, id: id}
}

func (r *emailIDSearchCorrelator) Send(s Session) error {
	return s.WriteResponse(r.String())
}

func (r *emailIDSearchCorrelator) String() string {
	return fmt.Sprintf("* EMAILID (%v %v)", r.seq, renderNString(string(r.id)))
}

type threadIDSearchCorrelator struct {
	seq imap.SeqID
	id  imap.ThreadID
}

func ThreadIDSearchCorrelator(seq imap.SeqID, id imap.ThreadID) Response {
	return &threadIDSearchCorrelator{seq: seq, id: id}
}

func (r *threadIDSearchCorrelator) Send(s Session) error {
	return s.WriteResponse(r.String())
}

func (r *threadIDSearchCorrelator) String() string {
	return fmt.Sprintf("* THREADID (%v %v)", r.seq, renderNString(string(r.id)))
}
