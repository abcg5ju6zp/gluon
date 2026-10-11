package session

import (
	"context"
	"fmt"

	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/response"
)

func (s *Session) handleEnable(ctx context.Context, tag string, cmd *command.Enable, ch chan response.Response) error {
	s.userLock.Lock()
	defer s.userLock.Unlock()

	if s.state == nil {
		return ErrNotAuthenticated
	}

	var enabled []imap.Capability

	for _, capability := range cmd.Capabilities {
		//nolint:exhaustive // only CONDSTORE and QRESYNC are enable-able
		switch imap.Capability(capability) {
		case imap.CondStore:
			s.state.EnableCondStore()

			if !containsCapability(enabled, imap.CondStore) {
				enabled = append(enabled, imap.CondStore)
			}

		case imap.QResync:
			s.state.EnableQResync()

			if !containsCapability(enabled, imap.CondStore) {
				enabled = append(enabled, imap.CondStore)
			}

			if !containsCapability(enabled, imap.QResync) {
				enabled = append(enabled, imap.QResync)
			}

		default:
			ch <- response.Bad(tag).WithError(fmt.Errorf("ENABLE: unsupported capability %v", capability))
			return nil
		}
	}
	if len(enabled) == 0 {
		ch <- response.Bad(tag).WithError(fmt.Errorf("ENABLE: no supported extensions"))
		return nil
	}

	ch <- response.Enable(enabled...)
	ch <- response.Ok(tag).WithMessage("ENABLE")

	return nil
}

func containsCapability(caps []imap.Capability, want imap.Capability) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}

	return false
}
