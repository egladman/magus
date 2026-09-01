package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/egladman/magus/internal/interactive/tty"
	"os"
	"strings"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

func doctorCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	var probe, fix, list bool
	_, err := cmdParse("doctor", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&probe, "probe", false,
			"Run each declared tool-readiness probe instead of only listing it (forks a process per gated tool)")
		fs.BoolVar(&fix, "fix", false,
			"Run the remedy each finding names, where one exists (see --dry-run to list them first)")
		fs.BoolVar(&list, "list", false,
			"Print every check magus would run - name, subject, and MGS code - without running any of them")
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
			fs.PrintDefaults()
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

	// Query daemon status for the daemon-related checks. Non-fatal on failure.
	daemonInfo := buildDaemonInfo(ctx)

	dopts := []doctor.Option{doctor.WithConfig(globalCfg), doctor.WithDaemonInfo(daemonInfo), doctor.WithSkillCatalog(agentSkills)}
	if probe {
		dopts = append(dopts, doctor.WithProbe())
	}
	if exp, ok := doctorExplanations(root); ok {
		dopts = append(dopts, doctor.WithExplanations(exp))
	}
	out := doctor.Run(ctx, root, ws, wsErr, dopts...)

	if err := emitDoctor(opts, out); err != nil {
		return err
	}
	if fix {
		if err := applyDoctorFixes(ctx, root, rc, out); err != nil {
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
		// rather than returning nil - which reported a still-broken workspace as healthy
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
//
// The name is printed alone on its line so the identifier is what a reader copies, and
// the subject sits underneath rather than beside it - a name column wide enough for
// "stale-spell-shadow-acknowledgments" pushes every subject past a terminal's width.
func emitDoctorChecks(opts OutputOptions, checks []doctor.CheckInfo) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, checks)
	case outputName:
		for _, c := range checks {
			fmt.Println(c.Name)
		}
		return nil
	}

	fmt.Printf("%d checks, in the order `magus doctor` reports them:\n\n", len(checks))
	for _, c := range checks {
		fmt.Print("  " + c.Name)
		var notes []string
		if c.Code != "" {
			notes = append(notes, c.Code)
		}
		if !c.NeedsWorkspace {
			notes = append(notes, "reported even when the workspace fails to load")
		}
		if len(notes) > 0 {
			fmt.Printf("  (%s)", strings.Join(notes, "; "))
		}
		fmt.Printf("\n      %s [%s]\n", c.Doc, c.Evidence)
	}
	return nil
}

func emitDoctor(opts OutputOptions, out types.DoctorReport) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, c := range out.Checks {
			if c.Status != types.DoctorOK {
				fmt.Println(c.Name)
			}
		}
		return nil
	}

	// Doctor's report stays on stdout (it is the command's primary output, meant to be
	// piped/grepped) but shares the cache's colored [pass]/[fail] status glyphs so the
	// whole tool reads consistently. Color only when stdout is a TTY and NO_COLOR is unset.
	color := tty.WantsColor(os.Stdout, tty.SystemProbe)

	if out.Workspace != "" {
		fmt.Printf("workspace: %s\n\n", out.Workspace)
	}
	for _, c := range out.Checks {
		fmt.Printf("%s %s", checkGlyph(c, color), c.Name)
		if c.Message != "" {
			fmt.Printf(": %s", c.Message)
		}
		// Only the evidence that qualifies the verdict is printed. Stamping "measured"
		// on the forty checks that measured something is noise, and noise is what
		// people learn to skip; "inferred" is the one that says read this twice.
		if c.Evidence == types.EvidenceInferred {
			fmt.Printf(" [%s]", c.Evidence)
		}
		fmt.Println()
		for _, d := range c.Details {
			fmt.Printf("    %s\n", d)
		}
	}
	fmt.Printf("\nsummary: %d ok, %d fail", out.Summary.OK, out.Summary.Fail)
	if out.Summary.Advice > 0 {
		// Named only when there is some. A count of things that did not fail reads as
		// a scold when it is always there.
		fmt.Printf(", %d advice", out.Summary.Advice)
	}
	if out.Summary.Unknown > 0 {
		fmt.Printf(", %d unknown", out.Summary.Unknown)
	}
	fmt.Println()
	return nil
}

// checkGlyph renders a finding's level, except that a check which did not run gets its
// own glyph rather than the pass it nominally returned.
//
// A skipped check carries DoctorOK because it found nothing wrong, which is true and
// misleading: nothing was looked at. Rendering it as [pass] is how a green report came
// to mean "either fine or unexamined, and you cannot tell which from here".
func checkGlyph(c types.DoctorCheck, color bool) string {
	if c.Evidence == types.EvidenceUnknown {
		if color {
			return "\x1b[36m[unknown]\x1b[0m"
		}
		return "[unknown]"
	}
	return statusGlyph(c.Status, color)
}

