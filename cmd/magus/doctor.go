package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

func doctorCommand(ctx context.Context, root string, rc runConfig, args []string) error {
	var probe, fix, list bool
	_, err := commandParse("doctor", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&probe, "probe", false,
			"Run each declared tool-readiness probe instead of only listing it (forks a process per gated tool)")
		fs.BoolVar(&fix, "fix", false,
			"Run the remedy each finding names, where one exists (see --dry-run to list them first)")
		fs.BoolVar(&list, "list", false,
			"Print every check magus would run - name, subject, and MGS code - without running any of them")
		command.NegatedBoolVar(fs, &probe, "probe")
		command.NegatedBoolVar(fs, &fix, "fix")
		command.NegatedBoolVar(fs, &list, "list")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus doctor [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Validate the workspace: config file schema, cache writability,")
			fmt.Fprintln(os.Stderr, "discoverable projects, language coverage, a ci target, magusfile")
			fmt.Fprintln(os.Stderr, "syntax, spell docs, dependency cycles, workspace-escaping symlinks,")
			fmt.Fprintln(os.Stderr, "recognized env vars, charm/target name collisions, and VCS")
			fmt.Fprintln(os.Stderr, "base-ref reachability.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Findings come at two levels. [fail] is a workspace that is wrong -")
			fmt.Fprintln(os.Stderr, "a dependency cycle, an unparsable magusfile, two targets claiming")
			fmt.Fprintln(os.Stderr, "one output - and exits non-zero. [advice] is a convention magus")
			fmt.Fprintln(os.Stderr, "recommends, reported and not fatal, because how your workspace is")
			fmt.Fprintln(os.Stderr, "laid out is your call.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Every finding is reported under a stable check name. --list prints")
			fmt.Fprintln(os.Stderr, "them all, and what each one looks at, without running any.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	if list {
		return emitDoctorChecks(opts, doctor.Checks())
	}

	ws, wsErr := inspectWorkspace(ctx, root)

	// Query the server for the server-related checks. Non-fatal on failure.
	dopts := []doctor.Option{
		doctor.WithConfig(globalCfg), doctor.WithServerInfo(doctor.BuildServerInfo(ctx, serverSource())), doctor.WithSkillCatalog(agentSkills),
		doctor.WithGraphNodes(func(ctx context.Context) ([]types.KnowledgeNode, error) {
			// Domain-only: the graph `graph export` renders into the docs site.
			g, err := loadKnowledgeGraph(ctx, root, false, false, false)
			if err != nil {
				return nil, err
			}
			return g.Nodes(), nil
		}),
	}
	if probe {
		dopts = append(dopts, doctor.WithProbe())
	}
	if exp, ok := doctorExplanations(root); ok {
		dopts = append(dopts, doctor.WithExplanations(exp))
	}
	// The budget the broker starts with (brokerServe); a ci run strips every write charm,
	// and its verify passes claim without one.
	ctx = doctor.WithGateClaims(ctx, func(ctx context.Context, targets []types.Target) ([]types.MachineClaim, error) {
		m, err := loadMagus(ctx, root)
		if err != nil {
			return nil, err
		}
		return m.MachineClaims(ctx, targets, nil, nil), nil
	}, mem.BudgetMB(mem.UsableBytes(ctx), globalCfg.ConcurrencyProfile), cache.MachineCeiling())
	ctx = doctor.WithAdmissions(ctx, func(context.Context) ([]types.MachineAdmission, error) {
		return file.Log[types.MachineAdmission]{Path: server.AdmissionLogPath()}.Load()
	})
	out := doctor.Run(ctx, root, ws, wsErr, dopts...)

	if err := emitDoctor(opts, out); err != nil {
		return err
	}
	if fix {
		if err := doctor.ApplyFixes(ctx, os.Stdout, out, globalCfg.DryRun, func(ctx context.Context, sub string, args []string) error {
			return dispatchSub(ctx, root, rc, sub, args)
		}); err != nil {
			return err
		}
		// A dry run applied nothing, so there is nothing new to judge: it lists the
		// remedies and exits 0 even where the workspace is unhealthy, which is what
		// doctor_agent_skills.txtar pins against a deliberately imperfect fixture.
		if globalCfg.DryRun {
			return nil
		}
		// The report above described the workspace BEFORE the remedies ran, so its
		// summary is no longer what is true. Re-run the checks for the count to gate on,
		// rather than returning nil, which reported a still-broken workspace as healthy
		// to anything scripting `doctor --fix`. ws is reused: every remedy writes a file
		// the checks re-read here, and none changes the shape magus.Inspect models.
		out = doctor.Run(ctx, root, ws, wsErr, dopts...)
		if out.Summary.Fail > 0 {
			return fmt.Errorf("magus doctor: %d check(s) still failing after --fix", out.Summary.Fail)
		}
		return nil
	}
	if out.Summary.Fail > 0 {
		return fmt.Errorf("magus doctor: %d check(s) failed", out.Summary.Fail)
	}
	return nil
}

// emitDoctorChecks renders `magus doctor --list`: what magus would check, without
// checking it.
func emitDoctorChecks(opts output.Options, checks []doctor.CheckInfo) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, checks)
	case outputName:
		return emitNames(os.Stdout, doctor.CheckNames(checks))
	}
	doctor.RenderChecks(os.Stdout, checks)
	return nil
}

func emitDoctor(opts output.Options, out types.DoctorReport) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		return emitNames(os.Stdout, doctor.UnhealthyNames(out))
	}
	doctor.RenderReport(os.Stdout, out)
	return nil
}

// serverSource gathers what the server checks need from this process: the version, the
// resolved server and MCP addresses, and which services the config enables.
func serverSource() doctor.ServerSource {
	return doctor.ServerSource{
		ClientVersion: version,
		Addr:          resolveServerAddr(""),
		MCPAddr:       mcpAddrString(),
		BridgeEnabled: globalCfg.Console.Enabled == nil || *globalCfg.Console.Enabled,
		MCPEnabled:    globalCfg.MCP.Enabled == nil || *globalCfg.MCP.Enabled,
		ServerEnabled: globalCfg.Server.Enabled,
	}
}

// doctorExplanations reads every declared notes store and reports which files it explains,
// for the unexplained-hotspots check.
//
// Only FILE anchors count. A symbol anchor names code in some file, but resolving which one
// needs the knowledge graph, and a check that silently reported fewer explained files
// whenever the graph was cold would be measuring the index rather than the notes.
//
// ok is false when no store could be read at all, which is what separates "this workspace
// keeps no notes" from "magus could not tell". The check renders the second as unknown
// rather than as a gap.
func doctorExplanations(root string) (doctor.Explanations, bool) {
	stores, err := review.ResolveNoteStores(root, noteDirs(), "")
	if err != nil {
		return doctor.Explanations{}, false
	}
	var out doctor.Explanations
	read := false
	for _, st := range stores {
		list, lerr := notes.List(st.Dir)
		if lerr != nil {
			continue
		}
		read = true
		out.Notes += len(list)
		for _, n := range list {
			for _, a := range n.Anchors {
				if a.Kind == notes.AnchorFile {
					out.Files = append(out.Files, a.Target)
				}
			}
		}
	}
	return out, read
}
