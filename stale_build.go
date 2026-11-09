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

	"github.com/egladman/magus/internal/config"
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
	return fmt.Errorf("%w: %s", err, escape)
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
	pkg := strings.TrimPrefix(info.Path, info.Main.Path+"/")
	link := config.SourceCommand(root, pkg, filepath.Base(exe), false)
	escape := fmt.Sprintf("This binary was built from this checkout's own sources and predates them, so it cannot rebuild itself: every command it runs loads the same tree. Link a new one from source, one command at a time: `%s`, then rebuild it the way this workspace builds it. If that link fails with `undefined:` in generated code, the checkout's committed generated files are behind its sources (a merge kept one side), and restoring either side's copy brings back the mismatch: regenerate them with the commands its generate targets run, then link again.", link)
	if ready := config.ReadyCheckoutCommand(config.CheckoutHookAt(root)); ready != "" {
		escape += fmt.Sprintf(" Then ready the checkout, alone on its line: `%s`.", ready)
	}
	scripts := config.StaleBinaryScripts(root, workspaceConfigBytes(root))
	if len(scripts) == 0 {
		return escape + " No script in this workspace declares SERVES \"code:MGS1021\", so magus names none of its own."
	}
	return escape + fmt.Sprintf(" This workspace's script for it: `%s`.", strings.Join(scripts, "`, or `"))
}

// workspaceConfigBytes is root's magus.yaml as bytes, nil when it has none; the stale
// binary reading it may not decode it.
func workspaceConfigBytes(root string) []byte {
	for _, name := range []string{config.Filename, config.DottedFilename} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err == nil {
			return data
		}
	}
	return nil
}

// ownModule is the module this package was compiled from, read off a type so a renamed fork
// still recognizes its own tree. Only the binary-versus-own-source skew check reads it.
var ownModule = reflect.TypeFor[Magus]().PkgPath()
