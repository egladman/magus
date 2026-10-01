package guard

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
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

// magusUtilsGenerators are the cmd/magus-utils subcommands that write generated source, the
// ones the *_generate targets and the go:generate directives they drive run. The rest
// (sign, verify, cut, release-index, diffdemo) are release and demo tools, not part of
// regenerating a tree.
var magusUtilsGenerators = []string{
	"api", "bindings", "boundarylist", "boundaryobjects", "cliflags", "completions", "config",
	"jobschema", "mcptools", "mockassert", "moduledecls", "moduleset", "types",
}

// recoversMagus reports a command that rebuilds a checkout whose committed generated files
// or binary lag its sources: the relink MGS1021 prints, `go build [-trimpath] -o magus
// ./cmd/magus`, a `go generate` of packages inside the checkout, or `go run [-trimpath]
// ./cmd/magus-utils` with one of magusUtilsGenerators.
func (g goCall) recoversMagus() bool {
	if len(g.args) == 0 {
		return false
	}
	args := g.args[1:]
	switch g.args[0] {
	case "build":
		args = trimpathOptional(args)
		return len(args) == 3 && args[0] == "-o" && args[1] == "magus" && path.Clean(args[2]) == "cmd/magus"
	case "generate":
		return len(args) > 0 && !slices.ContainsFunc(args, func(a string) bool { return !insidePackage(a) })
	case "run":
		args = trimpathOptional(args)
		return len(args) >= 2 && path.Clean(args[0]) == "cmd/magus-utils" && slices.Contains(magusUtilsGenerators, args[1])
	}
	return false
}

func trimpathOptional(args []string) []string {
	if len(args) > 0 && args[0] == "-trimpath" {
		return args[1:]
	}
	return args
}

// insidePackage reports a package operand naming a directory under the build root: no
// flag, no absolute path, nothing that climbs out.
func insidePackage(a string) bool {
	if a == "" || strings.HasPrefix(a, "-") || path.IsAbs(a) {
		return false
	}
	c := path.Clean(a)
	return c != ".." && !strings.HasPrefix(c, "../")
}

// soleGoCommand reports a line that is one go command and nothing else: no pipe, chain,
// redirect, wrapper or subshell, and no environment prefix but GOEXPERIMENT, which this
// module's builds set.
func soleGoCommand(command string, d Dialect) bool {
	f, err := parseFile(command, d)
	if err != nil || len(f.Stmts) != 1 {
		return false
	}
	st := f.Stmts[0]
	call, ok := st.Cmd.(*syntax.CallExpr)
	if !ok || st.Negated || st.Background || st.Coprocess || len(st.Redirs) > 0 || len(call.Args) == 0 {
		return false
	}
	for _, a := range call.Assigns {
		if a.Name == nil || a.Name.Value != "GOEXPERIMENT" || a.Append || a.Naked || a.Array != nil || a.Index != nil {
			return false
		}
	}
	return literalWord(call.Args[0].Parts) == "go"
}

// cannotLoad reports whether the workspace at root fails to load with MGS1021 for the
// binary judging the call. The hook runs the checkout's own ./magus when there is one, so
// a binary that predates its sources answers for itself; with none, the binary on PATH
// answers, and a tree it can load needs no recovery.
func cannotLoad(ctx context.Context, deps Dependencies, root string) bool {
	_, err := deps.inspect(ctx, root)
	return errors.Is(err, types.WorkspaceNeedsNewerMagus)
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
	// recovery is a recoversMagus call alone on its line in a root whose workspace fails
	// to load with MGS1021, advised through as recoveryAdvisory.
	recovery         bool
	recoveryAdvisory ShellVerdict
}

// apply layers this outcome onto v, the raw-tool deny it refines.
func (o *ownBuildOutcome) apply(v ShellVerdict) ShellVerdict {
	switch {
	case o.recovery:
		return o.recoveryAdvisory
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
func ownBuildOutcomeFor(ctx context.Context, deps Dependencies, command string, d Dialect, denied hint.Invocation, call goCall, root string, multipleCmds bool, cwd string) *ownBuildOutcome {
	where := "this checkout"
	argv := bootstrapArgv
	if root != filepath.Clean(cwd) {
		where = root
		argv = append([]string{"go", "-C", root}, bootstrapArgv[1:]...)
	}
	rule := denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(denied)}
	workspaceShell := matchWorkspaceShell(deps.ShellRules, command, d)
	// The load is a full workspace open, so only a line that could be let through pays it.
	recovery := call.recoversMagus() && soleGoCommand(command, d) && cannotLoad(ctx, deps, root)
	return &ownBuildOutcome{
		root:          root,
		bootstrapArgv: argv,
		bootstrap:     call.bootstrapsMagus(),
		link:          call.linksMagus(),
		hasBinary:     hasMagusBinary(root),
		multipleCmds:  multipleCmds,
		advisory: strengthenWithWorkspace(ShellVerdict{
			Context: "magus workspace: bootstrap allowed, since " + where + " has no magus binary yet: " + bootstrapWhy + " Use ./magus from then on.",
			Rule:    rule,
		}, workspaceShell),
		recovery: recovery,
		recoveryAdvisory: strengthenWithWorkspace(ShellVerdict{
			Context: "magus workspace: recovery allowed, since " + where + " cannot load its own sources (MGS1021) and no magus target can run until it does. " +
				"Relink and regenerate only; once it loads, rebuild with " + ownRebuild + ".",
			Rule: rule,
		}, workspaceShell),
	}
}

// ownBuildVerdict is the BOOTSTRAP correction for a go command aimed at magus's own
// module: a fresh checkout has no ./magus, and every route to one runs through magus or
// a raw toolchain command. bootstrapArgv, alone on its line, in a root with no binary
// yet, is advised through instead of denied, and every other raw go command there is
// served it. Nil when the line holds no go command denied in a checkout of magus.
//
// The RECOVERY correction covers a checkout that cannot load its own sources (MGS1021):
// no target runs there, so the relink and the generators (recoversMagus), each alone on
// its line, are advised through while the load fails, with or without a binary.
//
// A -C outside the workspace passes the pure rule, since a foreign tree is not its to
// funnel; a -C into another checkout of magus is this repository's policy to judge
// (hack/policy/guard.buzz), bootstrap included.
//
// It reads the filesystem, so it lives beside Judge rather than inside Evaluate.
func ownBuildVerdict(ctx context.Context, deps Dependencies, cwd, command string, d Dialect) *ownBuildOutcome {
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
		return ownBuildOutcomeFor(ctx, deps, command, d, cmds[i], call, root, len(cmds) > 1, cwd)
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
