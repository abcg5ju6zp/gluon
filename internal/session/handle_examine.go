package session

import (
	"context"

	"github.com/ProtonMail/gluon/imap/command"
	"github.com/ProtonMail/gluon/internal/response"
	"github.com/ProtonMail/gluon/profiling"
)

func (s *Session) handleExamine(ctx context.Context, tag string, cmd *command.Examine, ch chan response.Response) error {
	profiling.Start(ctx, profiling.CmdTypeExamine)
	defer profiling.Stop(ctx, profiling.CmdTypeExamine)

	return s.handleSelectOrExamine(ctx, tag, cmd.Mailbox, cmd.Params, true, ch)
}
