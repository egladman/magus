package interp

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

func init() {
	// Internal: this is the dispatch path for a magusfile's OWN targets, not a
	// toolchain adapter, and it is not one of the spells a user binds. See
	// [spells.WithInternal] for why the distinction is load-bearing rather than
	// cosmetic.
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(
		"magusfile",
		spells.WithInternal(),
		spells.WithSources("magusfile.buzz"),
		spells.WithInvoker(runTarget),
		spells.WithDeclarationFiles("magusfile.buzz"),
		spells.WithDeclarationDirGlobs("magusfiles/*.buzz"),
	))
}

// runTarget dispatches target via RunDir, returning the target's return value so
// it reaches InvokeResponse.Data. This invoker hardcoded nil, which meant the
// value was discarded a second time even once the interpreter kept it.
// Returns nil on ErrNoMagusfile or ErrUnknownTarget.
func runTarget(ctx context.Context, req spells.InvokeRequest) (any, error) {
	val, err := RunDir(ctx, req.Dir, req.Target, project.ExtraArgs(ctx))
	if errors.Is(err, ErrNoMagusfile) || errors.Is(err, ErrUnknownTarget) {
		// nil value + nil error is precisely right here and is not a sentinel case:
		// a project without this target is SKIPPED, not failed (that is what makes a
		// fan-out tolerant), and it produced no value to report. The (any, error)
		// shape is fixed by spells.WithInvoker, so there is no third slot to say it in.
		//nolint:nilnil // absent target is a skip, and a skip has no value to return
		return nil, nil
	}
	return val, err
}

// ImportProbes answers the path probes import resolution makes while a workspace
// loads, from one directory listing per directory instead of one stat per
// candidate path. A nil *ImportProbes, or one whose load has ended, stats every
// path, so callers need no nil check. Safe for concurrent use.
type ImportProbes struct {
	mu       sync.Mutex
	sealed   bool
	dirs     map[string]*probeDir
	notSpell map[string]struct{}
	spellAt  map[string]*spells.Descriptor
}

type importProbesKey struct{}

// WithImportProbes attaches a fresh ImportProbes to ctx for one workspace load. The
// returned seal ends it: every later probe stats the filesystem again, so no answer
// outlives the load, including in a long-lived server.
func WithImportProbes(ctx context.Context) (context.Context, func()) {
	p := &ImportProbes{
		dirs:     make(map[string]*probeDir),
		notSpell: make(map[string]struct{}),
		spellAt:  make(map[string]*spells.Descriptor),
	}
	seal := func() {
		p.mu.Lock()
		p.sealed = true
		p.dirs, p.notSpell, p.spellAt = nil, nil, nil
		p.mu.Unlock()
	}
	return context.WithValue(ctx, importProbesKey{}, p), seal
}

// ImportProbesFromContext returns the load's ImportProbes, or nil outside a load.
func ImportProbesFromContext(ctx context.Context) *ImportProbes {
	p, _ := ctx.Value(importProbesKey{}).(*ImportProbes)
	return p
}

