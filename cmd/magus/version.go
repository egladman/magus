package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/contract"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// selfBuild is this binary's build identity, [buildinfo.Self].
func selfBuild() types.MagusBuild { return buildinfo.Self() }

// version is the version go-build stamped into buildinfo, beside its commit stamp. A var
// so a test can name another.
var (
	version = buildinfo.Version()
	// builtBy is the CI workflow that produced this binary, as the build was told it
	// (release.yaml passes its workflow ref in BUILT_BY), empty for a local build. A CLAIM, not proof (a signature is what proves
	// provenance), but empty is the honest default, so a local build never asserts a
	// pedigree it does not have. For a keyless-signed artifact it should equal the
	// job_workflow_ref in the certificate, which is what makes the two cross-checkable.
	builtBy = ""
)

// serverProbeTimeout bounds the whole server half. Far under proc's own 5s status
// deadline because `magus version` is a scriptable command: an absent or wedged server
// must cost a blink, not seconds.
const serverProbeTimeout = 500 * time.Millisecond

func runVersion(ctx context.Context, args []string) error {
	var vf *flaggen.VersionFlags
	// version is dispatched straight from main, so without commandParse the global
	// display flags are never bound and -o is silently inert in either position.
	if _, err := commandParse("version", args, func(fs *flag.FlagSet) {
		vf = flaggen.BindVersion(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus version [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the version, commit, and build date stamped into this binary,")
			fmt.Fprintln(os.Stderr, "plus the version of the server when one is running.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	}); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	out := buildinfo.NewVersionOutput(selfBuild(), builtBy)
	if out.Embedded.ContractSHA256, err = contract.Binary(); err != nil {
		return err
	}
	// -o name prints the bare version and nothing else, so the round-trip would be paid
	// for a field nothing renders, and that form is what a CI step compares against a
	// pin, which is exactly where a server that is slow to answer must not be felt.
	var probe buildinfo.ServerProbe
	probed := !vf.Client && opts.Format != outputName
	if probed {
		probe = probeServerVersion(ctx)
		out.ServerVersion = &probe.Version
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// The scriptable scalar: `magus version -o name` is what a CI step compares
		// against a pin, so it is the bare version with nothing to strip.
		return emitNames(os.Stdout, []string{out.Version})
	case outputText:
	}

	fmt.Printf("magus %s (%s) built %s\n", out.Version, out.Commit, out.BuildDate)
	if out.BuiltBy != "" {
		fmt.Printf("built by: %s\n", out.BuiltBy)
	}
	if probed {
		fmt.Println(buildinfo.ServerLine(probe, out.Version))
	}
	if buildinfo.HasVerboseFlag(args) {
		fmt.Printf("engine: %s\n", out.Engine)
	}
	return nil
}

// probeServerVersion asks the server for its version. Failures are silent: a missing
// server is the normal case, and the build stamp this command exists to print does not
// depend on one.
//
// Only the server's own socket is asked, never a scanned per-process proc server: one of
// those belongs to whatever invocation spawned it (very possibly an unrelated checkout's
// in-flight run), and reporting it as the server would state something false.
func probeServerVersion(ctx context.Context) buildinfo.ServerProbe {
	ctx, cancel := context.WithTimeout(ctx, serverProbeTimeout)
	defer cancel()

	reply, err := proc.QueryStatus(ctx, resolveServerAddr(""))
	if err != nil {
		return buildinfo.ServerProbe{}
	}
	if reply.Version == "" {
		// A server predating the field still answered, so it is running and did not say
		// which version it is. Kept distinct from the client's own "unknown" sentinel by
		// buildinfo.ServerLine, which never compares this case against the client.
		return buildinfo.ServerProbe{Version: buildinfo.UnknownVersion}
	}
	return buildinfo.ServerProbe{Version: reply.Version}
}
