package eval

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// Tree is a checkout of one commit under a scratch directory. A span's checked file is
// the one in the tree at the commit its set was cut from, so imports resolve the way
// they did when the set was made.
type Tree struct {
	Dir string
	Rev string
}

// MakeTree extracts the tracked files of rev under dir/tree-<rev> with git archive and
// puts overlayRev's magus.yaml in it: the binary that checks is built at the overlay's
// commit and refuses a workspace file with a key it does not know. A tree already
// extracted is reused.
func MakeTree(root, rev, overlayRev, dir string) (Tree, error) {
	if len(rev) < 12 {
		return Tree{}, fmt.Errorf("tree needs a full commit, got %q", rev)
	}
	t := Tree{Dir: filepath.Join(dir, "tree-"+rev[:12]), Rev: rev}
	done := filepath.Join(t.Dir, ".extracted")
	if _, err := os.Stat(done); err == nil {
		return t, nil
	}
	if err := os.RemoveAll(t.Dir); err != nil {
		return Tree{}, err
	}
	if err := os.MkdirAll(t.Dir, 0o755); err != nil {
		return Tree{}, err
	}
	archive := exec.Command("git", "-C", root, "archive", rev)
	untar := exec.Command("tar", "-x", "-C", t.Dir)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return Tree{}, err
	}
	untar.Stdin = pipe
	if err := untar.Start(); err != nil {
		return Tree{}, err
	}
	if err := archive.Run(); err != nil {
		_ = untar.Wait()
		return Tree{}, fmt.Errorf("git archive %s: %w", rev, err)
	}
	if err := untar.Wait(); err != nil {
		return Tree{}, fmt.Errorf("tar: %w", err)
	}
	cfg, err := exec.Command("git", "-C", root, "show", overlayRev+":magus.yaml").Output()
	if err != nil {
		return Tree{}, fmt.Errorf("git show %s:magus.yaml: %w", overlayRev, err)
	}
	if err := os.WriteFile(filepath.Join(t.Dir, "magus.yaml"), cfg, 0o644); err != nil {
		return Tree{}, err
	}
	return t, os.WriteFile(done, nil, 0o644)
}

// Read returns a tracked file's text, and fails when its git blob is not the one a span
// names: the span's byte range means nothing against another version.
func (t Tree) Read(path, blob string) (string, error) {
	b, err := os.ReadFile(filepath.Join(t.Dir, path))
	if err != nil {
		return "", err
	}
	if got := gitBlobSHA(b); got != blob {
		return "", fmt.Errorf("%s in %s is blob %s, the span names %s", path, t.Rev[:12], got, blob)
	}
	return string(b), nil
}

// Splice puts middle in place of the byte range [start, end) of text. A range outside
// the text is an error.
func Splice(text string, start, end int, middle string) (string, error) {
	if start < 0 || end < start || end > len(text) {
		return "", fmt.Errorf("range [%d,%d) is outside a %d-byte file", start, end, len(text))
	}
	return text[:start] + middle + text[end:], nil
}

// SpliceName is the sibling file name a spliced variant is written under, beside the
// original so its relative imports hold: dir/name.s<id>.ext.
func SpliceName(path, id string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".s" + id + ext
}

var goPosRE = regexp.MustCompile(`\S+\.go:\d+(:\d+)?`)

// GoChecker type-checks a Go file in process. It checks one file at a time, so a file
// that names its package's other files or a module outside the standard library gets
// errors for what it cannot see; CheckSplice therefore asks for no new error beyond the
// ones the original file has. The standard library resolves from GOROOT source; any
// other import is an empty stand-in package.
type GoChecker struct {
	mu   sync.Mutex
	fset *token.FileSet
	imp  types.Importer
	pkgs map[string]*types.Package
}

// NewGoChecker makes a checker whose import cache lives as long as it does.
func NewGoChecker() *GoChecker {
	fset := token.NewFileSet()
	return &GoChecker{fset: fset, imp: importer.ForCompiler(fset, "source", nil), pkgs: map[string]*types.Package{}}
}

func (g *GoChecker) Import(path string) (*types.Package, error) {
	if p, ok := g.pkgs[path]; ok {
		return p, nil
	}
	if first, _, _ := strings.Cut(path, "/"); !strings.Contains(first, ".") && path != "C" {
		if p, err := g.imp.Import(path); err == nil {
			g.pkgs[path] = p
			return p, nil
		}
	}
	p := types.NewPackage(path, filepath.Base(path))
	p.MarkComplete()
	g.pkgs[path] = p
	return p, nil
}

// Errors parses and type-checks src and returns each problem's message with positions
// stripped. A file that does not parse returns its syntax errors and parsed=false, with
// no type check.
func (g *GoChecker) Errors(path, src string) (msgs []string, parsed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, err := parser.ParseFile(g.fset, path, src, parser.AllErrors|parser.SkipObjectResolution)
	if err != nil {
		for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
			msgs = append(msgs, "syntax: "+goPosRE.ReplaceAllString(line, ""))
		}
		return msgs, false
	}
	conf := types.Config{Importer: g, FakeImportC: true, Error: func(err error) {
		if te, ok := err.(types.Error); ok {
			msgs = append(msgs, goPosRE.ReplaceAllString(te.Msg, ""))
		}
	}}
	_, _ = conf.Check(f.Name.Name, g.fset, []*ast.File{f}, nil)
	slices.Sort(msgs)
	return msgs, true
}

// CheckSplice reports whether the spliced file adds no error the original lacks: every
// message it draws is one the original draws too, with at least as many repeats. A
// spliced file that does not parse never passes.
func (g *GoChecker) CheckSplice(path, spliced string, original []string) bool {
	got, parsed := g.Errors(path, spliced)
	if !parsed {
		return false
	}
	have := map[string]int{}
	for _, m := range original {
		have[m]++
	}
	for _, m := range got {
		if have[m] == 0 {
			return false
		}
		have[m]--
	}
	return true
}
