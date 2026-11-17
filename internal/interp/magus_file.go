package interp

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

func init() { //nolint:gochecknoinits // eager since the first commit, unlike the spells bindings registers lazily; no record says why
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
// loads or a run executes, from one directory listing per directory instead of one
// stat per candidate path, and remembers each imported spell's descriptor. A nil
// *ImportProbes, or one that has been sealed, stats every path and remembers
// nothing, so callers need no nil check. Safe for concurrent use.
type ImportProbes struct {
	mu       sync.Mutex
	sealed   bool
	dirs     map[string]*probeDir
	notSpell map[string]fileStamp
	spellAt  map[spellKey]spellEntry
}

// spellKey names one compile: a spell's descriptor depends on the importing
// directory, a search root for the spell's own imports and the working directory of
// a run-mode load.
type spellKey struct{ path, dir string }

// spellEntry is one recorded compile. notSpell means the module ran and exported no
// spell, so spec is empty.
type spellEntry struct {
	src      string
	spec     spells.Descriptor
	notSpell bool
}

// fileStamp is what a stat says about a file: the size and modification time a
// not-a-spell mark was made against.
type fileStamp struct {
	size    int64
	modTime time.Time
}

type importProbesKey struct{}

// WithImportProbes attaches a fresh ImportProbes to ctx for one workspace load or one
// run. The returned seal ends it: every later probe stats the filesystem again, so no
// answer outlives that unit, including in a long-lived server. A nested attach
// shadows the outer one for the contexts derived from it.
func WithImportProbes(ctx context.Context) (context.Context, func()) {
	p := &ImportProbes{
		dirs:     make(map[string]*probeDir),
		notSpell: make(map[string]fileStamp),
		spellAt:  make(map[spellKey]spellEntry),
	}
	seal := func() {
		p.mu.Lock()
		p.sealed = true
		p.dirs, p.notSpell, p.spellAt = nil, nil, nil
		p.mu.Unlock()
	}
	return context.WithValue(ctx, importProbesKey{}, p), seal
}

// ImportProbesFromContext returns the ImportProbes in force, or nil outside a load
// or a run.
func ImportProbesFromContext(ctx context.Context) *ImportProbes {
	p, _ := ctx.Value(importProbesKey{}).(*ImportProbes)
	return p
}

// IsFile reports whether path exists and is not a directory, following symlinks:
// the answer os.Stat gives.
//
// optimization: answer from a per-load or per-run directory listing instead of os.Stat.
//
//	measured: at workspace load only; describe targets on this repo, 442 stats
//	  to 32 directory opens; BenchmarkResolveLocalSpellImport in bindings -55% sec/op, -62% B/op, -50%
//	  allocs/op (benchstat -col /probes, n=10).
//	trade-off: a file created after its directory was listed is not seen until
//	  the load or run ends.
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

// NotSpell reports whether MarkNotSpell recorded path before the seal and the file
// still has the size and modification time it was marked with. It costs one stat.
func (p *ImportProbes) NotSpell(path string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	marked, ok := p.notSpell[path]
	p.mu.Unlock()
	if !ok {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Size() == marked.size && fi.ModTime().Equal(marked.modTime)
}

// MarkNotSpell records that the file at path is a plain module, not a spell, so a
// later import of it skips reading it again. size and modTime come from a stat of
// the file taken before it was read, so a file rewritten during the read does not
// stay marked.
//
// trade-off: a rewrite of the same size inside the filesystem's timestamp
// granularity is not seen until the run ends.
func (p *ImportProbes) MarkNotSpell(path string, size int64, modTime time.Time) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.sealed {
		p.notSpell[path] = fileStamp{size: size, modTime: modTime}
	}
}

// SpellAt returns what a compile of the absolute path, imported from directory dir,
// recorded for exactly the bytes src. notSpell is true when that compile found a
// module that exports no spell. known is false when nothing was recorded, when the
// file's bytes or the importing directory differ, or after the seal. The descriptor
// shares its slices, maps and pointers, so callers must not modify it; nothing
// writes them after decode.
//
// optimization: compile each imported spell once per path, importing directory and
// bytes.
//
//	measured: importing one spell four times, BenchmarkResolveImportedSpell in
//	  bindings -75% B/op, -75% allocs/op, -70% sec/op (medians of n=6, not
//	  benchstat; the other lines here are n=10).
//	trade-off: the file is still read on every import; a helper module the spell
//	  itself imports is not part of the key, so an edit to it mid-run is not seen
//	  until the run ends.
//	assumes: the key is absolute; a relative import string such as
//	  ../../hack/magusfile/index names a different file from each project.
func (p *ImportProbes) SpellAt(path, dir, src string) (spec spells.Descriptor, notSpell, known bool) {
	if p == nil {
		return spells.Descriptor{}, false, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.spellAt[spellKey{path, dir}]
	if !ok || e.src != src {
		return spells.Descriptor{}, false, false
	}
	return e.spec, e.notSpell, true
}

// RecordSpellAt records a successful compile of the absolute path from directory dir
// over the bytes src. A failed compile is never recorded: only a compile that
// produced a descriptor has an answer to remember. A relative path, or probes that
// have been sealed, record nothing.
func (p *ImportProbes) RecordSpellAt(path, dir, src string, spec spells.Descriptor) {
	p.record(path, dir, spellEntry{src: src, spec: spec})
}

// RecordNotSpellAt records that running the absolute path from directory dir over the
// bytes src found a module that exports no spell. The same rules as RecordSpellAt.
func (p *ImportProbes) RecordNotSpellAt(path, dir, src string) {
	p.record(path, dir, spellEntry{src: src, notSpell: true})
}

func (p *ImportProbes) record(path, dir string, e spellEntry) {
	if p == nil || !filepath.IsAbs(path) {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.sealed {
		p.spellAt[spellKey{path, dir}] = e
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
