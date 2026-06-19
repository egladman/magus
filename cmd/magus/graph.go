package main

import (
	"context"
	"fmt"
	"os"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/types"
)

type graphRenderOptions struct {
	Upstream bool
	Depth    int
	Spell    string
	Roots    []string
	// Target is the target whose duration history to show (e.g. "build").
	// Falls back to "build" when empty.
	Target string
}

// renderWorkspaceGraph emits the project dependency graph; respects -o (text|json|yaml|dot|mermaid|tree).
func renderWorkspaceGraph(ctx context.Context, ws types.WorkspaceRepository, opts graphRenderOptions) error {
	outOpts, err := ResolveOutput(global.output, outputDot, outputMermaid, outputTree)
	if err != nil {
		return err
	}

	g, err := ws.Graph()
	if err != nil {
		return err
	}

	target := opts.Target
	if target == "" {
		target = "build"
	}

	// Load timing history best-effort; silently skip when unavailable.
	composeOpts := []magus.ComposeOption{magus.WithGraphInput(g)}
	if opts.Upstream {
		composeOpts = append(composeOpts, magus.WithUpstream())
	}
	if opts.Spell != "" {
		composeOpts = append(composeOpts, magus.WithComposeSpell(opts.Spell))
	}
	if len(opts.Roots) > 0 {
		composeOpts = append(composeOpts, magus.WithComposeRoots(opts.Roots...))
	}
	if path := globalCfg.HistoryPath; path != "" {
		var hist forecast.History
		if err := hist.Load(ctx, path); err == nil {
			composeOpts = append(composeOpts, magus.WithGraphHistory(&hist, target))
		}
	}

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, magus.ComposeGraph(ws, composeOpts...))
	case outputName:
		out := magus.ComposeGraph(ws, composeOpts...)
		for _, n := range out.Nodes {
			fmt.Println(n.Path)
		}
		return nil
	case outputDot:
		return render.WriteGraphDOT(os.Stdout, magus.ComposeGraph(ws, composeOpts...))
	case outputMermaid:
		return render.WriteGraphMermaid(os.Stdout, magus.ComposeGraph(ws, composeOpts...))
	}

	// text and tree formats both render the ASCII dependency tree.
	var rOpts []render.RenderOption
	if opts.Upstream {
		rOpts = append(rOpts, render.WithDirection(types.Upstream))
	}
	if opts.Spell != "" {
		rOpts = append(rOpts, render.WithSpell(opts.Spell))
	}
	if opts.Depth != 0 {
		rOpts = append(rOpts, render.WithMaxDepth(opts.Depth))
	}
	if len(opts.Roots) > 0 {
		rOpts = append(rOpts, render.WithRoots(opts.Roots...))
	}
	return render.WriteTree(os.Stdout, g, rOpts...)
}
