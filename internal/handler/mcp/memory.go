package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/memory"
	"github.com/egladman/magus/types"
)

// memoryTool (magus_memory) is the durable counterpart to magus_scratchpad: a set of
// discrete, categorized memory RECORDS that persist across sessions, models, and agent
// hosts. Each record is one typed pointer into the magus domain (a saved query, a graph
// node, an output ref, a command, a doc) - the payload is the ref, never free prose;
// only a decision/plan carries a one-line caption. Records live in the user's XDG state
// directory keyed by repository (worktrees share them), NOT in the repo. The store and
// schema live in internal/memory; this tool is the agent-facing door onto it. The console
// (MemoryService RPC) is the second door onto the same store.
type memoryTool struct{ opts Options }

func (t *memoryTool) Name() string { return ToolMemory.String() }

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
	Created    int64           `json:"created"`
	Updated    int64           `json:"updated"`
}

func toRecordView(r memory.Record) memoryRecordView {
	refs := make([]memoryRefView, len(r.Refs))
	for i, ref := range r.Refs {
		refs[i] = memoryRefView{Kind: ref.Kind, Target: ref.Target}
	}
	return memoryRecordView{
		Name: r.Name, Type: r.Type, Status: r.Status, Refs: refs,
		References: r.References, Body: r.Body, Created: r.Created, Updated: r.Updated,
	}
}

func (t *memoryTool) Invoke(_ context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	root := t.opts.Magus.Root()
	op := paramString(req.Params, "op", "list")
	switch op {
	case "list":
		recs, err := memory.List(root)
		if err != nil {
			return types.InvokeResponse{}, err
		}
		views := make([]memoryRecordView, len(recs))
		for i, r := range recs {
			views[i] = toRecordView(r)
		}
		return types.InvokeResponse{Data: map[string]any{"records": views}}, nil

	case "get":
		name := paramString(req.Params, "name", "")
		rec, err := memory.Get(root, name)
		if err != nil {
			return types.InvokeResponse{}, fmt.Errorf("mcp: no memory named %q", name)
		}
		return types.InvokeResponse{Data: toRecordView(rec)}, nil

	case "put":
		refs, err := parseMemoryRefs(paramString(req.Params, "refs", ""))
		if err != nil {
			return types.InvokeResponse{}, err
		}
		rec := memory.Record{
			Name:       strings.TrimSpace(paramString(req.Params, "name", "")),
			Type:       strings.TrimSpace(paramString(req.Params, "type", "")),
			Status:     strings.TrimSpace(paramString(req.Params, "status", "")),
			Body:       paramString(req.Params, "body", ""),
			Refs:       refs,
			References: splitCommaList(paramString(req.Params, "references", "")),
		}
		stored, err := memory.Put(root, rec) // validates the schema, rejects at the door
		if err != nil {
			return types.InvokeResponse{}, err
		}
		return types.InvokeResponse{Data: toRecordView(stored)}, nil

	case "delete":
		name := paramString(req.Params, "name", "")
		if err := memory.Delete(root, name, true); err != nil {
			return types.InvokeResponse{}, err
		}
		return types.InvokeResponse{Data: map[string]any{"deleted": name}}, nil

	case "cursor":
		// The cursor is the single "where did I leave off" snapshot, kept beside the
		// record set. Passing content overwrites it; omitting content reads it.
		if _, writing := req.Params["content"]; writing {
			content := paramString(req.Params, "content", "")
			if err := memory.WriteCursor(root, content); err != nil {
				return types.InvokeResponse{}, err
			}
			return types.InvokeResponse{Data: map[string]any{"cursor": content}}, nil
		}
		content, err := memory.ReadCursor(root)
		if err != nil {
			return types.InvokeResponse{}, err
		}
		return types.InvokeResponse{Data: map[string]any{"cursor": content}}, nil

	default:
		return types.InvokeResponse{}, errors.New("mcp: memory op must be one of list, get, put, delete, cursor")
	}
}

// parseMemoryRefs parses the `refs` param: one ref per line as "kind: target". It splits
// each line on the FIRST colon only, so a target that itself contains colons or commas (a
// query expression, a namespaced node ID) survives intact - newline is the sole record
// separator. Kind and target validity is enforced downstream by memory.Validate.
func parseMemoryRefs(s string) ([]memory.Ref, error) {
	var refs []memory.Ref
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			return nil, fmt.Errorf("mcp: ref %q must be written as 'kind: target' (kinds: query, node, output, command, doc)", line)
		}
		refs = append(refs, memory.Ref{
			Kind:   strings.TrimSpace(line[:i]),
			Target: strings.TrimSpace(line[i+1:]),
		})
	}
	return refs, nil
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

var _ types.SpellDriver = (*memoryTool)(nil)
