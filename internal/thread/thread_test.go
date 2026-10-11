package thread

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractTokens(t *testing.T) {
	t.Run("own id and references", func(t *testing.T) {
		literal := []byte("Message-ID: <a@x>\r\nIn-Reply-To: <parent@x>\r\nReferences: <g1@x> <g2@x>\r\nSubject: hi\r\n\r\nbody")
		tokens := ExtractTokens(literal, "fallback")

		require.Contains(t, tokens, "<a@x>")
		require.Contains(t, tokens, "<parent@x>")
		require.Contains(t, tokens, "<g1@x>")
		require.Contains(t, tokens, "<g2@x>")
		require.Len(t, tokens, 4)
	})

	t.Run("synthetic token when no usable id", func(t *testing.T) {
		tokens := ExtractTokens([]byte("Subject: hi\r\n\r\nbody"), "fallback-id")
		require.Equal(t, []string{syntheticTokenPrefix + "fallback-id"}, tokens)
	})

	t.Run("bare message id is accepted", func(t *testing.T) {
		tokens := ExtractTokensFromValues("bare-id")
		require.Equal(t, []string{"bare-id"}, tokens)
	})

	t.Run("duplicates are removed", func(t *testing.T) {
		tokens := ExtractTokensFromValues("<a@x>", "Re: <a@x>", "<b@x>")
		require.Equal(t, []string{"<a@x>", "<b@x>"}, tokens)
	})
}

func TestGraphComponents(t *testing.T) {
	// m1 and m2 share <a@x>; m3 references <a@x> too (merge); m4 is isolated.
	graph := NewGraph(4)
	graph.Add("m1", []string{"<a@x>"})
	graph.Add("m2", []string{"<a@x>", "<b@x>"})
	graph.Add("m3", []string{"<b@x>", "<c@x>"})
	graph.Add("m4", []string{"<d@x>"})

	components := graph.Components()
	require.Len(t, components, 2)

	var merged, isolated []string
	for _, component := range components {
		if len(component.IDs) == 3 {
			merged = component.IDs
		} else {
			isolated = component.IDs
		}
	}

	require.ElementsMatch(t, []string{"m1", "m2", "m3"}, merged)
	require.Equal(t, []string{"m4"}, isolated)
}

func TestGraphSplit(t *testing.T) {
	// m2 bridges <a@x> and <b@x>; removing it splits the graph in two.
	graph := NewGraph(2)
	graph.Add("m1", []string{"<a@x>"})
	graph.Add("m3", []string{"<b@x>"})

	components := graph.Components()
	require.Len(t, components, 2)
}
