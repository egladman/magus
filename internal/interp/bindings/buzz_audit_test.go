package bindings

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// auditMode is how a .buzz file is loaded, and so how the audit checks it.
type auditMode int

const (
	// auditScript is a file `magus buzz` runs, or a module one imports: strict, with
	// the script host.
	auditScript auditMode = iota
	// auditModule is a module nothing in the workspace imports, run and tested with
	// `magus buzz --embedded`: embedded, with the script host.
	auditModule
	// auditMagusfile is a magusfile, or a module a magusfile imports: embedded, with
	// the magusfile host and its spell imports.
	auditMagusfile
	// auditSpell is a spell, or a module a spell imports: embedded, with the host a
	// spell is compiled under.
	auditSpell
	// auditDecls is a module's declaration source, registered with SetModuleDecls:
	// embedded, with the spell host so a bundle that imports magus/spell resolves.
	auditDecls
)

func (m auditMode) String() string {
	switch m {
	case auditScript:
		return "script"
	case auditModule:
		return "module"
	case auditMagusfile:
		return "magusfile"
	case auditSpell:
		return "spell"
	case auditDecls:
		return "decls"
	}
	return "unknown"
}

// auditFile is one check: a file, or a declaration bundle, and the directories its
// imports resolve against.
type auditFile struct {
	path string
	src  string
	mode auditMode
	dirs []string
}

// auditSkipDirs are trees whose .buzz files no magus host loads: testdata holds
// fixtures that are wrong on purpose, and benchmarks/upstream is upstream Buzz's own
// benchmark set, run by upstream Buzz.
var auditSkipDirs = []string{".git", "node_modules", "testdata", filepath.Join("libs", "gopherbuzz", "benchmarks", "upstream")}

// auditBundles are the declaration bundles assembled from generated mirrors. A
// mirror alone references types its bundle declares before it, so each is checked as
// the bundle the session registers, never file by file.
var auditBundles = map[string]string{
	spell.SpellModulePath: spell.SpellModuleSource,
	spell.LintModulePath:  spell.LintModuleSource,
}

// auditBundled are the directories whose files reach a session only through a bundle
// or through the host that embeds them.
var auditBundled = []string{
	filepath.Join("internal", "spell", "gen", "types"),
	// The merge-queue provider and its record mirrors load in internal/queue/provider's
	// own session, whose host module this package cannot build; that package's tests
	// load github.buzz through it.
	filepath.Join("internal", "queue", "provider"),
}

type bindingsSuite struct {
	suite.Suite
}

func TestBindings(t *testing.T) {
	suite.Run(t, new(bindingsSuite))
}

// TestBuzzAudit type-checks every .buzz file in the workspace with the in-tree checker
// and the full host modules, each in the mode it is loaded in, and fails on any
// diagnostic, warnings included. A file loaded more than one way is checked each way.
func (s *bindingsSuite) TestBuzzAudit() {
	t := s.T()
	if testing.Short() {
		t.Skip("type-checks every .buzz file in the workspace")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	s.Require().NoError(err)
	checks := auditFiles(t, root)
	s.Require().NotEmpty(checks, "no .buzz files under the workspace root %s", root)
	// A module's diagnostic is reported by its own check and by every importer's, so
	// each is kept once, under the file it names.
	byFile := map[string][]string{}
	seen := map[string]bool{}
	for _, c := range checks {
		for _, d := range auditCheck(t, root, c) {
			line := fmt.Sprintf("%s: %s", c.mode, d)
			if !seen[line] {
				seen[line] = true
				byFile[d.File] = append(byFile[d.File], line)
			}
		}
	}
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		s.Empty(byFile[file], "%s: Buzz diagnostics", file)
	}
	t.Logf("%d checks, %d files with diagnostics", len(checks), len(byFile))
}

// auditFiles walks root for .buzz files and gives each the mode it is loaded in. A
// magusfile, a spell and a script each load embedded or strict, and so does every
// file their imports reach; a module nothing reaches is run with --embedded.
func auditFiles(t *testing.T, root string) []auditFile {
	t.Helper()
	var all []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			if slices.Contains(auditSkipDirs, d.Name()) || slices.Contains(auditSkipDirs, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".buzz") {
			return nil
		}
		if slices.ContainsFunc(auditBundled, func(dir string) bool { return strings.HasPrefix(rel, dir+string(filepath.Separator)) }) {
			return nil
		}
		all = append(all, path)
		return nil
	})
	require.NoError(t, err)

	var out []auditFile
	reached := map[string]bool{}
	type walked struct {
		path string
		mode auditMode
	}
	done := map[walked]bool{}
	var walk func(path string, mode auditMode, dirs []string)
	walk = func(path string, mode auditMode, dirs []string) {
		reached[path] = true
		if done[walked{path, mode}] {
			return
		}
		done[walked{path, mode}] = true
		src := auditRead(t, path)
		out = append(out, auditFile{path: path, src: src, mode: mode, dirs: dirs})
		for _, imp := range auditImports(src) {
			if dep := auditResolve(imp, append([]string{filepath.Dir(path)}, dirs...), mode); dep != "" {
				walk(dep, mode, dirs)
			}
		}
	}
	for _, path := range all {
		src := auditRead(t, path)
		switch {
		case strings.Contains(src, spell.NameFunc):
			walk(path, auditSpell, []string{filepath.Dir(path)})
		case auditIsMagusfile(root, path, src):
			walk(path, auditMagusfile, auditProjectDirs(root, path))
		case strings.Contains(src, "fun main("):
			walk(path, auditScript, []string{filepath.Dir(path), root})
		}
	}
	for _, path := range all {
		if !reached[path] {
			out = append(out, auditFile{path: path, src: auditRead(t, path), mode: auditModule, dirs: []string{filepath.Dir(path), root}})
		}
	}
	for module, src := range auditBundles {
		out = append(out, auditFile{path: fmt.Sprintf("<%s>", module), src: src, mode: auditDecls})
	}
	return out
}

func auditRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// auditIsMagusfile reports whether path is read as a magusfile: by its name, by its
// magusfiles/ directory, as a std example (a magusfile target body), or because it
// imports a spell, which only a workspace load binds.
func auditIsMagusfile(root, path, src string) bool {
	base := filepath.Base(path)
	rel, _ := filepath.Rel(root, path)
	return base == "magusfile.buzz" || strings.HasPrefix(base, "magusfile.") ||
		filepath.Base(filepath.Dir(path)) == "magusfiles" ||
		strings.HasPrefix(rel, filepath.Join("std", "examples")+string(filepath.Separator)) ||
		strings.Contains(src, `import "magus/spell/`)
}

// auditProjectDirs is the import search the workspace loader gives the magusfile at
// path: its project, then the workspace root.
func auditProjectDirs(root, path string) []string {
	dir := filepath.Dir(path)
	if filepath.Base(dir) == "magusfiles" {
		dir = filepath.Dir(dir)
	}
	if dir == root {
		return []string{dir}
	}
	return []string{dir, root}
}

var auditImportLine = regexp.MustCompile(`(?m)^\s*import\s+"([^"]+)"`)

func auditImports(src string) []string {
	var out []string
	for _, m := range auditImportLine.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

// auditTemplates are the module layouts every host searches (magusSearchPaths,
// spellSearchPaths, the script include dirs).
var auditTemplates = []string{"?.buzz", filepath.Join("?", "main.buzz"), filepath.Join("?", "src", "main.buzz"), filepath.Join("?", "src", "?.buzz")}

// auditResolve finds the file an import names, or "" for a module no file backs.
// A magusfile also searches magusfiles/ and the spells/<name>/spell.buzz layout.
func auditResolve(imp string, dirs []string, mode auditMode) string {
	templates := auditTemplates
	if mode == auditMagusfile {
		templates = append(slices.Clone(templates), filepath.Join("magusfiles", "?.buzz"), filepath.Join("?", "spell.buzz"))
	}
	for _, dir := range dirs {
		for _, tmpl := range templates {
			candidate := filepath.Join(dir, strings.ReplaceAll(tmpl, "?", imp))
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

// auditWorkspace answers Root for the import resolvers, which bound their search by
// it. A check calls no other method, and one that did would panic.
type auditWorkspace struct {
	types.WorkspaceRepository
	root string
}

func (w auditWorkspace) Root() string { return w.root }

// auditCheck builds the session c's mode loads it in and returns its diagnostics,
// each with File set relative to root.
func auditCheck(t *testing.T, root string, c auditFile) []buzz.Diagnostic {
	t.Helper()
	ctx := types.WithWorkspace(context.Background(), auditWorkspace{root: root})
	var sess *buzz.Session
	switch c.mode {
	case auditMagusfile:
		ctx = interp.WithSource(ctx, &interp.Source{Dir: c.dirs[0], Files: []string{c.path}, Engine: "buzz"})
		sess = buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithSearchPaths(auditSearchPaths(c.dirs, true)...))
		sess.SetIncludeDirs(nil)
		registerAllBuzz(ctx, sess, map[string]vm.Callable{}, map[string]vm.Value{}, true)
	case auditSpell, auditDecls:
		sess = buzz.NewSession(ctx, buzz.WithEmbedded(), buzz.WithSearchPaths(spellSearchPaths(c.dirs...)...))
		RegisterMagusNamespace(ctx, sess)
		registerMagusModules(ctx, sess)
	case auditScript, auditModule:
		var opts []buzz.Option
		if c.mode == auditModule {
			opts = append(opts, buzz.WithEmbedded())
		}
		sess = buzz.NewSession(ctx, opts...)
		// A module a script imports resolves its own imports beside itself first.
		dirs := append([]string{filepath.Dir(c.path)}, c.dirs...)
		sess.SetIncludeDirs(append(dirs, sess.IncludeDirs()...))
		RegisterModules(ctx, sess, WithScriptOutput(io.Discard))
		RegisterSpellDecls(sess)
		RegisterMagusNamespace(ctx, sess)
		DeclareMagusTypes(sess)
	}
	defer func() { _ = sess.Close() }()

	diags := sess.Diagnostics(c.src)
	for i := range diags {
		file := diags[i].File
		if file == "" {
			file = c.path
		}
		if rel, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(rel, "..") {
			file = rel
		}
		diags[i].File = file
	}
	return diags
}

// auditSearchPaths is magusSearchPaths over dirs, without the remote spell view an
// audit has no lock for.
func auditSearchPaths(dirs []string, magusfiles bool) []string {
	templates := auditTemplates
	if magusfiles {
		templates = append(slices.Clone(templates), filepath.Join("magusfiles", "?.buzz"))
	}
	var out []string
	for _, d := range dirs {
		for _, tmpl := range templates {
			out = append(out, filepath.Join(d, tmpl))
		}
	}
	return out
}
