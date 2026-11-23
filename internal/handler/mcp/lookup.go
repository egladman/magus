package mcp

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// Keep the first answer small enough to read before asking for another page.
const lookupPageSize = 20

type queryTool struct {
	workspace *magus.Magus
}

func (*queryTool) Name() string { return hint.ToolQuery.String() }

func (t *queryTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	terms, ok := req.Params["terms"].(string)
	if !ok || strings.TrimSpace(terms) == "" {
		return spells.InvokeResponse{}, fmt.Errorf("query: terms must be a nonempty string")
	}
	var offset int
	if raw, present := req.Params["offset"]; present {
		n, ok := raw.(float64)
		if !ok || n < 0 || n >= float64(math.MaxInt) || math.Trunc(n) != n {
			return spells.InvokeResponse{}, fmt.Errorf("query: offset must be a nonnegative integer")
		}
		offset = int(n)
	}
	result, err := std.MagusQuery(t.workspace.Context(ctx), terms, types.KnowledgeQueryOptions{
		Budget: lookupPageSize, Limit: lookupPageSize, Offset: offset,
	})
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: projectLookup(result)}, nil
}

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
