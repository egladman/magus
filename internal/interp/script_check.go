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
			if handle, ok := unresolvedSpellHandle(d); ok {
				if err := CheckSpellImports([]string{handle}); err != nil {
					slog.InfoContext(ctx, "unresolved spell import", attr.Hint(), attr.Error(err))
				}
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

// nearestMagusfileDir returns the closest directory at or above dir that holds a
// magusfile.buzz, or "" when none does.
func nearestMagusfileDir(dir string) string {
	for dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "magusfile.buzz")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
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
	// A file importing a spell module is only ever loaded by the workspace loader,
	// which reads it embedded, so --check judges it by those rules and not upstream's.
	if embedded || ImportsSpell(string(data)) {
		opts = append(opts, buzz.WithEmbedded())
	}
	sess := buzz.NewSession(ctx, opts...)
	defer func() { _ = sess.Close() }() // the diagnostics are already decided; a close error has no reader
	if dir := scriptImportDir(resolved); dir != "" {
		dirs := []string{dir}
		// A project-relative import names a path from the workspace root, which
		// neither the script's directory nor the cwd need be.
		if root := nearestMagusfileDir(dir); root != "" && root != dir {
			dirs = append(dirs, root)
		}
		sess.SetIncludeDirs(append(dirs, sess.IncludeDirs()...))
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
	// The bindings a magusfile load gives `magus/spell/*`, so a magusfile or a
	// target definition checks as a standalone file. No workspace is open, so no
	// magus.yaml override applies: every import binds the spell this build ships.
	r.Host.RegisterSpellImports(sess)

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

// unresolvedSpellHandle returns the <handle> of an unresolved
// `import "magus/spell/<handle>"` d reports. Every spell this build ships is bound
// during a check, so such a diagnostic is a genuine miss, and the caller names the
// spells that do resolve.
//
// The code is matched in EITHER position, because an unresolved import arrives by two
// routes that disagree about where it lands. A checker error carries it in Code; an
// import that fails during resolution surfaces as a parse error, and that path
// (Session.Diagnostics) renders the whole thing into Msg and leaves Code empty.
func unresolvedSpellHandle(d buzz.Diagnostic) (string, bool) {
	if string(d.Code) != unresolvedImportCode && !strings.Contains(d.Msg, unresolvedImportCode) {
		return "", false
	}
	_, rest, ok := strings.Cut(d.Msg, `"`+spellModulePrefix)
	if !ok {
		return "", false
	}
	handle, _, ok := strings.Cut(rest, `"`)
	return handle, ok && handle != ""
}

const (
	// unresolvedImportCode is gopherbuzz's unresolved-import code, spelled here rather
	// than imported: the code is the stable published identifier.
	unresolvedImportCode = "BZZ2001"
	spellModulePrefix    = "magus/spell/"
)
