package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
	"golang.org/x/term"
)

func doctorCmd(ctx context.Context, root string, args []string) error {
	_, err := cmdParse("doctor", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus doctor [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Validate the workspace: config file schema, cache writability,")
			fmt.Fprintln(os.Stderr, "discoverable projects, language coverage, a ci target, magusfile")
			fmt.Fprintln(os.Stderr, "syntax, spell docs, dependency cycles, workspace-escaping symlinks,")
			fmt.Fprintln(os.Stderr, "recognised env vars, charm/target name collisions, and VCS")
			fmt.Fprintln(os.Stderr, "base-ref reachability.")
			fmt.Fprintln(os.Stderr, "Exits non-zero if any check fails.")
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
	daemonInfo := buildDaemonInfo(ctx, ws)

	out := doctor.Run(
		root, ws, wsErr,
		doctor.WithConfig(globalCfg),
		doctor.WithDaemonInfo(daemonInfo),
	)

	if err := emitDoctor(opts, out); err != nil {
		return err
	}
	if out.Summary.Fail > 0 {
		return fmt.Errorf("magus doctor: %d check(s) failed", out.Summary.Fail)
	}
	return nil
}

func emitDoctor(opts OutputOptions, out doctor.Report) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, c := range out.Checks {
			if c.Status != "ok" {
				fmt.Println(c.Name)
			}
		}
		return nil
	}

	// Doctor's report stays on stdout (it is the command's primary output, meant to be
	// piped/grepped) but shares the cache's coloured [pass]/[fail] status glyphs so the
	// whole tool reads consistently. Colour only when stdout is a TTY and NO_COLOR is unset.
	color := term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == ""

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
	fmt.Printf("\nsummary: %d ok, %d fail\n", out.Summary.OK, out.Summary.Fail)
	return nil
}

// statusGlyph renders a doctor check's status with the shared [pass]/[fail] glyphs,
// coloured (green/red) when color is true. Mirrors the cache handler's glyphs so a
// failed check and a failed build look identical across the tool.
func statusGlyph(status doctor.CheckStatus, color bool) string {
	label, code := "[?]", "0"
	switch status {
	case doctor.StatusOK:
		label, code = "[pass]", "32" // green
	case doctor.StatusFail:
		label, code = "[fail]", "31" // red
	}
	if color {
		return "\x1b[" + code + "m" + label + "\x1b[0m"
	}
	return label
}

// buildDaemonInfo queries the running daemon (if any) and returns a
// DaemonInfo for the doctor checks. If no daemon is reachable, returns an
// empty DaemonInfo so checks render a sensible "no daemon" message.
func buildDaemonInfo(ctx context.Context, _ types.WorkspaceRepository) doctor.DaemonInfo {
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
