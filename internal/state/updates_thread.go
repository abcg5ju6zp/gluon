package state

import (
	"context"
	"fmt"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
)

// threadIDsUpdatedStateUpdate refreshes the persistent THREADID assignments in every snapshot that
// contains one of the affected messages. The update is applied atomically with the message changes
// that produced it, so a session can never observe messages of a merged or split thread with
// mixed old/new thread ids. No unsolicited responses are sent: clients learn the current THREADID
// through explicit FETCH/SEARCH requests, which is fully sufficient per RFC 8474 and keeps the
// unsolicited update stream compatible with OBJECTID-unaware clients.
type threadIDsUpdatedStateUpdate struct {
	SnapFilter
	assignments []db.ThreadAssignment
}

func NewThreadIDsStateUpdate(assignments []db.ThreadAssignment) Update {
	messageIDs := make([]imap.InternalMessageID, 0, len(assignments))

	for _, assignment := range assignments {
		messageIDs = append(messageIDs, assignment.InternalID)
	}

	return &threadIDsUpdatedStateUpdate{
		SnapFilter:  &AnyMessageIDStateFilter{MessageIDs: messageIDs},
		assignments: assignments,
	}
}

func (u *threadIDsUpdatedStateUpdate) Apply(_ context.Context, _ db.Transaction, s *State) error {
	if s.snap == nil {
		return nil
	}

	for _, assignment := range u.assignments {
		s.snap.updateMessageThreadID(assignment.InternalID, assignment.ThreadID)
	}

	return nil
}

func (u *threadIDsUpdatedStateUpdate) String() string {
	return fmt.Sprintf("ThreadIDsUpdatedStateUpdate: %v assignments", len(u.assignments))
}
