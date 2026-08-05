package mcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// magus_output and magus_tail_log are the captured-output retrieval tools: they return a
// target execution's stdout/stderr, not a knowledge-graph answer. magus_output addresses ONE
// past execution by its reference id (out1a2b3c); magus_tail_log returns the LATEST log for a
// project (no ref needed). Both read straight from the cache dir.

// outputReader is the slice of the workspace magus_output needs: resolve a
// target-output ref to its stored bytes and descriptor, invert the ref back to
// the target(s) that would produce it (on a miss), and render one of those
// matches as the `magus run` command that would reproduce it. *magus.Magus
// satisfies it.
type outputReader interface {
	OutputByRef(ref string) ([]byte, cache.OutputDescriptor, error)
	IdentifyRef(ctx context.Context, ref string) ([]types.RefMatch, error)
	RefMatchCommand(mt types.RefMatch) string
}

// outputTool (magus_output) retrieves one target execution's captured output by its
// reference id - the MCP analog of `magus query output <ref>`. It is a dedicated tool,
// not a mode of magus_query, so a free-text graph query can never collide with a ref id.
type outputTool struct {
	reader outputReader
}

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
// descriptor. On a miss, ctx feeds a best-effort IdentifyRef sweep (it runs a real
// key computation per candidate target, unlike the straight cache-dir read the
// happy path takes) so the error names what would have produced the ref instead of
// leaving the agent to guess.
func (t *outputTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	ref := paramString(req.Params, "ref", "")
	if ref == "" {
		return spells.InvokeResponse{}, errors.New("mcp: ref is required")
	}
	if !cache.LooksLikeRef(ref) {
		return spells.InvokeResponse{}, fmt.Errorf("mcp: %q is not a target-output reference (expected out<hex>, e.g. out1a2b3c)", ref)
	}
	data, desc, err := t.reader.OutputByRef(ref)
	if err != nil {
		var amb *cache.AmbiguousRefError
		switch {
		case errors.As(err, &amb):
			return spells.InvokeResponse{}, fmt.Errorf("mcp: %w", amb)
		case errors.Is(err, fs.ErrNotExist):
			return spells.InvokeResponse{}, t.notFoundError(ctx, ref, err)
		default:
			return spells.InvokeResponse{}, err
		}
	}
	return spells.InvokeResponse{Data: outputRefResult{
		Ref:        desc.Ref,
		Project:    desc.Project,
		Target:     desc.Target,
		Failed:     desc.Failed,
		DurationMs: desc.DurationMs,
		Output:     string(data),
	}}, nil
}

// notFoundError renders the "no stored output" error for a ref OutputByRef could not
// resolve. It best-effort-inverts the ref back to the target(s) that would produce it
// (IdentifyRef) and folds the finding into the error message in the same three shapes
// cmd/magus/query.go's printRefIdentitySuggestion renders for the CLI - compacted to
// one sentence plus the command(s), since this is an agent-facing tool error rather
// than a terminal layout. An MCP tool failure is still the right shape here (the
// agent asked for bytes that do not exist), only the message gets richer.
//
// If IdentifyRef itself errors, cause's plain message is kept as-is: a best-effort
// suggestion must never replace a lookup failure with a different one.
//
// Unlike the CLI, there is no --no-default-charms escape hatch over MCP: run.go's
// effectiveCharms always merges the workspace's configured default_charms into
// magus_run_target, so a command rendered with that flag (the match required the
// bare CI variant) names a `magus run` invocation this MCP surface cannot itself
// execute. bareVariantUnreachable flags that case so the message says so plainly
// instead of sending an MCP-only agent after a command it will run, silently get
// the wrong ref from, and have no explanation why.
func (t *outputTool) notFoundError(ctx context.Context, ref string, cause error) error {
	plain := fmt.Errorf("mcp: no stored output for ref %q: %w", ref, cause)
	matches, err := t.reader.IdentifyRef(ctx, ref)
	if err != nil {
		return plain
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("mcp: no stored output for ref %q: no target in this workspace keys to that ref at the current tree, so the run that printed it had different inputs", ref)
	case 1:
		cmd := t.reader.RefMatchCommand(matches[0])
		msg := fmt.Sprintf("mcp: no stored output for ref %q: this workspace would produce it with `%s`", ref, cmd)
		if bareVariantUnreachable(cmd) {
			msg += "; " + noDefaultCharmsNote
		}
		return errors.New(msg)
	default:
		cmds := make([]string, len(matches))
		anyUnreachable := false
		for i, mt := range matches {
			cmds[i] = t.reader.RefMatchCommand(mt)
			anyUnreachable = anyUnreachable || bareVariantUnreachable(cmds[i])
		}
		msg := fmt.Sprintf("mcp: no stored output for ref %q: this workspace would produce it with any of: %s", ref, strings.Join(cmds, "; "))
		if anyUnreachable {
			msg += "; " + noDefaultCharmsNote
		}
		return errors.New(msg)
	}
}

// noDefaultCharmsNote is the one sentence added to a not-found message when a
// rendered command required --no-default-charms: this MCP surface has no
// equivalent flag, so magus_run_target cannot reproduce that ref.
const noDefaultCharmsNote = "this ref was minted without the workspace's default charms, which magus_run_target always applies, so that tool cannot reproduce it"

// bareVariantUnreachable reports whether cmd is a RefMatchCommand rendering that
// required --no-default-charms. Detecting this from the rendered string (rather
// than re-deriving "bare match + configured defaults" independently) keeps
// RefMatchCommand the single source of that decision.
func bareVariantUnreachable(cmd string) bool {
	return strings.Contains(cmd, "--no-default-charms")
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

func (t *tailLogTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	projectPath := paramString(req.Params, "project", "")
	if projectPath == "" {
		return spells.InvokeResponse{}, errors.New("mcp: project is required")
	}

	logPath, err := t.opts.Magus.TailLog(projectPath, "")
	if errors.Is(err, types.ErrNoCache) {
		return spells.InvokeResponse{}, errors.New("mcp: workspace cache is not open")
	}
	if errors.Is(err, fs.ErrNotExist) {
		return spells.InvokeResponse{}, errors.New("mcp: no cache entries for project " + projectPath)
	}
	if err != nil {
		return spells.InvokeResponse{}, fmt.Errorf("mcp: cache lookup: %w", err)
	}

	b, err := os.ReadFile(filepath.Clean(logPath))
	if err != nil {
		toolLogger(ctx).WarnContext(ctx, "mcp: read log failed", "path", logPath, "error", err)
		return spells.InvokeResponse{}, fmt.Errorf("mcp: read log: %w", err)
	}

	return spells.InvokeResponse{Data: tailResult{
		Project: projectPath,
		LogPath: logPath,
		Content: string(b),
	}}, nil
}

var (
	_ spells.Driver = (*outputTool)(nil)
	_ spells.Driver = (*tailLogTool)(nil)
)