// IsFile reports whether path exists and is not a directory, following symlinks:
// the answer os.Stat gives.
//
// optimization: answer from a per-load directory listing instead of os.Stat.
//
//	measured: describe targets on this repo, 442 stats to 32 directory opens;
//	  BenchmarkResolveLocalSpellImport in bindings -55% sec/op, -62% B/op, -50%
//	  allocs/op (benchstat -col /probes, n=10).
//	trade-off: a file created during the load after its directory was listed is
//	  not seen until the next load.
//	assumes: a name missing from a listing is missing to stat, unless it could match
//	  an entry case-insensitively (darwin, windows), which falls back to os.Stat.
func (p *ImportProbes) IsFile(path string) bool {
	if p == nil || !filepath.IsAbs(path) {
		return statIsFile(path)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sealed {
		return statIsFile(path)
	}
	mode, found, known := p.dir(filepath.Dir(path)).child(filepath.Base(path))
	if !known {
		return statIsFile(path)
	}
	return found && !mode.IsDir()
}

// NotSpell reports whether MarkNotSpell recorded path during this load.
func (p *ImportProbes) NotSpell(path string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.notSpell[path]
	return ok
}

// MarkNotSpell records that the file at path is a plain module, not a spell, so a
// later import of it this load skips reading it again.
func (p *ImportProbes) MarkNotSpell(path string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.sealed {
		p.notSpell[path] = struct{}{}
	}
}

// SpellAt reports what RecordSpellAt recorded this load for the absolute candidate
// path. known is false when nothing was; a nil spec with known true means no spell
// loads from path. The spec is shared, so callers must not modify it.
//
// optimization: memoize each spell import candidate by its absolute path.
//
//	measured: describe targets on this repo answers 70 of 328 candidates from the
//	  memo; BenchmarkResolveLocalSpellImport in bindings -5% allocs/op, sec/op
//	  within noise (benchstat, n=10).
//	trade-off: a spell file edited during the load keeps its first descriptor
//	  until the next load.
//	assumes: the key is absolute; a relative import string such as
//	  ../../hack/magusfile/index names a different file from each project.
func (p *ImportProbes) SpellAt(path string) (spec *spells.Descriptor, known bool) {
	if p == nil {
		return nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	spec, known = p.spellAt[path]
	return spec, known
}

// RecordSpellAt records the outcome of resolving the absolute candidate path, nil
// when no spell loads from it. A relative path, or a load that has ended, records
// nothing.
func (p *ImportProbes) RecordSpellAt(path string, spec *spells.Descriptor) {
	if p == nil || !filepath.IsAbs(path) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.sealed {
		p.spellAt[path] = spec
	}
}

func statIsFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// probeDir is one directory's listing. absent means no file exists anywhere
// under it; unknown means the listing cannot stand in for stat.
type probeDir struct {
	absent, unknown bool
	entries         map[string]fs.FileMode
	folded          map[string]struct{}
	nonASCII        bool
}

var (
	absentDir  = &probeDir{absent: true}
	unknownDir = &probeDir{unknown: true}
)

// child reports name's type bits in d. known is false when only stat can answer:
// a symlink entry, or a miss that a case-insensitive filesystem could still match.
func (d *probeDir) child(name string) (mode fs.FileMode, found, known bool) {
	switch {
	case d.absent:
		return 0, false, true
	case d.unknown:
		return 0, false, false
	}
	if m, ok := d.entries[name]; ok {
		if m&fs.ModeSymlink != 0 {
			return 0, false, false
		}
		return m, true, true
	}
	if _, ok := d.folded[strings.ToLower(name)]; ok || d.nonASCII || !isASCII(name) {
		return 0, false, false
	}
	return 0, false, true
}

func (p *ImportProbes) dir(path string) *probeDir {
	if d, ok := p.dirs[path]; ok {
		return d
	}
	d := p.list(path)
	p.dirs[path] = d
	return d
}

func (p *ImportProbes) list(path string) *probeDir {
	// A listed parent already says whether path is a directory, so a missing
	// subtree costs no syscall at all.
	if parent := filepath.Dir(path); parent != path {
		if pd, ok := p.dirs[parent]; ok {
			if mode, found, known := pd.child(filepath.Base(path)); known && (!found || !mode.IsDir()) {
				return absentDir
			}
		}
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return absentDir
	}
	if err != nil {
		return unknownDir
	}
	defer f.Close()
	// f.ReadDir rather than os.ReadDir: the order is irrelevant, so skip the sort.
	ents, err := f.ReadDir(-1)
	if err != nil {
		return unknownDir
	}
	d := &probeDir{entries: make(map[string]fs.FileMode, len(ents)), folded: make(map[string]struct{}, len(ents))}
	for _, e := range ents {
		name := e.Name()
		d.entries[name] = e.Type()
		if !isASCII(name) {
			d.nonASCII = true
		}
		d.folded[strings.ToLower(name)] = struct{}{}
	}
	return d
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}
