package commands

import (
	"context"
	"flag"
	"slices"
	"strings"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/log/quiet"
)

// DispatchProfile describes which pre-dispatch phases a subcommand needs.
type DispatchProfile struct {
	NeedsConfig    bool // load magus.yaml + env vars
	NeedsForward   bool // attempt forward to a running server
	NeedsWorkspace bool // call loadMagus + start per-process proc server
	// SpawnsWork marks the invocations that spend host capacity: target runs, and the
	// few non-run paths that load a workspace the same way (session --brief). Only these
	// start the broker and claim the machine budget: `magus ls` costs the same however
	// loaded the machine is, and starting a broker for one would make every read command
	// spawn a background process.
	SpawnsWork bool
	// HostOnly marks a command whose stderr only a host's glue reads, never a person, so
	// its display is quiet.
	HostOnly bool
}

// SchedulesSteps reports whether args name a verb that seats its own steps, and so takes
// claims of its own rather than running inside the parent step's seat. `ci` is a target
// these verbs reach, not a verb. valueFlags is GlobalValueFlags' set.
func SchedulesSteps(args []string, valueFlags map[string]bool) bool {
	sub, _ := PeekSub(quiet.ExpandVerbosityArgs(args), valueFlags)
	return sub == "run" || sub == "affected"
}

// GlobalValueFlags is the set of "-name"/"--name" tokens for every value-taking
// global flag, derived once from the real bindings rather than hand-listed, so PeekSub can
// never drift from them: bind registers the config-generated flags and the display flags on
// the set it is given. Two bootstrap flags are added explicitly: root and config
// precede config loading, so they are not part of the generated set. Deriving it
// closes the latent gap where a value global flag missing from the old hand-kept
// list made `magus --cache-dir x run` misread x as the subcommand.
func GlobalValueFlags(bind func(*flag.FlagSet)) map[string]bool {
	fs := flag.NewFlagSet("global", flag.ContinueOnError)
	bind(fs)
	// Both short forms belong here beside their long names: PeekSub reads this set to
	// know a token consumes the next one, and a missing -C made `magus -C /ws run`
	// misread the path as the subcommand.
	out := map[string]bool{
		"-root": true, "--root": true, "-C": true, "--C": true,
		"-config": true, "--config": true, "-c": true, "--c": true,
	}
	fs.VisitAll(func(f *flag.Flag) {
		if !FlagIsBool(f) {
			out["-"+f.Name] = true
			out["--"+f.Name] = true
		}
	})
	return out
}

// PeekSub returns the subcommand and trailing args, scanning past global flags, which
// valueFlags names. Intentionally approximate: disagreement with fs.Parse costs extra work,
// not correctness.
func PeekSub(args []string, valueFlags map[string]bool) (sub string, subArgs []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if len(a) == 0 {
			i++
			continue
		}
		// --flag=value form: consume the whole token.
		if a[0] == '-' && strings.ContainsRune(a, '=') {
			i++
			continue
		}
		// -version/--version is a subcommand in a flag's clothing: the stdlib flag
		// package special-cases -h/-help this way already (fs.Parse itself returns
		// ErrHelp for those), but has nothing built in for version, so without this
		// `magus --version` fell through to the generic dash-skip below, PeekSub
		// returned no subcommand at all, and the later fs.Parse died on an
		// unregistered flag ("flag parse failed") instead of printing the version.
		if a == "-version" || a == "--version" {
			return "version", args[i+1:]
		}
		// --flag value form: consume both tokens.
		if valueFlags[a] && i+1 < len(args) {
			i += 2
			continue
		}
		// Any other dash-prefixed token is a boolean/counted flag (-v, -vv).
		if a[0] == '-' {
			i++
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// IsUsageOnlyInvocation reports whether a run/affected invocation only wants usage
// text: no target given, or the first token is a help flag. It mirrors the guards at
// the top of runTarget and affected exactly, so the forward decision agrees with what
// those handlers do locally.
func IsUsageOnlyInvocation(subArgs []string) bool {
	if len(subArgs) == 0 {
		return true
	}
	switch subArgs[0] {
	case "-h", "--help", "help":
		return true
	}
	return false
}

// WantsUsage reports whether a subcommand's args ask for its help text.
//
// Distinct from IsUsageOnlyInvocation, which additionally treats NO arguments as a usage
// request. That is right for run and affected, whose first positional is required, and wrong
// for everything else: bare `magus diff` is a real invocation.
//
// Scanning stops at "--", past which the tokens belong to a forwarded tool: a run that
// forwards `-h` after the marker is asking the test binary for help, not magus.
func WantsUsage(subArgs []string) bool {
	for i, a := range subArgs {
		if a == "--" {
			return false
		}
		// -h and --help are unambiguous: no subcommand takes either as a positional, so
		// they mean help wherever they sit. `magus diff --impact -h` is the case worth
		// catching.
		if a == "-h" || a == "--help" {
			return true
		}
		// The bare word is only a help request in the FIRST position. Elsewhere it is
		// ordinary data: `magus notes get help` shows a note named help. Treating it as
		// usage would skip the workspace preload for a real invocation.
		if a == "help" && i == 0 {
			return true
		}
	}
	return false
}

// HasDetachFlag reports whether argv carries --detach, in every spelling the flag
// package accepts. Scanning stops at "--", past which the tokens belong to a forwarded
// tool rather than to magus.
func HasDetachFlag(args []string) bool { return hasFlag(args, flaggen.FlagRunDetach) }

// HasBriefFlag reports whether a session invocation selected --brief. The profile
// decision runs before commandParse, so the flag is peeked the same way --detach is.
func HasBriefFlag(args []string) bool { return hasFlag(args, "brief") }

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if key, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); key == name {
			return true
		}
	}
	return false
}

// IsDeclaredRun is the second, deliberately narrow admission path into the job dispatcher: a
// plain `run <target> <project>` whose target the workspace's own magusfile declares for that
// project. The review app submits these so a reader can run a project's tests against the code
// they are looking at.
//
// It admits strictly less than a terminal's `magus run`: exactly three tokens, no flags, no
// charms, no `spell::op` form, and a target name that has to appear in the generated target graph
// rather than being taken from the caller. So the property the job dispatcher exists to hold (a
// job RPC can never name an arbitrary command) still holds by construction: the allowlist is the
// magusfile, not the request. declared lists the targets the workspace declares for a project,
// and is nil when no workspace is loaded, which refuses every run.
func IsDeclaredRun(ctx context.Context, args []string, declared func(ctx context.Context, project string) []string) bool {
	if len(args) != 3 || args[0] != "run" {
		return false
	}
	target, project := args[1], args[2]
	if strings.HasPrefix(target, "-") || strings.Contains(target, ":") {
		return false
	}
	if declared == nil {
		return false
	}
	return slices.Contains(declared(ctx, project), target)
}
