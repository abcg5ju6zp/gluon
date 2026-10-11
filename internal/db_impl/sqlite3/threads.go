package sqlite3

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/ProtonMail/gluon/db"
	"github.com/ProtonMail/gluon/imap"
	"github.com/ProtonMail/gluon/internal/db_impl/sqlite3/utils"
	v1 "github.com/ProtonMail/gluon/internal/db_impl/sqlite3/v1"
	v4 "github.com/ProtonMail/gluon/internal/db_impl/sqlite3/v4"
	"github.com/ProtonMail/gluon/internal/thread"
	"github.com/bradenaw/juniper/xslices"
	"github.com/google/uuid"
)

// insertMessageThreadTokens persists the threading tokens of one message.
func insertMessageThreadTokens(ctx context.Context, qw utils.QueryWrapper, id imap.InternalMessageID, tokens []string) error {
	if len(tokens) == 0 {
		return nil
	}

	for _, chunk := range xslices.Chunk(tokens, db.ChunkLimit) {
		query := fmt.Sprintf("INSERT INTO %v (`%v`, `%v`) VALUES %v",
			v4.ThreadTokensTableName,
			v4.ThreadTokensFieldMessageID,
			v4.ThreadTokensFieldToken,
			strings.Join(xslices.Repeat("(?,?)", len(chunk)), ","))

		args := make([]any, 0, 2*len(chunk))

		for _, token := range chunk {
			args = append(args, id, token)
		}

		if _, err := utils.ExecQuery(ctx, qw, query, args...); err != nil {
			return err
		}
	}

	return nil
}

func deleteMessageThreadTokens(ctx context.Context, qw utils.QueryWrapper, id imap.InternalMessageID) error {
	query := fmt.Sprintf("DELETE FROM %v WHERE `%v` = ?",
		v4.ThreadTokensTableName, v4.ThreadTokensFieldMessageID)

	_, err := utils.ExecQuery(ctx, qw, query, id)

	return err
}

// getThreadTokenNeighbors returns the other live messages that share at least one threading token
// with any of the given messages. Tombstoned messages are excluded since they no longer participate
// in threading.
func getThreadTokenNeighbors(ctx context.Context, qw utils.QueryWrapper, ids []imap.InternalMessageID) ([]imap.InternalMessageID, error) {
	result := make(map[imap.InternalMessageID]struct{})

	for _, chunk := range xslices.Chunk(ids, db.ChunkLimit) {
		query := fmt.Sprintf(
			"SELECT DISTINCT `t2`.`%[1]v` FROM `%[3]v` AS t1 "+
				"JOIN `%[3]v` AS t2 ON `t2`.`%[2]v` = `t1`.`%[2]v` "+
				"JOIN `%[4]v` AS m ON `m`.`%[5]v` = `t2`.`%[1]v` AND `m`.`%[6]v` = FALSE "+
				"WHERE `t1`.`%[1]v` IN (%[7]v)",
			v4.ThreadTokensFieldMessageID,
			v4.ThreadTokensFieldToken,
			v4.ThreadTokensTableName,
			v1.MessagesTableName,
			v1.MessagesFieldID,
			v1.MessagesFieldDeleted,
			utils.GenSQLIn(len(chunk)),
		)

		neighbors, err := utils.MapQueryRows[imap.InternalMessageID](ctx, qw, query, utils.MapSliceToAny(chunk)...)
		if err != nil {
			return nil, err
		}

		for _, neighbor := range neighbors {
			result[neighbor] = struct{}{}
		}
	}

	out := make([]imap.InternalMessageID, 0, len(result))

	for id := range result {
		out = append(out, id)
	}

	return out, nil
}

// loadMessageThreadTokens returns the token sets of the given messages.
func loadMessageThreadTokens(ctx context.Context, qw utils.QueryWrapper, ids []imap.InternalMessageID) (map[imap.InternalMessageID][]string, error) {
	result := make(map[imap.InternalMessageID][]string, len(ids))

	for _, chunk := range xslices.Chunk(ids, db.ChunkLimit) {
		query := fmt.Sprintf("SELECT `%v`, `%v` FROM %v WHERE `%v` IN (%v)",
			v4.ThreadTokensFieldMessageID,
			v4.ThreadTokensFieldToken,
			v4.ThreadTokensTableName,
			v4.ThreadTokensFieldMessageID,
			utils.GenSQLIn(len(chunk)))

		rows, err := utils.MapQueryRowsFn(ctx, qw, query, func(scanner utils.RowScanner) (struct {
			id    imap.InternalMessageID
			token string
		}, error) {
			var row struct {
				id    imap.InternalMessageID
				token string
			}

			return row, scanner.Scan(&row.id, &row.token)
		}, utils.MapSliceToAny(chunk)...)
		if err != nil {
			return nil, err
		}

		for _, row := range rows {
			result[row.id] = append(result[row.id], row.token)
		}
	}

	return result, nil
}

// loadMessageThreadIDs returns the current thread id of the given messages (empty for freshly
// inserted messages without an assignment yet).
func loadMessageThreadIDs(ctx context.Context, qw utils.QueryWrapper, ids []imap.InternalMessageID) (map[imap.InternalMessageID]imap.ThreadID, error) {
	result := make(map[imap.InternalMessageID]imap.ThreadID, len(ids))

	for _, chunk := range xslices.Chunk(ids, db.ChunkLimit) {
		query := fmt.Sprintf("SELECT `%v`, `%v` FROM %v WHERE `%v` IN (%v)",
			v1.MessagesFieldID,
			v4.MessagesFieldThreadID,
			v1.MessagesTableName,
			v1.MessagesFieldID,
			utils.GenSQLIn(len(chunk)))

		rows, err := utils.MapQueryRowsFn(ctx, qw, query, func(scanner utils.RowScanner) (struct {
			id       imap.InternalMessageID
			threadID string
		}, error) {
			var (
				row      struct{ id imap.InternalMessageID; threadID string }
				threadID sql.NullString
			)

			if err := scanner.Scan(&row.id, &threadID); err != nil {
				return row, err
			}

			if threadID.Valid {
				row.threadID = threadID.String
			}

			return row, nil
		}, utils.MapSliceToAny(chunk)...)
		if err != nil {
			return nil, err
		}

		for _, row := range rows {
			result[row.id] = imap.ThreadID(row.threadID)
		}
	}

	return result, nil
}

