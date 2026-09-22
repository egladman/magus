package guard

import (
	"errors"
	"io/fs"
	"os"
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
		call.chdir, args = dir, args[chdirWidth(args):]
	}
	if len(args) == 0 || !subcommandWord(args[0]) {
		return goCall{}, false
	}
	rest := args[1:]
	if call.chdir == "" {
		if dir, ok := valuedOperand(valued, rest); ok {
			call.chdir, rest = dir, rest[chdirWidth(rest):]
		}
	}
	call.args = append([]string{args[0]}, rest...)
	return call, true
}

// chdirWidth is how many words the -C flag leading args spans.
func chdirWidth(args []string) int {
	if strings.Contains(args[0], "=") {
		return 1
	}
	return 2
}

// root is the directory the call builds in: its -C resolved against cwd, or cwd.
func (g goCall) root(cwd string) string {
	switch {
	case g.chdir == "":
		return filepath.Clean(cwd)
	case filepath.IsAbs(g.chdir):
		return filepath.Clean(g.chdir)
	}
	return filepath.Join(cwd, g.chdir)
}

// bootstrapsMagus reports the one build the raw-tool rule exempts: `go build -o magus
// ./cmd/magus` and nothing else, the output landing as `magus` in the root it builds.
// Any other flag, package or output path is an ordinary build and stays denied.
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
	if len(pkgs) != 1 || (pkgs[0] != "./cmd/magus" && pkgs[0] != "./cmd/magus/") || out == "" {
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

// rankOwnBuild re-judges a go command aimed at a checkout of magus itself. It reads the
// filesystem, so it lives beside Judge rather than inside Evaluate.
//
// Two corrections, both only for magus's own module, the one tree whose targets the
// guard can name:
//
//   - The BOOTSTRAP: a fresh checkout has no ./magus, and every route to one runs through
//     magus or a raw build. `go build -o magus ./cmd/magus`, alone on its line, into a
//     root with no binary yet, is advised through instead of denied.
//   - Another checkout by -C: the pure rule passes a -C outside the workspace, because a
//     foreign tree is not its to funnel. A sibling checkout of magus is, so the deny holds
//     there too.
func rankOwnBuild(v ShellVerdict, deps Dependencies, cwd, command string, d Dialect) ShellVerdict {
	if cwd == "" {
		return v
	}
	cmds, parsed := ParseCommandsDialect(command, d)
	if !parsed {
		return v
	}
	var denied hint.Invocation
	var call goCall
	var root string
	switch {
	case v.Rule.Name == denyRuleRawTool && v.Deny != "":
		i := slices.IndexFunc(cmds, func(c hint.Invocation) bool { return rawToolDenied(deps, c) })
		if i < 0 {
			return v
		}
		var ok bool
		denied = cmds[i]
		if call, ok = readGoCall(denied); !ok {
			return v
		}
		if root = call.root(cwd); !ownSourceRoot(root) {
			return v
		}
	case v.Deny == "":
		found := false
		for _, c := range cmds {
			got, ok := readGoCall(c)
			if !ok || got.chdir == "" || !escapesWorkspace(got.chdir) || !ownSourceRoot(got.root(cwd)) {
				continue
			}
			match, covered := rawToolMatch(deps, hint.Invocation{Name: "go", Args: got.args})
			if !covered {
				continue
			}
			denied, call, root, found = c, got, got.root(cwd), true
			v = ShellVerdict{
				Deny: explainDeny(command, c, runGuardAdvice(match)+"\n"+root+" is a checkout of magus itself, so run the target from that checkout."),
				Rule: denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(c)},
			}
			break
		}
		if !found {
			return v
		}
	default:
		return v
	}
	if !call.bootstrapsMagus(root) {
		return v
	}
	switch {
	case hasMagusBinary(root):
		v.Deny += "\nNot a bootstrap: " + root + " already has a magus binary. Rebuild with " + bootstrapRebuild + "."
	case len(cmds) > 1:
		v.Deny += "\nThe bootstrap build is exempt only alone on its line."
	default:
		where := "this checkout"
		if root != filepath.Clean(cwd) {
			where = root
		}
		return strengthenWithWorkspace(ShellVerdict{
			Context: "magus workspace: bootstrap build allowed, since " + where + " has no magus binary yet. Next run " + bootstrapRebuild + ".",
			Rule:    denyRule{Name: denyRuleRawTool, Arg: resolvedCommand(denied)},
		}, matchWorkspaceShell(deps.ShellRules, command, d))
	}
	return v
}
