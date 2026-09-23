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

// bootstrapsMagus reports the one build the raw-tool rule exempts: `go build -o magus
// ./cmd/magus` (or `cmd/magus`, with or without a trailing slash) and nothing else, the
// output landing as `magus` in the root it builds. Any other flag, package or output
// path is an ordinary build and stays denied.
func (g goCall) bootstrapsMagus(root string) bool {
	if len(g.args) == 0 || g.args[0] != "build" {
		return false
	}
	var out string
	var pkgs []string
	for i := 1; i < len(g.args); i++ {
		switch a := g.args[i]; {
		case a == "-o" && i+1 < len(g.args):
			out = g.args[i+1]
			i++
		case strings.HasPrefix(a, "-o="):
			out = strings.TrimPrefix(a, "-o=")
		case strings.HasPrefix(a, "-"):
			return false
		default:
			pkgs = append(pkgs, a)
		}
	}
	if len(pkgs) != 1 || path.Clean(pkgs[0]) != "cmd/magus" || out == "" {
		return false
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}
	return filepath.Clean(out) == filepath.Join(root, "magus")
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

const bootstrapRebuild = "`./magus run go-build .`, which regenerates the embedded spell bytecode a bare link bakes in stale"

// ownBuildOutcome is the tail of a correction rankOwnBuild applies once bootstrapsMagus
// already holds for the matched call: whether root already has a binary, whether the
// bootstrap build shared its line with something else, and the verdict to use for
// neither.
type ownBuildOutcome struct {
	root         string
	hasBinary    bool
	multipleCmds bool
	advisory     ShellVerdict
}

// apply layers this outcome onto v, the deny the raw-tool case refines in place or the
// escapes-workspace case starts fresh.
func (o *ownBuildOutcome) apply(v ShellVerdict) ShellVerdict {
	switch {
	case o.hasBinary:
		v.Deny += "\nNot a bootstrap: " + o.root + " already has a magus binary. Rebuild with " + bootstrapRebuild + "."
	case o.multipleCmds:
		v.Deny += "\nThe bootstrap build is exempt only alone on its line."
	default:
		return o.advisory
	}
	return v
}

// ownBuildCorrection is what rankOwnBuild needs to re-judge a go command aimed at a
// checkout of magus itself. At most one side is used, chosen by the verdict already in
// hand: rawTool refines a deny Evaluate's pure pass already produced for the raw-tool
// rule; escaped pairs a fresh deny (that pass let a -C outside the workspace through,
// since a foreign tree is not its to funnel) with its own outcome.
type ownBuildCorrection struct {
	rawTool     *ownBuildOutcome
	escapedDeny ShellVerdict
	escaped     *ownBuildOutcome
}

// ownBuildOutcomeFor builds the outcome rankOwnBuild layers onto the deny for denied,
// once bootstrapsMagus already holds for it.
func ownBuildOutcomeFor(deps Dependencies, command string, d Dialect, denied hint.Invocation, root string, multipleCmds bool, cwd string) *ownBuildOutcome {
	where := "this checkout"
	if root != filepath.Clean(cwd) {
		where = root
	}
	return &ownBuildOutcome{
		root:         root,
		hasBinary:    hasMagusBinary(root),
		multipleCmds: multipleCmds,
		advisory: strengthenWithWorkspace(ShellVerdict{
			Context: "magus workspace: bootstrap build allowed, since " + where + " has no magus binary yet. Next run " + bootstrapRebuild + ".",
			Rule:    denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(denied)},
		}, matchWorkspaceShell(deps.ShellRules, command, d)),
	}
}

// ownBuildVerdict computes the two corrections rankOwnBuild may apply for a go command
// aimed at magus's own module, the one tree whose targets the guard can name:
//
//   - The BOOTSTRAP: a fresh checkout has no ./magus, and every route to one runs through
//     magus or a raw build. `go build -o magus ./cmd/magus`, alone on its line, into a
//     root with no binary yet, is advised through instead of denied.
//   - Another checkout by -C: the pure rule passes a -C outside the workspace, because a
//     foreign tree is not its to funnel. A sibling checkout of magus is, so the deny holds
//     there too.
//
// It reads the filesystem, so it lives beside Judge rather than inside Evaluate.
func ownBuildVerdict(deps Dependencies, cwd, command string, d Dialect) ownBuildCorrection {
	var oc ownBuildCorrection
	if cwd == "" {
		return oc
	}
	cmds, parsed := ParseCommandsDialect(command, d)
	if !parsed {
		return oc
	}

	if i := slices.IndexFunc(cmds, func(c hint.Invocation) bool { return rawToolDenied(deps, c) }); i >= 0 {
		if call, ok := readGoCall(cmds[i]); ok {
			if root := call.buildRoot(cwd); ownSourceRoot(root) && call.bootstrapsMagus(root) {
				oc.rawTool = ownBuildOutcomeFor(deps, command, d, cmds[i], root, len(cmds) > 1, cwd)
			}
		}
	}

	for _, c := range cmds {
		call, ok := readGoCall(c)
		if !ok || call.chdir == "" || !escapesWorkspace(call.chdir) {
			continue
		}
		root := call.buildRoot(cwd)
		if !ownSourceRoot(root) {
			continue
		}
		match, covered := rawToolMatch(deps, hint.Invocation{Name: "go", Args: call.args})
		if !covered {
			continue
		}
		oc.escapedDeny = ShellVerdict{
			Deny: explainDeny(command, c, runGuardAdvice(match)+"\n"+root+" is a checkout of magus itself, so run the target from that checkout."),
			Rule: denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(c)},
		}
		if call.bootstrapsMagus(root) {
			oc.escaped = ownBuildOutcomeFor(deps, command, d, c, root, len(cmds) > 1, cwd)
		}
		break
	}

	return oc
}

// rankOwnBuild ranks the own-build correction against the verdict the other rules
// reached: rawTool only refines a deny already keyed to the raw-tool rule, escaped only
// replaces a silence, and every other verdict passes through untouched.
func rankOwnBuild(v ShellVerdict, oc ownBuildCorrection) ShellVerdict {
	switch {
	case v.Rule.Name == denyRuleRawTool && v.Deny != "":
		if oc.rawTool == nil {
			return v
		}
		return oc.rawTool.apply(v)
	case v.Deny == "":
		if oc.escapedDeny.Deny == "" {
			return v
		}
		if oc.escaped == nil {
			return oc.escapedDeny
		}
		return oc.escaped.apply(oc.escapedDeny)
	default:
		return v
	}
}
