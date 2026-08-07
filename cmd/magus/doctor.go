package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/egladman/magus/internal/interactive/tty"
	"os"
	"strings"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

func doctorCmd(ctx context.Context, root string, rc runConfig, args []string) error {
	var probe, fix bool
	_, err := cmdParse("doctor", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&probe, "probe", false,
			"Run each declared tool-readiness probe instead of only listing it (forks a process per gated tool)")
		fs.BoolVar(&fix, "fix", false,
			"Run the remedy each finding names, where one exists (see --dry-run to list them first)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus doctor [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Validate the workspace: config file schema, cache writability,")
			fmt.Fprintln(os.Stderr, "discoverable projects, language coverage, a ci target, magusfile")
			fmt.Fprintln(os.Stderr, "syntax, spell docs, dependency cycles, workspace-escaping symlinks,")
			fmt.Fprintln(os.Stderr, "recognised env vars, charm/target name collisions, and VCS")
			fmt.Fprintln(os.Stderr, "base-ref reachability.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Findings come at two levels. [fail] is a workspace that is wrong -")
			fmt.Fprintln(os.Stderr, "a dependency cycle, an unparsable magusfile, two targets claiming")
			fmt.Fprintln(os.Stderr, "one output - and exits non-zero. [advice] is a convention magus")
			fmt.Fprintln(os.Stderr, "recommends, reported and not fatal, because how your workspace is")
			fmt.Fprintln(os.Stderr, "laid out is your call.")
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

	ws, wsErr := inspectWorkspace(ctx, root)

	// Query daemon status for the daemon-related checks. Non-fatal on failure.
	daemonInfo := buildDaemonInfo(ctx)

	dopts := []doctor.Option{doctor.WithConfig(globalCfg), doctor.WithDaemonInfo(daemonInfo)}
	if probe {
		dopts = append(dopts, doctor.WithProbe())
	}
	out := doctor.Run(ctx, root, ws, wsErr, dopts...)

	if err := emitDoctor(opts, out); err != nil {
		return err
	}
	if fix {
		if err := applyDoctorFixes(ctx, root, rc, out); err != nil {
			return err
		}
		// The report above described the workspace BEFORE the remedies ran, so its
		// summary is no longer what is true. Re-run doctor to see the result rather
		// than trusting a count that predates the fixes.
		return nil
	}
	if out.Summary.Fail > 0 {
		return fmt.Errorf("magus doctor: %d check(s) failed", out.Summary.Fail)
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
	// piped/grepped) but shares the cache's coloured [pass]/[fail] status glyphs so the
	// whole tool reads consistently. Colour only when stdout is a TTY and NO_COLOR is unset.
	color := tty.WantsColor(os.Stdout, tty.SystemProbe)

	if out.Workspace != "" {
		fmt.Printf("workspace: %s\n\n", out.Workspace)
	}
	for _, c := range out.Checks {
		fmt.Printf("%s %s", statusGlyph(c.Status, color), c.Name)
		if c.Message != "" {
			fmt.Printf(": %s", c.Message)
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
	fmt.Println()
	return nil
}

// statusGlyph renders a doctor check's status with the shared [pass]/[fail] glyphs,
// coloured (green/red) when color is true. Mirrors the cache handler's glyphs so a
// failed check and a failed build look identical across the tool.
func statusGlyph(status types.DoctorCheckStatus, color bool) string {
	label, code := "[?]", "0"
	switch status {
	case types.DoctorOK:
		label, code = "[pass]", "32" // green
	case types.DoctorFail:
		label, code = "[fail]", "31" // red
	case types.DoctorAdvice:
		// Yellow, not red: it did not fail, and colouring it like a failure would
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
// judgement - narrow this over-wide glob, or accept that the key is deliberately volatile?
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
		fmt.Printf("\napplied %d remedy/remedies; re-run `magus doctor` to confirm\n", ran)
	}
	return nil
}
