// Package thread implements persistent RFC 5322 based conversation threading used to derive
// RFC 8474 THREADID values. Threading is modeled as a bipartite graph between messages and the
// Message-ID tokens they carry (their own Message-ID plus their In-Reply-To/References values):
// messages that transitively share a token belong to the same thread. This model keeps threading
// consistent when the same message appears in multiple mailboxes and allows threads to merge
// (a new message references members of two previously separate threads) and split (when a message
// that bridged two groups is removed or stops referencing one of them).
package thread

import (
	"regexp"
	"strings"

	"github.com/ProtonMail/gluon/rfc822"
)

// syntheticTokenPrefix is used for messages that carry no usable Message-ID/In-Reply-To/References
// tokens. Such messages form an isolated thread of their own.
const syntheticTokenPrefix = "gluon-synthetic-thread-token:"

var msgIDTokenRegexp = regexp.MustCompile(`<[^>]+>`)

// ExtractTokensFromValues normalizes raw Message-ID/In-Reply-To/References header values into
// angle-addr tokens. Every <...> occurrence is a token; a single bare whitespace-free value is
// used as a fallback.
func ExtractTokensFromValues(values ...string) []string {
	tokens := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)

	addToken := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) == 0 {
			return
		}

		if _, ok := seen[value]; ok {
			return
		}

		seen[value] = struct{}{}

		tokens = append(tokens, value)
	}

	for _, raw := range values {
		matches := msgIDTokenRegexp.FindAllString(raw, -1)
		if len(matches) > 0 {
			for _, match := range matches {
				addToken(match)
			}

			continue
		}

		// Non-conformant messages may carry a bare msg-id (or even a CFWS wrapped one); only use
		// the raw value when it is a single token so we never merge unrelated messages.
		raw = strings.TrimSpace(raw)
		if len(raw) > 0 && !strings.ContainsAny(raw, " \t\r\n") {
			addToken(raw)
		}
	}

	return tokens
}

// ExtractTokens extracts the normalized set of threading tokens from a message literal.
// The message's own Message-ID is a token, as are every angle-addr value found in In-Reply-To and
// References. If a header has a single bare msg-id without angle brackets it is used as-is.
// When no token can be found, a synthetic token derived from fallbackID (the internal message id)
// is returned so that the message is still assigned exactly one stable, isolated thread.
func ExtractTokens(literal []byte, fallbackID string) []string {
	values := make([]string, 0, 3)

	for _, key := range []string{"Message-ID", "In-Reply-To", "References"} {
		value, err := rfc822.GetHeaderValue(literal, key)
		if err != nil || len(value) == 0 {
			continue
		}

		values = append(values, value)
	}

	tokens := ExtractTokensFromValues(values...)
	if len(tokens) == 0 {
		tokens = append(tokens, syntheticTokenPrefix+fallbackID)
	}

	return tokens
}

// Graph is an union-find over messages connected through shared Message-ID tokens.
type Graph struct {
	parent  []int
	tokenTo map[string]int
	ids     []string
}

func NewGraph(capacity int) *Graph {
	return &Graph{
		parent:  make([]int, 0, capacity),
		tokenTo: make(map[string]int, capacity),
		ids:     make([]string, 0, capacity),
	}
}

func (g *Graph) find(i int) int {
	root := i

	for g.parent[root] != root {
		root = g.parent[root]
	}

	for g.parent[i] != root {
		next := g.parent[i]
		g.parent[i] = root
		i = next
	}

	return root
}

func (g *Graph) union(a, b int) {
	ra, rb := g.find(a), g.find(b)
	if ra != rb {
		g.parent[ra] = rb
	}
}

// Add registers a message with its set of threading tokens. Messages sharing any token end up in
// the same component.
func (g *Graph) Add(id string, tokens []string) {
	idx := len(g.ids)
	g.ids = append(g.ids, id)
	g.parent = append(g.parent, idx)

	for _, token := range tokens {
		if other, ok := g.tokenTo[token]; ok {
			g.union(idx, other)
		} else {
			g.tokenTo[token] = idx
		}
	}
}

// Component is a group of message ids that belong to the same thread.
type Component struct {
	IDs []string
}

// Components returns all connected components. The member ids within a component and the
// components themselves are returned in insertion order, which keeps the output deterministic.
func (g *Graph) Components() []Component {
	order := make([]int, 0, len(g.ids))
	groups := make(map[int][]string)

	for i := range g.ids {
		root := g.find(i)
		if _, ok := groups[root]; !ok {
			order = append(order, root)
		}

		groups[root] = append(groups[root], g.ids[i])
	}

	components := make([]Component, 0, len(order))
	for _, root := range order {
		components = append(components, Component{IDs: groups[root]})
	}

	return components
}
