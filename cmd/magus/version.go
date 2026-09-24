package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/proc"
)

// unknownVersion is the unstamped-build default, and the dev-build sentinel the proc
// adoption gate keys on to fingerprint one (proc.devVersionSentinel); keep the two in
// sync. Named because a daemon reports it too, and the two must not be compared.
const unknownVersion = "unknown"

// version, commit, and buildDate are injected by the linker at build time:
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc123 -X main.buildDate=2026-05-06"
var (
	version   = unknownVersion
	commit    = unknownVersion
	buildDate = unknownVersion
	// builtBy is the CI workflow that produced this binary ($GITHUB_WORKFLOW_REF),
	// empty for a local build. A CLAIM, not proof (a signature is what proves
	// provenance), but empty is the honest default, so a local build never asserts a
	// pedigree it does not have. For a keyless-signed artifact it should equal the
	// job_workflow_ref in the certificate, which is what makes the two cross-checkable.
	builtBy = ""
)

// versionOutput is the structured view of the build stamp. Engine is unconditional
// here even though the text form gates it behind -v: a verbosity flag is a reading
// convenience for a human, and a caller parsing json should not have to discover that
// a field exists only at a higher verbosity.
type versionOutput struct {
	Version   string `json:"version"    yaml:"version"`
	Commit    string `json:"commit"     yaml:"commit"`
	BuildDate string `json:"build_date" yaml:"build_date"`
	// No omitempty: a local build reports built_by as "" rather than dropping the key,
	// so `-o json` and `-o template` see one record shape either way.
	BuiltBy string `json:"built_by"   yaml:"built_by"`
	Engine  string `json:"engine"     yaml:"engine"`
	// ServerVersion is what the server reported. A POINTER because the field has two
	// different silences and one spelling for both: NIL is "the probe never ran"
	// (--client, or an output format that does not render it) and the key is dropped,
	// while a non-nil empty string is "the probe ran and nothing answered".
	//
	// omitzero, not omitempty: under json/v2 omitempty drops a pointer to "" as well as
	// a nil one, which is exactly the collapse this pointer exists to undo. omitzero
	// drops only the nil.
	ServerVersion *string `json:"server,omitzero" yaml:"server,omitempty"`
}

// daemonProbe is the server half of `magus version`: what, if anything, the server said
// when asked.
type daemonProbe struct {
	// version is what it reported, "" when nothing answered.
	version string
}

// daemonProbeTimeout bounds the whole server half. Far under proc's own 5s status
// deadline because `magus version` is a scriptable command: an absent or wedged server
// must cost a blink, not seconds.
const daemonProbeTimeout = 500 * time.Millisecond

func runVersion(ctx context.Context, args []string) error {
	var vf *gen.VersionFlags
	// version is dispatched straight from main, so without cmdParse the global
	// display flags are never bound and -o is silently inert in either position.
	if _, err := cmdParse("version", args, func(fs *flag.FlagSet) {
		vf = gen.BindVersion(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus version [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the version, commit, and build date stamped into this binary,")
			fmt.Fprintln(os.Stderr, "plus the version of the server when one is running.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	}); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	out := versionOutput{Version: version, Commit: commit, BuildDate: buildDate, BuiltBy: builtBy, Engine: "buzz"}
	// -o name prints the bare version and nothing else, so the round-trip would be paid
	// for a field nothing renders, and that form is what a CI step compares against a
	// pin, which is exactly where a server that is slow to answer must not be felt.
	var probe daemonProbe
	probed := !vf.Client && opts.Format != outputName
	if probed {
		probe = probeDaemonVersion(ctx)
		out.ServerVersion = &probe.version
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// The scriptable scalar: `magus version -o name` is what a CI step compares
		// against a pin, so it is the bare version with nothing to strip.
		return emitNames([]string{out.Version})
	}

	fmt.Printf("magus %s (%s) built %s\n", out.Version, out.Commit, out.BuildDate)
	if out.BuiltBy != "" {
		fmt.Printf("built by: %s\n", out.BuiltBy)
	}
	if probed {
		fmt.Println(daemonLine(probe, out.Version))
	}
	if hasVerboseFlag(args) {
		fmt.Printf("engine: %s\n", out.Engine)
	}
	return nil
}

// probeDaemonVersion asks the server for its version. Failures are silent: a missing
// server is the normal case, and the build stamp this command exists to print does not
// depend on one.
//
// Only the server's own socket is asked, never a scanned per-process proc server: one of
// those belongs to whatever invocation spawned it (very possibly an unrelated checkout's
// in-flight run), and reporting it as the server would state something false.
func probeDaemonVersion(ctx context.Context) daemonProbe {
	ctx, cancel := context.WithTimeout(ctx, daemonProbeTimeout)
	defer cancel()

	reply, err := proc.QueryStatus(ctx, resolveServerAddr(""))
	if err != nil {
		return daemonProbe{}
	}
	if reply.Version == "" {
		// A server predating the field still answered, so it is running and did not say
		// which version it is. Kept distinct from the client's own "unknown" sentinel by
		// daemonLine, which never compares this case against the client.
		return daemonProbe{version: unknownVersion}
	}
	return daemonProbe{version: reply.Version}
}

// daemonLine renders the server half of the text form, against the client's own version.
//
// The unknown case is its own line rather than a comparison: an unstamped client also
// reports "unknown", so an unstamped client talking to an unstamped server would have
// read as "server: unknown", the same line two matching stamped builds print.
func daemonLine(p daemonProbe, client string) string {
	switch {
	case p.version == "":
		return "server: not running"
	case p.version == unknownVersion:
		return "server: running, version not reported"
	case p.version != client:
		return fmt.Sprintf("server: %s (differs from this client)", p.version)
	default:
		return "server: " + p.version
	}
}

// hasVerboseFlag reports whether args contains -v or --verbose.
func hasVerboseFlag(args []string) bool {
	for _, a := range args {
		if a == "-v" || a == "--verbose" {
			return true
		}
	}
	return false
}
