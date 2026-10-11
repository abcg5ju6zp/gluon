package response

import (
	"testing"

	"github.com/ProtonMail/gluon/imap"
	"github.com/stretchr/testify/require"
)

func TestVanishedMerge(t *testing.T) {
	a := Vanished(false, imap.NewSeqSetFromUID([]imap.UID{1}))
	b := Vanished(false, imap.NewSeqSetFromUID([]imap.UID{3}))

	merged := a.mergeWith(b)
	require.NotNil(t, merged)
	require.Equal(t, "* VANISHED 1,3", merged.String())

	// EARLIER responses are never merged with in-session responses.
	earlier := Vanished(true, imap.NewSeqSetFromUID([]imap.UID{1}))
	require.Nil(t, earlier.mergeWith(b))
	require.Nil(t, b.mergeWith(earlier))
}
