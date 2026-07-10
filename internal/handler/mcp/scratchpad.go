package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// scratchpadTool (magus_scratchpad) gives an agent a private, per-workspace scratch
// file to jot intermediate work into and read back, instead of dumping it all into
// the conversation. It is a single file under the workspace cache dir; nothing else
// reads it, so it is not shared with the user unless they open it themselves.
type scratchpadTool struct{ opts Options }

func (t *scratchpadTool) Name() string { return "magus_scratchpad" }

// scratchpadResult is the wire shape for magus_scratchpad: the resulting file
// content plus a byte count so the agent sees the post-op state in one reply.
// Cleared is set only by the clear op, where Content is empty by definition.
type scratchpadResult struct {
	Op      string `json:"op"`
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
	Cleared bool   `json:"cleared,omitempty"`
}

// Invoke dispatches on the op param and reads/mutates the per-workspace scratchpad
// file. The ctx is unused: every op is a straight local file operation under the
// cache dir. Content is required in practice for write/append (empty is allowed).
func (t *scratchpadTool) Invoke(_ context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	op := paramString(req.Params, "op", "read")
	content := paramString(req.Params, "content", "")

	res, err := scratchpadOp(filepath.Join(t.opts.Magus.CacheDir(), "scratch"), op, content)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: res}, nil
}

// scratchpadOp is the pure file logic behind magus_scratchpad, split out so it is
// unit-testable against a t.TempDir() without constructing a full workspace. dir is
// the scratch/ directory that holds the single scratchpad.md file; it is created
// (0755) on write/append. An unknown op is rejected before any file access.
func scratchpadOp(dir, op, content string) (scratchpadResult, error) {
	path := filepath.Join(dir, "scratchpad.md")

	switch op {
	case "read":
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return scratchpadResult{Op: op}, nil
		}
		if err != nil {
			return scratchpadResult{}, fmt.Errorf("mcp: scratchpad read: %w", err)
		}
		return scratchpadResult{Op: op, Content: string(b), Bytes: len(b)}, nil

	case "write":
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return scratchpadResult{}, fmt.Errorf("mcp: scratchpad dir: %w", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return scratchpadResult{}, fmt.Errorf("mcp: scratchpad write: %w", err)
		}
		return scratchpadResult{Op: op, Content: content, Bytes: len(content)}, nil

	case "append":
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return scratchpadResult{}, fmt.Errorf("mcp: scratchpad dir: %w", err)
		}
		existing, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return scratchpadResult{}, fmt.Errorf("mcp: scratchpad read: %w", err)
		}
		var b strings.Builder
		b.Write(existing)
		// Separate old and new content with a newline only when the existing
		// content is non-empty and does not already end in one.
		if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
			b.WriteByte('\n')
		}
		b.WriteString(content)
		joined := b.String()
		if err := os.WriteFile(path, []byte(joined), 0o644); err != nil {
			return scratchpadResult{}, fmt.Errorf("mcp: scratchpad write: %w", err)
		}
		return scratchpadResult{Op: op, Content: joined, Bytes: len(joined)}, nil

	case "clear":
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return scratchpadResult{}, fmt.Errorf("mcp: scratchpad clear: %w", err)
		}
		return scratchpadResult{Op: op, Cleared: true}, nil

	default:
		return scratchpadResult{}, errors.New("mcp: scratchpad op must be one of read, write, append, clear")
	}
}

var _ types.SpellDriver = (*scratchpadTool)(nil)
