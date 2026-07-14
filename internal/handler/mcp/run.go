package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/types"
)

type runResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Charms lists the execution charms actually applied to this run: the
	// workspace default_charms merged with any charm suffix on the target
	// param. Omitted when empty. Lets the agent see whether writes (rw) were
	// enabled and avoid redundantly re-invoking a :rw form.
	Charms     []string           `json:"charms,omitempty"`
	DurationMs int64              `json:"duration_ms"`
	Events     []codec.RawMessage `json:"events"`
}

// effectiveCharms merges the workspace default_charms with the per-run charm
// suffix parsed from the target, mirroring cmd/magus withDefaultCharms:
// defaults first, per-run stacked on top, exact duplicates dropped. The MCP run
// tools are the daemon equivalent of `magus run`, so default_charms apply here
// too (there is no --no-default-charms escape hatch over MCP).
func effectiveCharms(perRun, defaults []string) []string {
	if len(defaults) == 0 {
		return perRun
	}
	out := slices.Clone(defaults)
	for _, c := range perRun {
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

type runTargetTool struct {
	opts Options
}

func (t *runTargetTool) Name() string { return "magus_run_target" }

func (t *runTargetTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	rawTarget := paramString(req.Params, "target", "")
	if rawTarget == "" {
		return types.InvokeResponse{}, errors.New("mcp: target is required")
	}
	projectsArg := paramString(req.Params, "projects", "")
	dryRun := paramBool(req.Params, "dry_run", false)
	// Split "spell::target" → spell filter + target.
	spellFilter, targetStr, hasSpell := strings.Cut(rawTarget, "::")
	if !hasSpell {
		spellFilter, targetStr = "", rawTarget
	}
	parsed, err := types.ParseTarget(targetStr)
	if err != nil {
		return types.InvokeResponse{}, fmt.Errorf("mcp: invalid target: %w", err)
	}
	switch parsed.Name {
	case "fmt":
		parsed.Name = "format"
	case "gen":
		parsed.Name = "generate"
	}

	ws := t.opts.Magus

	var targets []types.Target
	if projectsArg != "" {
		for _, path := range strings.Fields(projectsArg) {
			tg, e := ws.ExpandPath(types.Target{Path: path, Name: parsed.Name})
			if e != nil {
				return types.InvokeResponse{}, fmt.Errorf("mcp: expand %s: %w", path, e)
			}
			targets = append(targets, tg...)
		}
	} else {
		cwdTargets, found, e := ws.ExpandCwd(parsed)
		if e != nil {
			return types.InvokeResponse{}, fmt.Errorf("mcp: expand cwd: %w", e)
		}
		if found {
			targets = cwdTargets
		} else {
			targets, err = ws.ExpandPath(parsed)
		}
	}
	if err != nil {
		return types.InvokeResponse{}, fmt.Errorf("mcp: resolve targets: %w", err)
	}
	if len(targets) == 0 {
		toolLogger(ctx).Warn("mcp: no targets resolved", "raw_target", rawTarget)
		return types.InvokeResponse{}, errors.New("mcp: no targets resolved for " + rawTarget)
	}

	var buf bytes.Buffer
	rw, err := magus.NewReportWriter(&buf, nil)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	// Run dispatches explicit targets and builds no affected graph, so it needs
	// no graph observer. (Graph events come from the affected path; see
	// tool_affected, which routes them request-scoped via context.)

	charms := effectiveCharms(parsed.Charms, t.opts.Config.DefaultCharms)

	runOpts := []magus.RunOption{magus.WithReport(rw)}
	if dryRun {
		runOpts = append(runOpts, magus.WithDryRun())
	}
	if len(charms) > 0 {
		runOpts = append(runOpts, magus.WithCharms(charms...))
	}
	if spellFilter != "" {
		runOpts = append(runOpts, magus.WithSpellFilter(spellFilter))
	}

	start := time.Now()
	runErr := t.opts.Magus.Run(ctx, targets, runOpts...)
	dur := time.Since(start)

	// Close the writer to flush the drain goroutine before parsing.
	_ = rw.Close()
	events := parseEventLines(&buf)
	out := runResult{
		OK:         runErr == nil,
		Charms:     charms,
		DurationMs: dur.Milliseconds(),
		Events:     events,
	}
	if runErr != nil {
		out.Error = runErr.Error()
	}
	return types.InvokeResponse{Data: out}, nil
}

var _ types.SpellDriver = (*runTargetTool)(nil)

// parseEventLines parses JSONL from buf into a slice of raw JSON objects.
func parseEventLines(buf *bytes.Buffer) []codec.RawMessage {
	// Use a 1 MB scanner buffer — report lines can be large on wide workspaces.
	scanner := bufio.NewScanner(buf)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	var events []codec.RawMessage
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var raw codec.RawMessage
		if err := codec.Unmarshal(line, &raw); err == nil {
			events = append(events, raw)
		}
	}
	// scanner.Err() is intentionally ignored here: a truncated or malformed
	// line just means fewer events returned, which is already visible to the
	// caller via the events slice length.
	if events == nil {
		// ensure JSON encodes [] not null
		events = []codec.RawMessage{}
	}
	return events
}
