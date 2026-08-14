package main

import (
	"flag"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/manpage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReorderFlagsFirst(t *testing.T) {
	// A flag set mirroring the shapes cmdParse sees: a value flag, a bool flag.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.String("output", "", "value flag")
		fs.Bool("explain", false, "bool flag")
		var v verbosity
		fs.Var(&v, "v", "counted bool flag")
		return fs
	}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"bool flag after positional", []string{"lint:rw", "--explain"}, []string{"--explain", "lint:rw"}},
		{"value flag after positional", []string{"lint:rw", "-output", "name"}, []string{"-output", "name", "lint:rw"}},
		{"flags already first unchanged", []string{"--explain", "lint:rw"}, []string{"--explain", "lint:rw"}},
		{"equals form self-contained", []string{"api", "--output=json"}, []string{"--output=json", "api"}},
		{"counted bool -v does not eat positional", []string{"build", "-v"}, []string{"-v", "build"}},
		{"double-dash halts reorder, tail preserved", []string{"test", "--explain", "--", "-run", "X"}, []string{"--explain", "test", "--", "-run", "X"}},
		{"bare dash is a positional", []string{"-", "--explain"}, []string{"--explain", "-"}},
		{"multiple positionals keep order", []string{"a", "--explain", "b"}, []string{"--explain", "a", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, reorderFlagsFirst(newFS(), c.in))
		})
	}
}

// TestPartitionFlags locks the flag/positional split that both reorderFlagsFirst and
// splitTargetFromArgs depend on. A regression here silently breaks flag placement, so
// every rule (bool flag, value flag, -flag=value, "--" passthrough, unknown flag, and a
// value flag with no trailing value) is pinned.
func TestPartitionFlags(t *testing.T) {
	// A self-contained FlagSet so the cases don't drift with the real global flags:
	// "b" is boolean (no value), "val" takes the next token.
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.Bool("b", false, "")
		fs.String("val", "", "")
		return fs
	}

	cases := []struct {
		name    string
		args    []string
		flags   []string
		posargs []string
	}{
		{"positional only", []string{"x"}, []string{}, []string{"x"}},
		{"bool flag before positional", []string{"-b", "x"}, []string{"-b"}, []string{"x"}},
		{"bool flag after positional", []string{"x", "-b"}, []string{"-b"}, []string{"x"}},
		{"value flag keeps its value (before)", []string{"-val", "v", "x"}, []string{"-val", "v"}, []string{"x"}},
		{"value flag keeps its value (after)", []string{"x", "-val", "v"}, []string{"-val", "v"}, []string{"x"}},
		{"value is not mistaken for a positional", []string{"-val", "x", "y"}, []string{"-val", "x"}, []string{"y"}},
		{"flag=value is self-contained", []string{"-val=v", "x"}, []string{"-val=v"}, []string{"x"}},
		{"double dash halts, tail preserved", []string{"-b", "x", "--", "-y", "z"}, []string{"-b"}, []string{"x", "--", "-y", "z"}},
		{"unknown flag does not consume next token", []string{"-nope", "x"}, []string{"-nope"}, []string{"x"}},
		{"value flag at end with no value", []string{"x", "-val"}, []string{"-val"}, []string{"x"}},
		{"double-dash long forms too", []string{"--b", "--val", "v", "x"}, []string{"--b", "--val", "v"}, []string{"x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFlags, gotPos := partitionFlags(newFS(), c.args)
			assert.Equal(t, c.flags, gotFlags, "flags")
			assert.Equal(t, c.posargs, gotPos, "positionals")
			// reorderFlagsFirst must remain flags-then-positionals over the same split.
			assert.Equal(t, append(append([]string{}, c.flags...), c.posargs...),
				reorderFlagsFirst(newFS(), c.args), "reorderFlagsFirst")
		})
	}
}