// recomputeMessageThreads recomputes thread membership for all components reachable from the seed
// messages through shared threading tokens. Components keep their existing THREADID whenever
// possible; merging components deterministically keeps the smallest existing id and splitting
// components deterministically keep the id on the component rooted at the smallest member while
// other components get fresh ids. The resulting assignment for every message in the affected
// closure is returned so snapshots can be refreshed in the same commit.
func recomputeMessageThreads(ctx context.Context, qw utils.QueryWrapper, seed []imap.InternalMessageID) ([]db.ThreadAssignment, error) {
	if len(seed) == 0 {
		return nil, nil
	}

	// Expand the closure: all live messages transitively connected to a seed through tokens.
	closure := make(map[imap.InternalMessageID]struct{}, len(seed))

	frontier := append([]imap.InternalMessageID(nil), seed...)

	for _, id := range frontier {
		closure[id] = struct{}{}
	}

	for len(frontier) > 0 {
		neighbors, err := getThreadTokenNeighbors(ctx, qw, frontier)
		if err != nil {
			return nil, err
		}

		var next []imap.InternalMessageID

		for _, neighbor := range neighbors {
			if _, ok := closure[neighbor]; !ok {
				closure[neighbor] = struct{}{}
				next = append(next, neighbor)
			}
		}

		frontier = next
	}

	closureIDs := make([]imap.InternalMessageID, 0, len(closure))

	for id := range closure {
		closureIDs = append(closureIDs, id)
	}

	tokensByMessage, err := loadMessageThreadTokens(ctx, qw, closureIDs)
	if err != nil {
		return nil, err
	}

	threadIDs, err := loadMessageThreadIDs(ctx, qw, closureIDs)
	if err != nil {
		return nil, err
	}

	graph := thread.NewGraph(len(closureIDs))

	for _, id := range closureIDs {
		tokens := tokensByMessage[id]
		if len(tokens) == 0 {
			// Defensive: a live message without stored tokens must not join another thread.
			tokens = thread.ExtractTokens(nil, id.String())
		}

		graph.Add(id.String(), tokens)
	}

	components := graph.Components()

	// Deterministic resolution of component ids: process components by their smallest member and
	// let each claim the smallest prior thread id not already claimed by another component.
	componentKeys := make([][]imap.InternalMessageID, 0, len(components))

	for _, component := range components {
		members := make([]imap.InternalMessageID, 0, len(component.IDs))

		for _, id := range component.IDs {
			internalID, err := imap.InternalMessageIDFromString(id)
			if err != nil {
				return nil, fmt.Errorf("failed to parse internal message id %v: %w", id, err)
			}

			members = append(members, internalID)
		}

		sort.Slice(members, func(i, j int) bool {
			return members[i].String() < members[j].String()
		})
		componentKeys = append(componentKeys, members)
	}

	sort.Slice(componentKeys, func(i, j int) bool {
		return componentKeys[i][0].String() < componentKeys[j][0].String()
	})

	usedPriorIDs := make(map[imap.ThreadID]struct{})
	componentThreadIDs := make([]imap.ThreadID, len(componentKeys))

	for idx, members := range componentKeys {
		priorSet := make(map[imap.ThreadID]struct{})

		for _, member := range members {
			if prior := threadIDs[member]; len(prior) > 0 {
				priorSet[prior] = struct{}{}
			}
		}

		priors := make([]string, 0, len(priorSet))

		for prior := range priorSet {
			priors = append(priors, string(prior))
		}

		sort.Strings(priors)

		var chosen imap.ThreadID

		for _, prior := range priors {
			candidate := imap.ThreadID(prior)
			if _, used := usedPriorIDs[candidate]; !used {
				chosen = candidate
				break
			}
		}

		if len(chosen) == 0 {
			chosen = imap.ThreadID(uuid.NewString())
		}

		usedPriorIDs[chosen] = struct{}{}
		componentThreadIDs[idx] = chosen
	}

	assignments := make([]db.ThreadAssignment, 0, len(closureIDs))

	for idx, members := range componentKeys {
		threadID := componentThreadIDs[idx]

		for _, member := range members {
			if err := updateMessageThreadID(ctx, qw, member, threadID); err != nil {
				return nil, err
			}

			assignments = append(assignments, db.ThreadAssignment{InternalID: member, ThreadID: threadID})
		}
	}

	return assignments, nil
}

func updateMessageThreadID(ctx context.Context, qw utils.QueryWrapper, id imap.InternalMessageID, threadID imap.ThreadID) error {
	query := fmt.Sprintf("UPDATE %v SET `%v` = ? WHERE `%v` = ?",
		v1.MessagesTableName,
		v4.MessagesFieldThreadID,
		v1.MessagesFieldID)

	_, err := utils.ExecQuery(ctx, qw, query, threadID, id)

	return err
}

func (w writeOps) RecomputeMessageThreads(ctx context.Context, seed []imap.InternalMessageID) ([]db.ThreadAssignment, error) {
	return recomputeMessageThreads(ctx, w.qw, seed)
}
