package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

func doctorCmd(ctx context.Context, root string, args []string) error {
	var fix *bool
	_, err := cmdParse("doctor", args, func(fs *flag.FlagSet) {
		fix = fs.Bool("fix", false, "Apply fixable remediation in-place")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus doctor [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Validate the workspace: binary signature, config file schema,")
			fmt.Fprintln(os.Stderr, "cache writability, discoverable projects, language coverage,")
			fmt.Fprintln(os.Stderr, "dependency cycles, workspace-escaping symlinks, tools on PATH,")
			fmt.Fprintln(os.Stderr, "VCS base-ref reachability, recognised env vars, and mage")
			fmt.Fprintln(os.Stderr, "compatibility (coexistence, legacy forms, variadic targets).")
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

	mcpAddr := globalCfg.MCP.Address
	if mcpAddr == "" {
		mcpAddr = "127.0.0.1:7391"
	}
	mcpEnabled := globalCfg.MCP.Enabled == nil || *globalCfg.MCP.Enabled
	mcpDaemonUp := false
	if mcpIsCompiled && mcpEnabled {
		if _, err := proc.DiscoverSocket(ctx); err == nil {
			mcpDaemonUp = true
		}
	}

	// Query daemon status for the daemon-related checks. Non-fatal on failure.
	daemonInfo := buildDaemonInfo(ctx, ws)

	out := doctor.Run(
		root, ws, wsErr,
		doctor.WithConfig(globalCfg),
		doctor.WithVersion(version),
		doctor.WithCommit(commit),
		doctor.WithFix(*fix),
		doctor.WithMCPStatus(doctor.MCPStatus{
			Compiled: mcpIsCompiled,
			Enabled:  mcpEnabled,
			Address:  mcpAddr,
			DaemonUp: mcpDaemonUp,
		}),
		doctor.WithDaemonInfo(daemonInfo),
	)

	if err := emitDoctor(opts, out); err != nil {
		return err
	}
	if out.Summary.Fail > 0 || (globalCfg.Strict && out.Summary.Warn > 0) {
		return fmt.Errorf("magus doctor: %d check(s) failed, %d warning(s)", out.Summary.Fail, out.Summary.Warn)
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

	if out.Workspace != "" {
		fmt.Printf("workspace: %s\n\n", out.Workspace)
	}
	for _, c := range out.Checks {
		fmt.Printf("%s %s", statusGlyph(c.Status), c.Name)
		if c.Message != "" {
			fmt.Printf(": %s", c.Message)
		}
		fmt.Println()
		for _, d := range c.Details {
			fmt.Printf("    %s\n", d)
		}
	}
	fmt.Printf("\nsummary: %d ok, %d warn, %d fail\n", out.Summary.OK, out.Summary.Warn, out.Summary.Fail)

	if len(out.Fixes) > 0 {
		fmt.Println()
		fmt.Println("fixes applied:")
		for _, f := range out.Fixes {
			fmt.Printf("  [%s] %s", f.Status, f.Check)
			if f.Detail != "" {
				fmt.Printf(": %s", f.Detail)
			}
			fmt.Println()
		}
	}
	return nil
}

func statusGlyph(status doctor.CheckStatus) string {
	switch status {
	case doctor.StatusOK:
		return "[ok]"
	case doctor.StatusWarn:
		return "[warn]"
	case doctor.StatusFail:
		return "[fail]"
	}
	return "[?]"
}

// buildDaemonInfo queries the running daemon (if any) and returns a
// DaemonInfo for the doctor checks. If no daemon is reachable, returns an
// empty DaemonInfo so checks render a sensible "no daemon" message.
func buildDaemonInfo(ctx context.Context, _ types.WorkspaceRepository) doctor.DaemonInfo {
	sockDir := proc.SockDir()
	di := doctor.DaemonInfo{SockDir: sockDir}

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
	di.InUse = reply.InUse
	di.Waiting = reply.Waiting
	for _, w := range reply.Workspaces {
		di.Workspaces = append(di.Workspaces, doctor.LoadedWorkspace{
			Root:       w.Root,
			LoadedAt:   w.LoadedAt,
			LastAccess: w.LastAccess,
		})
	}
	return di
}
