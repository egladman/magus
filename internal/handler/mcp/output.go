package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// magus_output and magus_tail_log are the captured-output retrieval tools: they return a
// target execution's stdout/stderr, not a knowledge-graph answer. magus_output addresses ONE
// past execution by its reference id (out1a2b3c); magus_tail_log returns the LATEST log for a
// project (no ref needed). Both read straight from the cache dir.

// outputReader is the slice of the workspace magus_output needs: resolve a
// target-output ref to its stored bytes and descriptor. *magus.Magus satisfies it.
type outputReader interface {
	OutputByRef(ref string) ([]byte, cache.OutputDescriptor, error)
}

// outputTool (magus_output) retrieves one target execution's captured output by its
// reference id - the MCP analog of `magus query output <ref>`. It is a dedicated tool,
// not a mode of magus_query, so a free-text graph query can never collide with a ref id.
type outputTool struct{ reader outputReader }

func (t *outputTool) Name() string { return "magus_output" }

// outputRefResult is the wire shape for magus_output: the captured output plus the
// run's identity, so an agent that saw a ref in a run fetches the exact bytes
// without re-reading a wall of text.
type outputRefResult struct {
	Ref        string `json:"ref"`
	Project    string `json:"project,omitempty"`
	Target     string `json:"target,omitempty"`
	Failed     bool   `json:"failed"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Output     string `json:"output"`
}

// Invoke resolves a target-output ref (or unique prefix) to its stored bytes and
// descriptor. The ctx is unused: resolution is a straight read from the cache dir.
func (t *outputTool) Invoke(_ context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	ref := paramString(req.Params, "ref", "")
	if ref == "" {
		return types.InvokeResponse{}, errors.New("mcp: ref is required")
	}
	if !cache.LooksLikeRef(ref) {
		return types.InvokeResponse{}, fmt.Errorf("mcp: %q is not a target-output reference (expected out<hex>, e.g. out1a2b3c)", ref)
	}
	data, desc, err := t.reader.OutputByRef(ref)
	if err != nil {
		var amb *cache.AmbiguousRefError
		switch {
		case errors.As(err, &amb):
			return types.InvokeResponse{}, fmt.Errorf("mcp: %w", amb)
		case errors.Is(err, fs.ErrNotExist):
			return types.InvokeResponse{}, fmt.Errorf("mcp: no stored output for ref %q: %w", ref, err)
		default:
			return types.InvokeResponse{}, err
		}
	}
	return types.InvokeResponse{Data: outputRefResult{
		Ref:        desc.Ref,
		Project:    desc.Project,
		Target:     desc.Target,
		Failed:     desc.Failed,
		DurationMs: desc.DurationMs,
		Output:     string(data),
	}}, nil
}

type tailResult struct {
	Project string `json:"project"`
	LogPath string `json:"log_path"`
	Content string `json:"content"`
}

type tailLogTool struct {
	opts Options
}

func (t *tailLogTool) Name() string { return "magus_tail_log" }

func (t *tailLogTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	projectPath := paramString(req.Params, "project", "")
	if projectPath == "" {
		return types.InvokeResponse{}, errors.New("mcp: project is required")
	}

	logPath, err := t.opts.Magus.TailLog(projectPath, "")
	if errors.Is(err, types.ErrNoCache) {
		return types.InvokeResponse{}, errors.New("mcp: workspace cache is not open")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return types.InvokeResponse{}, errors.New("mcp: no cache entries for project " + projectPath)
	}
	if err != nil {
		return types.InvokeResponse{}, fmt.Errorf("mcp: cache lookup: %w", err)
	}

	b, err := os.ReadFile(filepath.Clean(logPath))
	if err != nil {
		toolLogger(ctx).Warn("mcp: read log failed", "path", logPath, "error", err)
		return types.InvokeResponse{}, fmt.Errorf("mcp: read log: %w", err)
	}

	return types.InvokeResponse{Data: tailResult{
		Project: projectPath,
		LogPath: logPath,
		Content: string(b),
	}}, nil
}

var (
	_ types.SpellDriver = (*outputTool)(nil)
	_ types.SpellDriver = (*tailLogTool)(nil)
)
