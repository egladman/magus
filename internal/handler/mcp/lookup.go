package mcp

import (
	"context"
	"fmt"

	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// Keep the first answer small enough to read before asking for another page.
const lookupPageSize = 20

type lookupMatch struct {
	ID        string `json:"id"`
	Signature string `json:"signature,omitempty"`
}

type lookupPage struct {
	Matches    []lookupMatch                `json:"matches"`
	Total      int                          `json:"total"`
	NextOffset *int                         `json:"next_offset,omitempty"`
	Verdict    types.KnowledgeVerdict       `json:"verdict"`
	Reason     types.KnowledgeUnknownReason `json:"reason,omitempty"`
}

func lookupWorkspace(ctx context.Context, repo types.WorkspaceRepository, terms string, offset int) (lookupPage, error) {
	if offset < 0 {
		return lookupPage{}, fmt.Errorf("lookup: offset must be nonnegative")
	}
	result, err := std.MagusQuery(types.WithWorkspace(ctx, repo), terms, types.KnowledgeQueryOptions{
		Budget: lookupPageSize, Limit: lookupPageSize, Offset: offset,
	})
	if err != nil {
		return lookupPage{}, err
	}
	return projectLookup(result), nil
}

func projectLookup(result types.KnowledgeQueryOutput) lookupPage {
	page := lookupPage{
		Total: result.MatchCount, Verdict: result.Answer.Verdict, Reason: result.Answer.Reason,
		Matches: make([]lookupMatch, 0, len(result.Matches)),
	}
	signatures := make(map[string]string)
	for _, node := range result.Nodes {
		if node.Kind == types.KindMethod {
			signatures[node.ID] = node.Attrs["buzz"]
		}
	}
	for _, match := range result.Matches {
		page.Matches = append(page.Matches, lookupMatch{ID: match.ID, Signature: signatures[match.ID]})
	}
	if next := result.Offset + len(page.Matches); len(page.Matches) > 0 && next < page.Total {
		page.NextOffset = &next
	}
	return page
}