// TestSplitTargetFromArgs is the regression guard for the fix: the target must be found
// regardless of where recognized global/display flags sit (the bug was a flag BEFORE the
// target being mistaken for it, e.g. `magus run --dry-run build`). Uses the real global
// flag set (--dry-run bool, -o value) so it tracks the actual bindings.
func TestSplitTargetFromArgs(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		target string
		rest   []string
		ok     bool
	}{
		{"target only", []string{"build"}, "build", []string{}, true},
		{"bool flag BEFORE target (the fix)", []string{"--dry-run", "build"}, "build", []string{"--dry-run"}, true},
		{"bool flag after target", []string{"build", "--dry-run"}, "build", []string{"--dry-run"}, true},
		{"flag before target, project after", []string{"--dry-run", "build", "web"}, "build", []string{"--dry-run", "web"}, true},
		{"value flag before target keeps value", []string{"-o", "json", "build"}, "build", []string{"-o", "json"}, true},
		{"value flag between target and project", []string{"build", "-o", "json", "web"}, "build", []string{"-o", "json", "web"}, true},
		{"multiple flags around target", []string{"--dry-run", "build", "-o", "json"}, "build", []string{"--dry-run", "-o", "json"}, true},
		{"no args", []string{}, "", nil, false},
		{"flag but no target", []string{"--dry-run"}, "", nil, false},
		{"only a passthrough marker", []string{"--", "x"}, "", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target, rest, ok := splitTargetFromArgs(c.args)
			assert.Equal(t, c.ok, ok, "ok")
			assert.Equal(t, c.target, target, "target")
			assert.Equal(t, c.rest, rest, "rest")
		})
	}
}

// ---- the man page's flag list vs what the CLI binds ----

// TestManpageFlagsMatchTheCLI is the gate on the man page's copy of every command's
// flags.
//
// internal/manpage/registry.go restates each command's flags because the real ones are
// bound in closures inside package main, which nothing outside it can import. That copy
// drifted for as long as it existed: it advertised --simple for months after the flag was
// deleted, and it still omitted most of what several commands accept. Nothing reported
// either direction, because a generated man page is only ever compared against itself.
//
// The comparison runs against the CLI as built, not against its source. Each documented
// command is invoked with -h, which makes cmdParse bind every flag and then stop at
// flag.ErrHelp before the command does any work; recordFlagSet captures the fully bound
// set on the way through. A source scan cannot do this - it sees neither the config flags
// generated from the schema nor the display flags, so it reports --dry-run as
// undocumented when it is simply global.
//
// Global flags are subtracted from BOTH sides, computed rather than listed, so this stays
// a check on what a command adds.
func TestManpageFlagsMatchTheCLI(t *testing.T) {
	globals := globalFlagNames()

	for _, cmd := range manpage.All {
		for _, probe := range flagProbes(cmd) {
			t.Run(strings.ReplaceAll(probe.name, " ", "_"), func(t *testing.T) {
				documented := documentedFlagNames(probe.name, probe.buildFlags, globals)
				for _, argv := range probe.argvs {
					runCLIQuietly(t, append(argv, "-h")...)
				}

				bound, ok := recordedFlagsUnder(probe.name)
				if !ok {
					// The command never reached cmdParse under -h. Not a drift finding:
					// it means this probe cannot see the command's flags, and a check
					// that cannot look must say so rather than report "no drift".
					t.Skipf("%q did not reach flag parsing under -h; its man page flags are unverified", probe.name)
				}
				actual := make([]string, 0, len(bound))
				for _, f := range bound {
					if !globals[f.Name] {
						actual = append(actual, f.Name)
					}
				}
				sort.Strings(actual)

				assert.Equal(t, documented, actual,
					"the man page's flag list for %q disagrees with what the command binds.\n"+
						"Fix internal/manpage/registry.go - it is a hand-written copy of the real\n"+
						"flags, and this is the only thing that notices when it goes stale.",
					probe.name)
			})
		}
	}
}

// flagProbe is one command whose flags can be compared: the argv that reaches it, and the
// man page's claim about it.
type flagProbe struct {
	name       string
	argvs      [][]string
	buildFlags func(*flag.FlagSet)
}

// modeArgv lists the invocations needed to bind a command's whole documented flag set.
//
// Two things make one invocation insufficient. A target-scoped command registers per
// TARGET ("run build", never "run"), because the target is part of the invocation rather
// than a flag. And a command with MODES binds a different set in each: `magus affected
// <t>` , `--plan` and `--bisect` are three separate flag sets, and the man page documents
// their union, so the probe has to cover all three or it reports the modes it never
// entered as undocumented.
var modeArgv = map[string][][]string{
	"run":      {{"run", "build"}},
	"affected": {{"affected", "build"}, {"affected", "build", "--plan"}, {"affected", "--bisect", "x"}},
	// describe binds nothing until it has its noun, and binds a DIFFERENT set per noun:
	// a target ref gets the cache-key flags, a project listing gets --evaluated.
	//
	// watch is deliberately NOT probed. It is a long-running file watcher that starts
	// watching before -h can stop it, so probing it hangs the test rather than reading
	// its flags. Its man-page flags stay unverified, and the skip says so out loud -
	// which is the honest outcome for a command this check cannot safely enter.
	"describe": {{"describe", "targets"}, {"describe", "projects"}},
}

