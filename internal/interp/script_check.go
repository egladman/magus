package interp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// Check parses and type-checks each named file WITHOUT running it, printing
// every diagnostic and failing on any of them, warnings included.
//
// Running a file is not a check of it. That is the gap this fills: a hook script
// whose whole job is a side effect (read the event on stdin, judge it, exec magus)
// cannot be validated by execution, so until this existed the only way to learn
// whether the shipped glue still compiled was a Go test that ran it. Buzz is also
// the one language where magus cannot defer to a spell-named tool, because magus is
// the toolchain; every other language pack names a checker that already exists.
//
// It takes several paths because the question is almost always asked about a set:
// the files just edited, or every glue script at once.
func (r *ScriptRunner) Check(ctx context.Context, files []string, embedded bool) error {
	// Sessions are NOT shared across files. Session.Diagnostics mutates session state
	// (loadedPaths, env, importedTypes) and is documented as needing a fresh one, so a
	// reused session would report the second file against the first file's scope and
	// skip its imports as already-loaded.
	failed := 0
	noted := false
	for _, path := range files {
		diags, err := r.CheckFile(ctx, path, embedded)
		if err != nil {
			return err
		}
		for _, d := range diags {
			level := slog.LevelWarn
			if d.Severity != buzz.SeverityWarning {
				level = slog.LevelError
			}
			failed++
			slog.LogAttrs(ctx, level, d.String(), attr.Notice(""))
			if !noted && spellImportUnresolved(d) {
				slog.InfoContext(ctx, spellImportNote, attr.Notice("note"))
				noted = true
			}
		}
	}
	if failed > 0 {
		// Reported: every diagnostic is already on stderr with its own position, so a
		// trailing "N errors" wrapper would be the only line without one.
		return exit.Reported(exit.Failure)
	}
	return nil
}

// CheckFile checks one path, returning its diagnostics with File set so each
// one renders as <file>:L:C, the position shape an editor can jump to. A diagnostic
// inside an imported file names that file, relative to the working directory when
// it sits beneath it.
func (r *ScriptRunner) CheckFile(ctx context.Context, path string, embedded bool) ([]buzz.Diagnostic, error) {
	resolved := resolveScriptFile(path)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	var opts []buzz.Option
	if embedded {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }()
	if dir := scriptImportDir(resolved); dir != "" {
		sess.SetIncludeDirs(append([]string{dir}, sess.IncludeDirs()...))
	}
	// Script output goes to STDERR on this path, unlike a run. A check prints no
	// program output of its own, so anything reaching stdout here came from an
	// imported module's top level, which Diagnostics executes; that is incidental to
	// the answer and must not be mixed into stdout with it.
	addProfileObserver(ctx, sess)
	lateErr, err := r.installHost(ctx, sess, string(data), os.Stderr)
	if err != nil {
		return nil, err
	}

	diags := sess.Diagnostics(string(data))
	if err := lateErr(); err != nil {
		return nil, err
	}
	wd, _ := os.Getwd()
	for i := range diags {
		switch f := diags[i].File; {
		case f == "":
			diags[i].File = resolved
		case !filepath.IsAbs(f):
			diags[i].File = filepath.Clean(f)
		default:
			if rel, err := filepath.Rel(wd, f); err == nil && !strings.HasPrefix(rel, "..") {
				diags[i].File = rel
			}
		}
	}
	return diags, nil
}

// spellImportUnresolved reports whether d is the one diagnostic --check raises
// that does not mean what it says: a magusfile's `import "magus/spell/<name>"`.
//
// Those paths are bound by the workspace loader from the resolved spell registry
// (internal/interp/runtime.go), so a bare session has nothing to resolve them
// against and reports BZZ2001. The import is fine; this mode just cannot see it.
//
// The code is matched in EITHER position, because an unresolved import arrives by two
// routes that disagree about where it lands. A checker error carries it in Code; an
// import that fails during resolution surfaces as a parse error, and that path
// (Session.Diagnostics) renders the whole thing into Msg and leaves Code empty. Reading
// only Code silently missed every real instance, since resolution is the route this
// one actually takes.
func spellImportUnresolved(d buzz.Diagnostic) bool {
	if !strings.Contains(d.Msg, spellModulePrefix) {
		return false
	}
	return string(d.Code) == unresolvedImportCode || strings.Contains(d.Msg, unresolvedImportCode)
}

// spellImportNote follows an unresolved spell import.
//
// It is printed rather than suppressed, and the diagnostic still fails, because the
// same code covers a genuine typo (`magus/spell/gooo`) and this mode cannot tell
// the two apart. What it CAN do is name the check that does settle it.
//
// That check is reassuring, and it bounds what this verb is for. A file importing a
// spell module is loaded by magus itself, and loading reports exactly these errors, so
// a magusfile and a target definition are the Buzz files that already had a check. The
// ones that did not are the ones nothing loads: a standalone script, and hook glue
// whose whole job is a side effect.
const spellImportNote = "magus/spell/* is bound by the workspace loader, so --check cannot resolve it. " +
	"A file that imports one (a magusfile, a target definition) is checked by loading it: " +
	"any magus command reports its errors."

const (
	// unresolvedImportCode is gopherbuzz's unresolved-import code, spelled here rather
	// than imported: the code is the stable published identifier.
	unresolvedImportCode = "BZZ2001"
	spellModulePrefix    = "magus/spell/"
)
