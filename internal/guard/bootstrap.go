package guard

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"golang.org/x/mod/modfile"
)

// ownModule is the module this guard was compiled from, read off a type rather than
// spelled out so a renamed fork still recognizes its own tree.
var ownModule = reflect.TypeFor[magus.Magus]().PkgPath()

// goCall is a go command read the way cmd/go reads it: -C in either position it accepts
// (`go -C dir build`, `go build -C dir`), and the argv from the subcommand on with that
// flag removed.
type goCall struct {
	chdir string
	args  []string
}

func readGoCall(c hint.Invocation) (goCall, bool) {
	if c.Name != "go" {
		return goCall{}, false
	}
	valued := valuedGlobalFlags["go"]
	args := c.Args
	var call goCall
	if dir, ok := valuedOperand(valued, args); ok {
		call.chdir, args = dir, args[chdirFlagTokens(args):]
	}
	if len(args) == 0 || !subcommandWord(args[0]) {
		return goCall{}, false
	}
	rest := args[1:]
	if call.chdir == "" {
		if dir, ok := valuedOperand(valued, rest); ok {
			call.chdir, rest = dir, rest[chdirFlagTokens(rest):]
		}
	}
	call.args = append([]string{args[0]}, rest...)
	return call, true
}

// chdirFlagTokens is how many words the -C flag leading args spans.
func chdirFlagTokens(args []string) int {
	if strings.Contains(args[0], "=") {
		return 1
	}
	return 2
}

// buildRoot is the directory the call builds in: its -C resolved against cwd, or cwd.
func (g goCall) buildRoot(cwd string) string {
	switch {
	case g.chdir == "":
		return filepath.Clean(cwd)
	case filepath.IsAbs(g.chdir):
		return filepath.Clean(g.chdir)
	}
	return filepath.Join(cwd, g.chdir)
}

// bootstrapArgv is the one command a checkout of magus with no binary runs to get one: the
// real go-build target, driven by a magus compiled on the fly. The target's own key cannot
// express the embedded-spell ordering, so its cache is bypassed; Go's content-addressed
// build cache stays on, and -trimpath matches the target's build so packages compile once.
var bootstrapArgv = []string{"go", "run", "-trimpath", "./cmd/magus", "run", "go-build", "--no-cache", "."}

var bootstrapCommand = strings.Join(bootstrapArgv, " ")

const bootstrapWhy = "it runs the real go-build target (generate steps and stamped link) past a magus cache that cannot key it, while Go's build cache stays on."

// bootstrapsMagus reports the one command the raw-tool rule exempts, bootstrapArgv, run in
// the root it builds. The package may be spelled `cmd/magus` with or without `./` or a
// trailing slash; any other flag, package or program argument is an ordinary `go run` and
// stays denied.
func (g goCall) bootstrapsMagus() bool {
	want := bootstrapArgv[1:]
	if len(g.args) != len(want) {
		return false
	}
	for i, a := range g.args {
		if i == 2 {
			a = path.Clean(a)
			if a != "cmd/magus" {
				return false
			}
			continue
		}
		if a != want[i] {
			return false
		}
	}
	return true
}

// linksMagus reports a bare `go build` of cmd/magus, which links a binary without the
// target's generate steps or stamp.
func (g goCall) linksMagus() bool {
	if len(g.args) == 0 || g.args[0] != "build" {
		return false
	}
	return slices.ContainsFunc(g.args[1:], func(a string) bool { return path.Clean(a) == "cmd/magus" })
}

// ownSourceRoot reports whether dir is the root of a checkout of this guard's module.
func ownSourceRoot(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	return err == nil && modfile.ModulePath(data) == ownModule
}

// hasMagusBinary reports whether root already holds a `magus`. Anything but a clean
// "does not exist" counts as present, so an unreadable root keeps the deny.
func hasMagusBinary(root string) bool {
	_, err := os.Lstat(filepath.Join(root, "magus"))
	return !errors.Is(err, fs.ErrNotExist)
}

const ownRebuild = "`./magus run go-build .`"

