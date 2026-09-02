package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/memory"
	"github.com/egladman/magus/spells"
)

// memoryTool (magus_memory) is the durable handoff journal: a set of
// discrete, categorized memory RECORDS that persist across sessions, models, and agent
// hosts. Each record is one typed pointer into the magus domain (a saved query, a graph
// node, an output ref, a command, a doc) - the payload is the ref, never free prose;
// only a decision/plan carries a one-line caption. Records live in the user's XDG state
// directory keyed by repository (worktrees share them), NOT in the repo. The store and
// schema live in internal/memory; this tool is the agent-facing door onto it. The console
// (MemoryService RPC) is the second door onto the same store.
type memoryTool struct{ opts Options }

func (t *memoryTool) Name() string { return hint.ToolMemory.String() }

// memoryRefView is the wire shape of one typed ref on a record.
type memoryRefView struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// memoryRecordView is the wire shape of a record returned by get/list/put.
type memoryRecordView struct {
	Name       string          `json:"name"`
	Type       string          `json:"type"`
	Status     string          `json:"status,omitempty"`
	Refs       []memoryRefView `json:"refs"`
	References []string        `json:"references,omitempty"`
	Body       string          `json:"body,omitempty"`
	Excerpt    string          `json:"excerpt,omitempty"`
	Created    int64           `json:"created"`
	Updated    int64           `json:"updated"`
}

func toRecordView(r memory.Record) memoryRecordView {
	refs := make([]memoryRefView, len(r.Refs))
	for i, ref := range r.Refs {
		refs[i] = memoryRefView{Kind: string(ref.Kind), Target: ref.Target}
	}
	return memoryRecordView{
		Name: r.Name, Type: string(r.Type), Status: r.Status, Refs: refs,
		References: r.References, Body: r.Body, Excerpt: r.Excerpt,
		Created: r.Created, Updated: r.Updated,
	}
}

func (t *memoryTool) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	root := t.opts.Magus.Root()
	op := paramString(req.Params, "op", "list")
	switch op {
	case "list":
		recs, issues, err := memory.Inspect(root)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		views := make([]memoryRecordView, len(recs))
		for i, r := range recs {
			views[i] = toRecordView(r)
		}
		return spells.InvokeResponse{Data: map[string]any{"records": views, "issues": issues}}, nil

	case "get":
		name := paramString(req.Params, "name", "")
		rec, err := memory.Get(root, name)
		if err != nil {
			return spells.InvokeResponse{}, fmt.Errorf("mcp: no memory named %q", name)
		}
		return spells.InvokeResponse{Data: toRecordView(rec)}, nil

	case "put":
		refs, err := memory.ParseRefs(paramString(req.Params, "refs", ""))
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		rec := memory.Record{
			Name:       strings.TrimSpace(paramString(req.Params, "name", "")),
			Type:       memory.RecordType(strings.TrimSpace(paramString(req.Params, "type", ""))),
			Status:     strings.TrimSpace(paramString(req.Params, "status", "")),
			Body:       paramString(req.Params, "body", ""),
			Excerpt:    paramString(req.Params, "excerpt", ""),
			Refs:       refs,
			References: splitCommaList(paramString(req.Params, "references", "")),
		}
		// No mask: the params the caller sent ARE the mask, mirroring the CLI. allow_missing
		// keeps the proto's spelling here rather than the CLI's --amend, because every other
		// param on this tool is the wire name.
		stored, err := memory.Update(root, rec, memory.UpdateOptions{
			AllowMissing: paramBool(req.Params, "allow_missing", true),
		})
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return spells.InvokeResponse{}, fmt.Errorf("mcp: the journal holds no entry named %q; drop allow_missing=false to create it, or find it with op=list", rec.Name)
			}
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: toRecordView(stored)}, nil

	case "delete":
		name := paramString(req.Params, "name", "")
		if err := memory.Delete(root, name, true); err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: map[string]any{"deleted": name}}, nil

	case "verify":
		report, err := memory.Verify(root, func(ref memory.Ref) error {
			if ref.Kind != memory.RefKindOutput {
				return nil
			}
			_, err := t.opts.Magus.OutputDescriptorByRef(ref.Target)
			return err
		})
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: report}, nil

	case "cursor":
		// The cursor is the single "where did I leave off" snapshot, kept beside the
		// record set. Passing content overwrites it; omitting content reads it.
		if _, writing := req.Params["content"]; writing {
			return spells.InvokeResponse{}, errors.New("mcp: cursor writes are retired because one shared snapshot is unsafe across sessions; create or update a named plan/decision with op=put instead")
		}
		content, err := memory.ReadCursor(root)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: map[string]any{
			"cursor":  content,
			"warning": "legacy cursor snapshot; use named handoff-journal entries with op=put instead",
		}}, nil

	default:
		return spells.InvokeResponse{}, errors.New("mcp: memory op must be one of list, get, put, delete, verify (cursor is legacy read-only)")
	}
}

// splitCommaList splits a comma-separated param (memory-to-memory reference names) into a
// trimmed, non-empty slice. Names are kebab slugs (no commas), so a comma split is safe.
func splitCommaList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var _ spells.Driver = (*memoryTool)(nil)