// statusGlyph renders a doctor check's status with the shared [pass]/[fail] glyphs,
// colored (green/red) when color is true. Mirrors the cache handler's glyphs so a
// failed check and a failed build look identical across the tool.
func statusGlyph(status types.DoctorCheckStatus, color bool) string {
	label, code := "[?]", "0"
	switch status {
	case types.DoctorOK:
		label, code = "[pass]", "32" // green
	case types.DoctorFail:
		label, code = "[fail]", "31" // red
	case types.DoctorAdvice:
		// Yellow, not red: it did not fail, and coloring it like a failure would
		// undo the whole point of the level.
		label, code = "[advice]", "33"
	}
	if color {
		return "\x1b[" + code + "m" + label + "\x1b[0m"
	}
	return label
}

// buildDaemonInfo queries the running daemon (if any) and returns a
// DaemonInfo for the doctor checks. If no daemon is reachable, returns an
// empty DaemonInfo so checks render a sensible "no daemon" message.
func buildDaemonInfo(ctx context.Context) doctor.DaemonInfo {
	sockDir := proc.SockDir()
	di := doctor.DaemonInfo{SockDir: sockDir}

	// Populate bridge fields from resolved config. BridgeEnabled is true unless
	// explicitly set to false (mirrors how MCP.Enabled works).
	di.MCPAddr = mcpAddrString()
	di.BridgeEnabled = globalCfg.Console.Enabled == nil || *globalCfg.Console.Enabled
	di.MCPEnabled = globalCfg.MCP.Enabled == nil || *globalCfg.MCP.Enabled

	// daemon.enabled=false means this invocation is self-contained, so there is nothing
	// to ask. Without this check the probe still discovers and dials whatever daemon the
	// host happens to be running: doctor then reports on a process the caller opted out
	// of, and the testscript suite - which sets MAGUS_DAEMON_ENABLED=false precisely to
	// stay hermetic - reaches the real socket and fails wherever a daemon is up.
	if !globalCfg.Daemon.Enabled {
		return di
	}

	addr, err := resolveDaemonAddr(ctx, "")
	if err != nil {
		return di
	}
	di.SockAddr = addr

	reply, err := proc.QueryStatus(ctx, addr)
	if err != nil {
		return di
	}
	di.Reachable = true
	// Mode separates the `magus server start` daemon from the per-process proc server
	// this very invocation may have spun up. Only the former starts the MCP HTTP server,
	// so only it makes a bridge something to expect.
	di.Persistent = reply.Mode == "daemon"
	di.ParentPID = reply.ParentPID
	di.DaemonVersion = reply.DaemonVersion
	di.Capacity = reply.Capacity
	di.Running = reply.Running
	di.Queued = reply.Queued
	for _, w := range reply.Workspaces {
		di.Workspaces = append(di.Workspaces, doctor.LoadedWorkspace{
			Root:       w.Root,
			LoadedAt:   w.LoadedAt,
			LastAccess: w.LastAccess,
		})
	}
	return di
}

// applyDoctorFixes runs the remedy each non-OK finding names.
//
// It dispatches EXISTING magus subcommands, never a private repair routine, and that is
// the safety property rather than an implementation detail: --fix can only do things you
// could have typed yourself and can inspect afterwards. A finding whose remedy needs
// judgment - narrow this over-wide glob, or accept that the key is deliberately volatile?
// - declares no Fix and stays a report, which is why this can be blunt about running what
// it is given.
//
// Nothing here writes config directly either. A config-shaped remedy is spelled `config
// set ...`, so the one thing in magus that edits config stays the one thing that edits
// config.
//
// Failures are reported and do not stop the rest: the findings are independent, and a
// remedy that cannot run is a thing to say, not a reason to leave the others unapplied.
func applyDoctorFixes(ctx context.Context, root string, rc runConfig, out types.DoctorReport) error {
	var ran, failed int
	for _, c := range out.Checks {
		if c.Status == types.DoctorOK || len(c.Fix) == 0 {
			continue
		}
		cmdline := "magus " + strings.Join(c.Fix, " ")
		if globalCfg.DryRun {
			fmt.Printf("would fix %s: %s\n", c.Name, cmdline)
			continue
		}
		fmt.Printf("fixing %s: %s\n", c.Name, cmdline)
		if err := dispatchSub(ctx, root, rc, c.Fix[0], c.Fix[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "magus doctor --fix: %s: %v\n", c.Name, err)
			failed++
			continue
		}
		ran++
	}
	switch {
	case globalCfg.DryRun:
		return nil
	case ran == 0 && failed == 0:
		fmt.Println("nothing to fix: no finding named a remedy magus can run")
	case failed > 0:
		return fmt.Errorf("magus doctor --fix: %d of %d remedy/remedies failed", failed, ran+failed)
	default:
		fmt.Printf("\napplied %d remedy/remedies; re-run `%s` to confirm\n", ran, hint.Doctor)
	}
	return nil
}
