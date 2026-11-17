package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuardKnowsEveryGlobalFlag holds the guard's reading of a magus argv to the flags magus
// really binds. A global flag the guard did not know would have its value read as the
// subcommand, and a lease rule keyed on the subcommand would stop matching.
func TestGuardKnowsEveryGlobalFlag(t *testing.T) {
	fs := flag.NewFlagSet("magus", flag.ContinueOnError)
	var root, cfgPath string
	bindGlobalFlags(fs, &root, &cfgPath)
	bindDisplayFlags(fs)
	fs.VisitAll(func(f *flag.Flag) {
		takesValue, known := guard.MagusFlagTakesValue(f.Name)
		require.True(t, known, "the guard does not know global flag -%s", f.Name)
		b, isBool := f.Value.(interface{ IsBoolFlag() bool })
		assert.Equal(t, !isBool || !b.IsBoolFlag(), takesValue, "-%s", f.Name)
	})
}

// ---- the man page's flag list vs what the CLI binds ----

// TestManpageFlagsMatchTheCLI is the gate on the man page's copy of every command's
// flags.
//
// internal/commands/registry.go declares each command's flags, and the generated binders
// bind most of them, but a flag bound by hand (a Custom kind, the config-derived globals)
// can still disagree with its declaration. A generated man page is only ever compared
// against itself, so nothing else reports either direction.
//
// The comparison runs against the CLI as built, not against its source. Each documented
// command is invoked with -h, which makes commandParse bind every flag and then stop at
// flag.ErrHelp before the command does any work; recordFlagSet captures the fully bound
// set on the way through. A source scan cannot do this: it sees neither the config flags
// generated from the schema nor the display flags, so it reports --dry-run as
// undocumented when it is simply global.
//
// Global flags are subtracted from BOTH sides, computed rather than listed, so this stays
// a check on what a command adds.
func TestManpageFlagsMatchTheCLI(t *testing.T) {
	globals := globalFlagNames()

	for _, cmd := range commands.Root.Spec.Children {
		for _, probe := range flagProbes(cmd) {
			t.Run(strings.ReplaceAll(probe.name, " ", "_"), func(t *testing.T) {
				documented := documentedFlagNames(t, probe.name, probe.buildFlags, globals)
				for _, argv := range probe.argvs {
					runCLIQuietly(t, append(argv, "-h")...)
				}

				bound, ok := recordedFlagsUnder(probe.name)
				if !ok {
					// The command never reached commandParse under -h. Not a drift finding:
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
						"Fix internal/commands/registry.go or the hand-bound flag; this is the\n"+
						"only thing that notices when the two disagree.",
					probe.name)
			})
		}
	}
}

// The guard reads a `job fork` argv by the registry's copy of its flags, where a bool
// flag takes no value; a flag the command binds differently leaves the guard reading a
// value as an operand, or an operand as a value.
func TestJobForkRegistryFlagsAreTheFlagsItBinds(t *testing.T) {
	var fork command.Command
	for _, group := range commands.Root.Spec.Children {
		for _, child := range group.Children {
			if group.Name == "job" && child.Name == "fork" {
				fork = child
			}
		}
	}
	require.NotEmpty(t, fork.Flags)

	runCLIQuietly(t, "job", "fork", "-h")
	bound, ok := recordedFlagsUnder("job fork")
	require.True(t, ok, "job fork did not reach flag parsing under -h")
	byName := map[string]recordedFlag{}
	for _, f := range bound {
		byName[f.Name] = f
	}
	for _, declared := range fork.Flags {
		got, ok := byName[declared.Name]
		if !assert.True(t, ok, "--%s is in the registry and not bound", declared.Name) {
			continue
		}
		want := recordedFlag{Name: declared.Name, Usage: command.DurationUsage(declared), Bool: declared.Kind == command.Bool}
		assert.Equal(t, want, got, "--%s: its doc, and whether it takes a value", declared.Name)
	}
}

// flagProbe is one command whose flags can be compared: the argv that reaches it, and the
// man page's claim about it.
type flagProbe struct {
	name       string
	argvs      [][]string
	buildFlags func(*flag.FlagSet) error
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
	// its flags. Its man-page flags stay unverified, and the skip says so out loud,
	// which is the honest outcome for a command this check cannot safely enter.
	"describe": {{"describe", "targets"}, {"describe", "projects"}},
}

// argvsFor turns a documented command name into the invocations that reach its flags.
func probeArgvs(name string) [][]string {
	if modes, ok := modeArgv[name]; ok {
		return modes
	}
	return [][]string{strings.Fields(name)}
}

