package main

import (
	"flag"
	"fmt"
	"os"
)

// version, commit, and buildDate are injected by the linker at build time:
//
//	go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc123 -X main.buildDate=2026-05-06"
//
// The default "unknown" is the dev-build sentinel the proc adoption gate keys on to
// fingerprint an unstamped build (proc.devVersionSentinel); keep the two in sync.
var (
	version   = "unknown"
	commit    = "unknown"
	buildDate = "unknown"
	// builtBy is the CI workflow that produced this binary ($GITHUB_WORKFLOW_REF),
	// empty for a local build. A CLAIM, not proof - a signature is what proves
	// provenance - but empty is the honest default, so a local build never asserts a
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
}

func runVersion(args []string) error {
	// version is dispatched straight from main, so without cmdParse the global
	// display flags are never bound and -o is silently inert in either position.
	if _, err := cmdParse("version", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus version [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the version, commit, and build date stamped into this binary.")
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

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// The scriptable scalar: `magus version -o name` is what a CI step compares
		// against a pin, so it is the bare version with nothing to strip.
		fmt.Println(out.Version)
		return nil
	}

	fmt.Printf("magus %s (%s) built %s\n", out.Version, out.Commit, out.BuildDate)
	if out.BuiltBy != "" {
		fmt.Printf("built by: %s\n", out.BuiltBy)
	}
	if hasVerboseFlag(args) {
		fmt.Printf("engine: %s\n", out.Engine)
	}
	return nil
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
