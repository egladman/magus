package deviation

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

// File is the analyzers file's name at the root of each module: YAML a person
// edits, one top-level key per analyzer.
const File = "analyzers.yaml"

// Exit is one departure a person keeps. A rule reading exits takes no
// //nolint, so they are its only way out.
type Exit struct {
	// Path is the slash-separated path of the declaring file inside the module.
	Path string `yaml:"path"`

	Symbol string `yaml:"symbol"`
	Reason string `yaml:"reason"`
}

type section struct {
	Exits []Exit `yaml:"exits"`
}

// Ledger holds one analyzer's exits for one project.
type Ledger struct {
	exits map[string]Exit
}

// Load reads rule's key from the analyzers file in dir. No file, or no key,
// holds no exits. An unknown field, a reasonless exit or a duplicate is an
// error. With check, so is an exit naming a file not under dir.
func Load(rule, dir string, check bool) (*Ledger, error) {
	path := filepath.Join(dir, File)
	display := path
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil {
			display = filepath.ToSlash(rel)
		}
	}
	var s section
	if err := source.PolicyKey(path, rule, &s); err != nil {
		return nil, fmt.Errorf("%s: %s: %w", rule, display, err)
	}
	l := &Ledger{exits: map[string]Exit{}}
	for _, x := range s.Exits {
		if x.Path == "" || x.Symbol == "" {
			return nil, fmt.Errorf("%s: %s: an exit needs a path and a symbol", rule, display)
		}
		if strings.TrimSpace(x.Reason) == "" {
			return nil, fmt.Errorf("%s: %s: the exit for %s in %s gives no reason: a person writes why it stays", rule, display, x.Symbol, x.Path)
		}
		k := x.Path + "#" + x.Symbol
		if _, dup := l.exits[k]; dup {
			return nil, fmt.Errorf("%s: %s: %s in %s is listed twice", rule, display, x.Symbol, x.Path)
		}
		if check {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(x.Path))); err != nil {
				return nil, fmt.Errorf("%s: %s: the exit for %s names %s, which is not a file under %s: delete or fix the row", rule, display, x.Symbol, x.Path, dir)
			}
		}
		l.exits[k] = x
	}
	return l, nil
}

// Accepts reports whether an exit names symbol declared in path.
func (l *Ledger) Accepts(path, symbol string) bool {
	_, ok := l.exits[path+"#"+symbol]
	return ok
}

// In returns the exits naming a function declared in path, by symbol.
func (l *Ledger) In(path string) []Exit {
	var out []Exit
	for _, x := range l.exits {
		if x.Path == path {
			out = append(out, x)
		}
	}
	slices.SortFunc(out, func(a, b Exit) int { return strings.Compare(a.Symbol, b.Symbol) })
	return out
}

// Ledgers reads each project's file once. It is safe for concurrent use, since
// golangci-lint runs packages in parallel.
type Ledgers struct {
	rule   string
	module string

	mu    sync.Mutex
	byDir map[string]loaded
}

type loaded struct {
	ledger *Ledger
	err    error
}

// NewLedgers reads rule's exits for files of module. An empty module reads the
// one analyzers file in the working directory, with paths taken from the
// package import path.
func NewLedgers(rule, module string) *Ledgers {
	return &Ledgers{rule: rule, module: module, byDir: map[string]loaded{}}
}

// Place returns the ledger of the project holding f, the nearest directory at
// or above f with a go.mod, and f's slash path inside it. It reports false for
// a file outside the module or outside any project, which no exit can name.
func (ls *Ledgers) Place(pass *analysis.Pass, f *ast.File) (*Ledger, string, bool, error) {
	if ls.module == "" {
		rel, ok := source.Rel(pass, "", f)
		l, err := ls.at(".")
		return l, rel, ok, err
	}
	if _, ok := source.Rel(pass, ls.module, f); !ok {
		return nil, "", false, nil
	}
	name := source.Name(pass, f)
	dir, ok := owner(name)
	if !ok {
		return nil, "", false, nil
	}
	rel, err := filepath.Rel(dir, name)
	if err != nil {
		return nil, "", false, nil
	}
	l, err := ls.at(dir)
	return l, filepath.ToSlash(rel), true, err
}

func (ls *Ledgers) at(dir string) (*Ledger, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if l, ok := ls.byDir[dir]; ok {
		return l.ledger, l.err
	}
	ledger, err := Load(ls.rule, dir, ls.module != "")
	ls.byDir[dir] = loaded{ledger: ledger, err: err}
	return ledger, err
}

// owner copies closedswitch's: that package reads its own key and type, and
// neither imports the other.
func owner(file string) (string, bool) {
	for d := filepath.Dir(file); ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			return d, true
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}
