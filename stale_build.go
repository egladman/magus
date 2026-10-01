package magus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/egladman/magus/types"
)

// withOwnBuildEscape adds the way out to an MGS1021 raised by a binary built from the
// checkout it cannot load. That binary cannot rebuild itself, since every command it runs
// loads the same tree, so the generic advice to rebuild names a step it cannot take.
// Any other error, and MGS1021 from any other binary, is returned as it came.
func withOwnBuildEscape(err error, root string) error {
	var d *types.DiagnosticError
	if !errors.As(err, &d) || d.Code != types.WorkspaceNeedsNewerMagus {
		return err
	}
	exe, exeErr := os.Executable()
	info, ok := debug.ReadBuildInfo()
	if exeErr != nil || !ok {
		return err
	}
	escape := ownBuildEscape(exe, info, root)
	if escape == "" {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, escape)
}

// ownBuildEscape is the escape for a binary at exe, built as info, that sits at the root of
// the checkout root and was built from that checkout's own module; "" for any other binary,
// such as a release installed on PATH or vendored at the root.
func ownBuildEscape(exe string, info *debug.BuildInfo, root string) string {
	if info == nil || info.Main.Path == "" || !strings.HasPrefix(info.Path, info.Main.Path+"/") {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if filepath.Dir(exe) != filepath.Clean(root) {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || modfile.ModulePath(data) != info.Main.Path {
		return ""
	}
	name := filepath.Base(exe)
	pkg := strings.TrimPrefix(info.Path, info.Main.Path+"/")
	return "This binary was built from this checkout's own sources and predates them, so it cannot " +
		"rebuild itself: every command it runs loads the same tree. Link a new one from source, one " +
		"command at a time: `mv " + name + " " + name + ".old`, then `go build -o " + name + " ./" + pkg +
		"`, then rebuild it the way this workspace builds it. If that link fails with `undefined:` in " +
		"generated code, the checkout's committed generated files are behind its sources (a merge kept " +
		"one side), and restoring either side's copy brings back the mismatch: " + regenerate(info.Main.Path) +
		", then link again."
}

// ownModule is the module this package was compiled from, read off a type so a renamed fork
// still recognizes its own tree.
var ownModule = reflect.TypeFor[Magus]().PkgPath()

// ownGenerators are the commands this module's *_generate targets in magusfile.buzz run, in
// the order their ctx.needs settle: types, spells, job, langservice, bindings, mcp_tools.
var ownGenerators = []string{
	"go generate ./cmd/magus-utils",
	"go generate ./internal/spell/...",
	"go run ./cmd/magus-utils jobschema -out internal/job/gen",
	"go generate ./internal/langservice",
	"go generate ./std/...",
	"go generate ./internal/handler/mcp",
}

// regenerate names how a checkout of module rebuilds its generated files without a binary.
// Only this module's generators are known; any other checkout is pointed at its own.
func regenerate(module string) string {
	if module != ownModule {
		return "regenerate them with the `go generate` commands its generate targets run"
	}
	return "regenerate them, one command at a time, with what the *_generate targets run: `" +
		strings.Join(ownGenerators, "`, then `") + "`"
}
