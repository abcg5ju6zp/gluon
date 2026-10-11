package command

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParser_FetchObjectIDAttributes(t *testing.T) {
	cmd, err := testParseCommand(`tag FETCH 1:* (EMAILID THREADID)`)
	require.NoError(t, err)

	fetch, ok := cmd.Payload.(*Fetch)
	require.True(t, ok)
	require.Len(t, fetch.Attributes, 2)
	require.IsType(t, &FetchAttributeEmailID{}, fetch.Attributes[0])
	require.IsType(t, &FetchAttributeThreadID{}, fetch.Attributes[1])

	// Single attribute without list.
	cmd, err = testParseCommand(`tag FETCH 1 EMAILID`)
	require.NoError(t, err)

	fetch, ok = cmd.Payload.(*Fetch)
	require.True(t, ok)
	require.Len(t, fetch.Attributes, 1)
	require.IsType(t, &FetchAttributeEmailID{}, fetch.Attributes[0])
}

func TestParser_StatusMailboxID(t *testing.T) {
	cmd, err := testParseCommand(`tag STATUS INBOX (MAILBOXID)`)
	require.NoError(t, err)

	status, ok := cmd.Payload.(*Status)
	require.True(t, ok)
	require.Equal(t, []StatusAttribute{StatusAttributeMailboxID}, status.Attributes)
}

func TestParser_SearchObjectIDKeys(t *testing.T) {
	cmd, err := testParseCommand(`tag SEARCH EMAILID "abc-def"`)
	require.NoError(t, err)

	search, ok := cmd.Payload.(*Search)
	require.True(t, ok)
	require.Len(t, search.Keys, 1)
	require.Equal(t, &SearchKeyEmailID{Value: "abc-def"}, search.Keys[0])
	require.Empty(t, search.ReturnOptions)

	cmd, err = testParseCommand(`tag SEARCH ALL THREADID thread-1`)
	require.NoError(t, err)

	search, ok = cmd.Payload.(*Search)
	require.True(t, ok)
	require.IsType(t, &SearchKeyAll{}, search.Keys[0])
	require.Equal(t, &SearchKeyThreadID{Value: "thread-1"}, search.Keys[1])
}

func TestParser_SearchReturnObjectIDs(t *testing.T) {
	cmd, err := testParseCommand(`tag SEARCH ALL RETURN (EMAILID THREADID)`)
	require.NoError(t, err)

	search, ok := cmd.Payload.(*Search)
	require.True(t, ok)
	require.Equal(t,
		[]SearchReturnOption{SearchReturnOptionEmailID, SearchReturnOptionThreadID},
		search.ReturnOptions,
	)

	// Also supported through UID SEARCH, where the search payload is wrapped.
	cmd, err = testParseCommand(`tag UID SEARCH ALL RETURN (EMAILID)`)
	require.NoError(t, err)

	uid, ok := cmd.Payload.(*UID)
	require.True(t, ok)
	search, ok = uid.Command.(*Search)
	require.True(t, ok)
	require.Equal(t, []SearchReturnOption{SearchReturnOptionEmailID}, search.ReturnOptions)

	_, err = testParseCommand(`tag SEARCH ALL RETURN (MIN)`)
	require.Error(t, err)
}