// ownBuildOutcome is the correction rankOwnBuild applies to a raw-tool deny of a go
// command in a checkout of magus: whether the call is the bootstrap, whether root already
// has a binary, whether the call shared its line with something else, and the verdict to
// use when the bootstrap stands alone in a root with none.
type ownBuildOutcome struct {
	root string
	// bootstrapArgv is the bootstrap as the call's directory runs it: with -C when root
	// lies elsewhere.
	bootstrapArgv []string
	bootstrap     bool
	link          bool
	hasBinary     bool
	multipleCmds  bool
	advisory      ShellVerdict
}

// apply layers this outcome onto v, the raw-tool deny it refines.
func (o *ownBuildOutcome) apply(v ShellVerdict) ShellVerdict {
	switch {
	case o.hasBinary && (o.bootstrap || o.link):
		v.Deny += "\nNot a bootstrap: " + o.root + " already has a magus binary. Rebuild with " + ownRebuild + "."
		return v
	case o.hasBinary:
		return v
	case o.bootstrap && o.multipleCmds:
		v.Deny += "\nThe bootstrap is exempt only alone on its line."
		return v
	case o.bootstrap:
		return o.advisory
	}
	v.Deny += "\n" + o.root + " has no magus binary yet. Get one with `" + strings.Join(o.bootstrapArgv, " ") + "`: " + bootstrapWhy
	v.Next, v.Lead = nil, ""
	return v.withRemedy("This checkout has no magus binary yet; one command builds it.",
		hint.NextForDenyRemedy(string(denyRuleRawTool), o.bootstrapArgv, bootstrapWhy))
}

// ownBuildOutcomeFor builds the outcome rankOwnBuild layers onto the deny for denied.
func ownBuildOutcomeFor(deps Dependencies, command string, d Dialect, denied hint.Invocation, call goCall, root string, multipleCmds bool, cwd string) *ownBuildOutcome {
	where := "this checkout"
	argv := bootstrapArgv
	if root != filepath.Clean(cwd) {
		where = root
		argv = append([]string{"go", "-C", root}, bootstrapArgv[1:]...)
	}
	return &ownBuildOutcome{
		root:          root,
		bootstrapArgv: argv,
		bootstrap:     call.bootstrapsMagus(),
		link:          call.linksMagus(),
		hasBinary:     hasMagusBinary(root),
		multipleCmds:  multipleCmds,
		advisory: strengthenWithWorkspace(ShellVerdict{
			Context: "magus workspace: bootstrap allowed, since " + where + " has no magus binary yet: " + bootstrapWhy + " Use ./magus from then on.",
			Rule:    denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(denied)},
		}, matchWorkspaceShell(deps.ShellRules, command, d)),
	}
}

// ownBuildVerdict is the BOOTSTRAP correction for a go command aimed at magus's own
// module: a fresh checkout has no ./magus, and every route to one runs through magus or
// a raw toolchain command. bootstrapArgv, alone on its line, in a root with no binary
// yet, is advised through instead of denied, and every other raw go command there is
// served it. Nil when the line holds no go command denied in a checkout of magus.
//
// A -C outside the workspace passes the pure rule, since a foreign tree is not its to
// funnel; a -C into another checkout of magus is this repository's policy to judge
// (hack/policy/guard.buzz), bootstrap included.
//
// It reads the filesystem, so it lives beside Judge rather than inside Evaluate.
func ownBuildVerdict(deps Dependencies, cwd, command string, d Dialect) *ownBuildOutcome {
	if cwd == "" {
		return nil
	}
	cmds, parsed := ParseCommandsDialect(command, d)
	if !parsed {
		return nil
	}
	i := slices.IndexFunc(cmds, func(c hint.Invocation) bool { return rawToolDenied(deps, c) })
	if i < 0 {
		return nil
	}
	call, ok := readGoCall(cmds[i])
	if !ok {
		return nil
	}
	if root := call.buildRoot(cwd); ownSourceRoot(root) {
		return ownBuildOutcomeFor(deps, command, d, cmds[i], call, root, len(cmds) > 1, cwd)
	}
	return nil
}

// rankOwnBuild refines a raw-tool deny with the bootstrap correction; every other verdict
// passes through untouched.
func rankOwnBuild(v ShellVerdict, oc *ownBuildOutcome) ShellVerdict {
	if oc == nil || v.Rule.Name != denyRuleRawTool || v.Deny == "" {
		return v
	}
	return oc.apply(v)
}
