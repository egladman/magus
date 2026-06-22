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
	fmt.Printf("\nsummary: %d ok, %d fail\n", out.Summary.OK, out.Summary.Fail)
	return nil
}

func statusGlyph(status doctor.CheckStatus) string {
	switch status {
	case doctor.StatusOK:
		return "[ok]"
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