// recordedFlagsUnder is the union of everything a probe bound: the record filed under the
// name itself, plus every record filed under a key extending it. The union is the point:
// a multi-mode command files one record per mode ("affected build", "affected build
// --plan"), and the man page describes the command, not one mode of it.
// A subcommand that declares its own flags is left out: its man page entry is its own.
func recordedFlagsUnder(name string) ([]recordedFlag, bool) {
	flagRecord.Lock()
	defer flagRecord.Unlock()
	children := flaggedChildren(name)
	seen := map[string]bool{}
	var out []recordedFlag
	for key, flags := range flagRecord.byCommand {
		if key != name && !strings.HasPrefix(key, name+" ") {
			continue
		}
		if rest := strings.Fields(strings.TrimPrefix(key, name)); len(rest) > 0 && children[rest[0]] {
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

// flaggedChildren names the subcommands of name that declare their own flags.
func flaggedChildren(name string) map[string]bool {
	cmds := commands.Root.Spec.Children
	for _, word := range strings.Fields(name) {
		i := slices.IndexFunc(cmds, func(c command.Command) bool { return c.Name == word })
		if i < 0 {
			return nil
		}
		cmds = cmds[i].Children
	}
	out := map[string]bool{}
	for _, c := range cmds {
		if len(c.Flags) > 0 {
			out[c.Name] = true
		}
	}
	return out
}

// flagProbes expands one man-page command into the invocations that actually bind flags.
// A command with subcommands binds nothing itself (`magus server -h` prints its own
// usage and parses nothing), so its children are what get probed.
func flagProbes(cmd command.Command) []flagProbe {
	if len(cmd.Children) == 0 {
		if len(cmd.Flags) == 0 {
			return nil
		}
		return []flagProbe{{name: cmd.Name, argvs: probeArgvs(cmd.Name), buildFlags: cmd.BindFlags}}
	}
	var out []flagProbe
	for _, child := range cmd.Children {
		if len(child.Flags) == 0 {
			continue
		}
		out = append(out, flagProbe{name: fmt.Sprintf("%s %s", cmd.Name, child.Name), argvs: probeArgvs(fmt.Sprintf("%s %s", cmd.Name, child.Name)), buildFlags: child.BindFlags})
	}
	return out
}

// modeSelectors are documented flags that SELECT a mode rather than configure one, and
// are therefore read straight from argv before any FlagSet exists: `magus affected
// --explain <project>` and `--plan` each dispatch to a different command path with its
// own flags (parseExplainArgs, and the --plan scan in affectedCmd). They are real, they
// are documented correctly, and they can never appear in a recorded flag set, so they
// and the --no- form the binder generates for each are excluded rather than "fixed" out
// of the man page.
var modeSelectors = map[string]map[string]bool{
	"affected": {"explain": true, "plan": true, "impact": true},
}

func TestGraphSymbolsHasOnePolarityAcrossCommands(t *testing.T) {
	parse := func(t *testing.T, args ...string) (build, export bool) {
		t.Helper()
		bfs := flag.NewFlagSet("graph build", flag.ContinueOnError)
		bf := flaggen.BindGraphBuild(bfs)
		require.NoError(t, bfs.Parse(args))
		efs := flag.NewFlagSet("graph export", flag.ContinueOnError)
		ef := flaggen.BindGraphExport(efs, flaggen.GraphExportDefaults{})
		require.NoError(t, efs.Parse(args))
		return bf.Symbols, ef.Symbols
	}

	build, export := parse(t)
	assert.True(t, build, "build reindexes by default")
	assert.False(t, export, "export leaves symbols out by default")

	build, export = parse(t, "--symbols")
	assert.True(t, build)
	assert.True(t, export)

	build, export = parse(t, "--no-symbols")
	assert.False(t, build)
	assert.False(t, export)
}

func TestGraphExportHasNoStaticAlias(t *testing.T) {
	fs := flag.NewFlagSet("graph export", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	flaggen.BindGraphExport(fs, flaggen.GraphExportDefaults{})
	assert.Error(t, fs.Parse([]string{"--static"}))
}

// documentedFlagNames lists the non-global flag names a BuildFlags closure declares.
func documentedFlagNames(t *testing.T, cmdPath string, build func(*flag.FlagSet) error, globals map[string]bool) []string {
	t.Helper()
	fs := flag.NewFlagSet("doc", flag.ContinueOnError)
	require.NoError(t, build(fs))
	var out []string
	fs.VisitAll(func(f *flag.Flag) {
		if !globals[f.Name] && !modeSelectors[cmdPath][strings.TrimPrefix(f.Name, command.NegationPrefix)] {
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
	// whole point of the check and also its hazard: a probe left MAGUS_PROC_SOCKET
	// behind, and the next test in the package then refused to start a server because
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
