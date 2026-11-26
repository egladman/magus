package langservice

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

// ModuleTable is the set of modules a script may import, keyed by import path.
type ModuleTable interface {
	// Names lists every import path the table holds.
	Names() []string
	// Module returns the module imported as name; ok is false when the table
	// holds none.
	Module(name string) (ModuleSource, bool)
}

// ModuleSource is one module: its doc and the Buzz declarations of its members.
// Source is parsed only when a query first needs the module's members.
type ModuleSource struct {
	Doc    string
	Source string
}

// Service answers editor queries against one ModuleTable. It is safe for
// concurrent use. Each module's Source is parsed at most once, on the first
// Members call that names it, and the result, error included, is kept.
type Service struct {
	table  ModuleTable
	mu     sync.Mutex
	parsed map[string]parsedModule
}

type parsedModule struct {
	members []Member
	err     error
}

// New returns a Service over table. A nil table holds no modules: every query
// answers as if nothing were importable, and Members and Load report it.
func New(table ModuleTable) *Service {
	return &Service{table: table, parsed: map[string]parsedModule{}}
}

var errNoTable = errors.New("langservice: no module table")

// Members returns the exported functions and values the module imported as
// module declares, in source order. It parses the module's source on the first
// call. A module that does not parse keeps its name and doc for every other
// query, and Members returns its parse error on every call.
func (s *Service) Members(module string) ([]Member, error) {
	members, err := s.members(module)
	return slices.Clone(members), err
}

// Load parses every module in the table now, so later queries never pay for a
// parse. It returns every module's parse error joined, or nil.
func (s *Service) Load() error {
	if s.table == nil {
		return errNoTable
	}
	var errs []error
	for _, name := range s.table.Names() {
		if _, err := s.members(name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// members is Members without the copy, for the queries that only read.
func (s *Service) members(module string) ([]Member, error) {
	src, ok := s.module(module)
	if !ok {
		if s.table == nil {
			return nil, errNoTable
		}
		return nil, fmt.Errorf("langservice: no module %q in the table", module)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.parsed[module]; ok {
		return p.members, p.err
	}
	members, err := parseMembers(src.Source)
	if err != nil {
		err = fmt.Errorf("langservice: module %s: %w", module, err)
	}
	s.parsed[module] = parsedModule{members: members, err: err}
	return members, err
}

func (s *Service) module(name string) (ModuleSource, bool) {
	if s.table == nil {
		return ModuleSource{}, false
	}
	return s.table.Module(name)
}

func (s *Service) names() []string {
	if s.table == nil {
		return nil
	}
	return s.table.Names()
}

// resolveModule maps a bound name at the cursor to a table module. An explicit
// import wins, so an alias resolves to the right module. The bare name comes
// next, so `fs\` offers members before `import "fs"` is typed.
func (s *Service) resolveModule(base, src string) (string, bool) {
	for _, imp := range scanImports(src) {
		if imp.Name == base {
			if name, ok := s.lookupPath(imp.Path); ok {
				return name, true
			}
		}
	}
	if _, ok := s.module(base); ok {
		return base, true
	}
	return "", false
}

// lookupPath finds the table module an import path names. It tries the path
// without its scheme, then the name the path binds. The second serves a table
// keyed by bare names.
func (s *Service) lookupPath(p string) (string, bool) {
	for _, name := range []string{stripScheme(p), moduleBase(p)} {
		if _, ok := s.module(name); ok {
			return name, true
		}
	}
	return "", false
}
