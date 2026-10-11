package response

import (
	"strings"

	"github.com/ProtonMail/gluon/imap"
)

type enable struct {
	capabilities []imap.Capability
}

func Enable(capabilities ...imap.Capability) *enable {
	return &enable{capabilities: capabilities}
}

func (r *enable) Send(s Session) error {
	return s.WriteResponse(r.String())
}

func (r *enable) String() string {
	var sb strings.Builder

	sb.WriteString("* ENABLE")

	for _, capability := range r.capabilities {
		sb.WriteString(" ")
		sb.WriteString(string(capability))
	}

	return sb.String()
}