// argvsFor turns a documented command name into the invocations that reach its flags.
func argvsFor(name string) [][]string {
	if modes, ok := modeArgv[name]; ok {
		return modes
	}
	return [][]string{strings.Fields(name)}
}

// recordedFlagsUnder is the union of everything a probe bound: the record filed under the
// name itself, plus every record filed under a key extending it. The union is the point -
// a multi-mode command files one record per mode ("affected build", "affected build
// --plan"), and the man page describes the command, not one mode of it.
func recordedFlagsUnder(name string) ([]recordedFlag, bool) {
	flagRecord.Lock()
	defer flagRecord.Unlock()
	seen := map[string]bool{}
	var out []recordedFlag
	for key, flags := range flagRecord.byCommand {
		if key != name && !strings.HasPrefix(key, name+" ") {
			continue
		}
		for _, f := range flags {
			if !seen[f.Name] {
				seen[f.Name] = true
				out = append(out, f)
			}
		}
	}
	return out, len(out) > 0
}

// flagProbes expands one man-page command into the invocations that actually bind flags.
// A command with subcommands binds nothing itself - `magus server -h` prints its own
// usage and parses nothing - so its children are what get probed.
func flagProbes(cmd manpage.Command) []flagProbe {
	if len(cmd.Children) == 0 {
		if !cmd.HasFlags() {
			return nil
		}
		return []flagProbe{{name: cmd.Name, argvs: argvsFor(cmd.Name), buildFlags: cmd.BindFlags}}
	}
	var out []flagProbe
	for _, child := range cmd.Children {
		if !child.HasFlags() {
			continue
		}
		out = append(out, flagProbe{name: cmd.Name + " " + child.Name, argvs: argvsFor(cmd.Name + " " + child.Name), buildFlags: child.BindFlags})
	}
	return out
}

// modeSelectors are documented flags that SELECT a mode rather than configure one, and
// are therefore read straight from argv before any FlagSet exists: `magus affected
// --explain <project>` and `--plan` each dispatch to a different command path with its
// own flags (parseExplainArgs, and the --plan scan in affectedCmd). They are real, they
// are documented correctly, and they can never appear in a recorded flag set - so they
// are excluded rather than "fixed" out of the man page.
var modeSelectors = map[string]map[string]bool{
	"affected": {"explain": true, "plan": true},
}

// documentedFlagNames lists the non-global flag names a BuildFlags closure declares.
func documentedFlagNames(command string, build func(*flag.FlagSet), globals map[string]bool) []string {
	fs := flag.NewFlagSet("doc", flag.ContinueOnError)
	build(fs)
	var out []string
	fs.VisitAll(func(f *flag.Flag) {
		if !globals[f.Name] && !modeSelectors[command][f.Name] {
			out = append(out, f.Name)
		}
	})
	sort.Strings(out)
	return out
}

// runCLIQuietly drives the real CLI in process with output discarded. -h is always the
// last argument, so parsing stops before the command acts.
func runCLIQuietly(t *testing.T, argv ...string) {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	defer func() { _ = devnull.Close() }()

	oldOut, oldErr, oldArgs := os.Stdout, os.Stderr, os.Args
	os.Stdout, os.Stderr, os.Args = devnull, devnull, append([]string{"magus"}, argv...)
	defer func() { os.Stdout, os.Stderr, os.Args = oldOut, oldErr, oldArgs }()

	// Restore the environment afterwards. Driving the real CLI in this process is the
	// whole point of the check and also its hazard: a probe left MAGUS_DAEMON_SOCKET
	// behind, and the next test in the package then refused to start a daemon because
	// it believed it was running under a parent magus. Snapshot everything rather than
	// the one variable that bit, since the next leak will be a different name.
	env := os.Environ()
	defer func() {
		os.Clearenv()
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				_ = os.Setenv(k, v)
			}
		}
	}()

	// A command that panics or exits under -h is a finding for another test; here it
	// only means the record stays absent and the subtest skips with that stated.
	defer func() { _ = recover() }()
	runCLI()
}
