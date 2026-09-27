package knowledge

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// The guard index is what a guard hook asks the graph, written where the graph is built so
// the hook never builds it. A hook runs before every agent command and a graph load costs
// seconds, so the hook reads this one file and stats the sources it was built from instead.
//
// It holds the ids of the kinds the guard's rules translate a search into, and a stamp of
// every source those ids were derived from: a file's mtime and size, and a digest of each
// directory's relevant entries so an added or removed file shows too. Any stamp that no
// longer matches makes that kind non-definitive, which keeps the rule silent until the next
// build. That errs one way only: a rewrite with identical bytes silences a rule for a while,
// and nothing makes it deny on a stale answer.

// GuardSymbol is the pseudo-kind the index lists symbol NAMES under, the spelling a search
// pattern and `magus refs <name>` use, rather than the full symbol ids.
const GuardSymbol = "symbol"

// guardIndexKinds are the node kinds the index lists by id.
var guardIndexKinds = []string{types.KindDiagnostic, types.KindDocSection, types.KindTarget, types.KindFile}

const guardIndexHeader = "magus-guard-index 1"

// GuardIndexPath is the index file under a workspace cache dir.
func GuardIndexPath(cacheDir string) string { return filepath.Join(StoreDir(cacheDir), "guard.idx") }

// GuardRefsPath is the reference-site file beside the index: one line per symbol name and
// file, sorted by name, kept apart because it is several times the index's size and a
// hook reads it only for a search it has already found a symbol in.
func GuardRefsPath(cacheDir string) string {
	return filepath.Join(StoreDir(cacheDir), "guard-refs.idx")
}

// guardSkipDirs are never walked and never counted in a directory digest: VCS internals,
// the cache, and dependency or build trees no index reads.
var guardSkipDirs = map[string]bool{".git": true, ".magus": true, "node_modules": true, "vendor": true, "target": true, "dist": true}

// guardSymbolExt are the source files a symbol index can be built from. A superset is
// safe: an extra stamp can only silence a rule.
var guardSymbolExt = map[string]bool{
	".go": true, ".buzz": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".rs": true, ".py": true, ".c": true, ".h": true, ".cc": true, ".cpp": true,
	".java": true, ".kt": true, ".swift": true, ".rb": true, ".cs": true, ".zig": true,
}

// guardClasses names which kinds a file's content feeds.
func guardClasses(name string) string {
	var c string
	if guardSymbolExt[path.Ext(name)] {
		c += "s"
	}
	if path.Ext(name) == ".md" {
		c += "d"
	}
	if name == "magusfile.buzz" {
		c += "t"
	}
	return c
}

func kindClass(kind string) byte {
	switch kind {
	case GuardSymbol:
		return 's'
	case types.KindDocSection:
		return 'd'
	case types.KindTarget:
		return 't'
	}
	return 0
}

type guardFileStamp struct {
	rel     string
	classes string
	mtime   int64
	size    int64
}

type guardDirStamp struct {
	rel    string
	digest uint64
}

// GuardIndex is a read guard index. Safe for concurrent use.
type GuardIndex struct {
	root         string
	refsPath     string
	symbolsFresh bool
	ids          map[string][]string // sorted
	files        []guardFileStamp
	dirs         []guardDirStamp

	mu    sync.Mutex
	fresh map[string]bool
	refs  []byte // the refs file, read on the first RefSites
	refsE error
}

// WriteGuardIndex writes the index for g, built from the workspace at root. symbolsFresh
// says whether every built symbol index matched its sources when g was assembled; false
// keeps symbol lookups non-definitive whatever the stamps say. The write is atomic.
func WriteGuardIndex(cacheDir, root string, g *Graph, symbolsFresh bool) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	ids := map[string][]string{}
	symbols := map[string]types.KnowledgeNode{}
	for _, n := range g.Nodes() {
		switch {
		case n.Kind == types.KindSymbol:
			if n.Label != "" && !strings.ContainsAny(n.Label, " \n\t") {
				ids[GuardSymbol] = append(ids[GuardSymbol], n.Label)
				symbols[n.ID] = n
			}
		case slices.Contains(guardIndexKinds, n.Kind):
			ids[n.Kind] = append(ids[n.Kind], n.ID)
		}
	}
	files, err := guardSourceFiles(absRoot)
	if err != nil {
		return err
	}
	if err := writeGuardRefs(GuardRefsPath(cacheDir), g, symbols); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nroot %s\nsymbols-fresh %t\n", guardIndexHeader, absRoot, symbolsFresh)
	dirs := map[string]bool{".": true}
	for _, rel := range files {
		info, err := os.Lstat(filepath.Join(absRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "f %s %d %d %s\n", guardClasses(path.Base(rel)), info.ModTime().UnixNano(), info.Size(), rel)
		for d := path.Dir(rel); d != "." && !dirs[d]; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	for _, d := range slices.Sorted(maps.Keys(dirs)) {
		digest, err := guardDirDigest(filepath.Join(absRoot, filepath.FromSlash(d)))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "d %x %s\n", digest, d)
	}
	for _, kind := range slices.Sorted(maps.Keys(ids)) {
		list := slices.Compact(slices.Sorted(slices.Values(ids[kind])))
		for _, id := range list {
			fmt.Fprintf(&b, "i %s %s\n", kind, id)
		}
	}
	return writeAtomic(GuardIndexPath(cacheDir), b.String())
}

// writeGuardRefs writes every file that defines or references a symbol, keyed by the
// symbol's NAME since that is what a search pattern spells: `name\tfile\tcount\tlines`,
// sorted, with lines capped as the graph's own reference edges cap them. A definition
// contributes its declaring line.
func writeGuardRefs(dst string, g *Graph, symbols map[string]types.KnowledgeNode) error {
	var lines []string
	for _, e := range g.Edges() {
		sym, ok := symbols[e.Target]
		if !ok || !strings.HasPrefix(e.Source, types.KindFile+":") {
			continue
		}
		file := strings.TrimPrefix(e.Source, types.KindFile+":")
		var count int
		var at []int
		switch e.Relation {
		case types.RelationReferences:
			count, at, ok = parseRefProvenance(e.Provenance)
			if !ok {
				continue
			}
		case types.RelationDefines:
			count = 1
			if p, l, found := strings.Cut(sym.Source, ":"); found && p == file {
				if n, err := strconv.Atoi(l); err == nil {
					at = []int{n}
				}
			}
		default:
			continue
		}
		var b strings.Builder
		b.WriteString(sym.Label + "\t" + file + "\t" + strconv.Itoa(count) + "\t")
		for i, l := range at {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.Itoa(l))
		}
		lines = append(lines, b.String())
	}
	slices.Sort(lines)
	lines = slices.Compact(lines)
	return writeAtomic(dst, strings.Join(lines, "\n")+"\n")
}

func writeAtomic(dst, content string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+"-*")
	if err != nil {
		return err
	}
	_, err = tmp.WriteString(content)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}

// guardSourceFiles lists the workspace-relative files any indexed kind is derived from,
// minus what version control ignores.
func guardSourceFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if p != root && (guardSkipDirs[d.Name()] || vcs.IsSecondaryCheckout(p)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || guardClasses(d.Name()) == "" {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return dropVCSIgnored(root, files), nil
}

// guardDirDigest hashes the names in dir that could change an indexed kind: its
// subdirectories and its files of an indexed class. Other files come and go (a rebuilt
// binary, an editor's swap file) without saying anything about the index.
func guardDirDigest(dir string) (uint64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	h := fnv.New64a()
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir():
			if guardSkipDirs[name] {
				continue
			}
			name += "/"
		case guardClasses(name) == "":
			continue
		}
		_, _ = h.Write([]byte(name)) // hash.Hash never returns an error
		_, _ = h.Write([]byte{0})
	}
	return h.Sum64(), nil
}

// ReadGuardIndex reads the index for the workspace at root, and reports an error when
// there is none or it was written for another root.
func ReadGuardIndex(cacheDir, root string) (*GuardIndex, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(GuardIndexPath(cacheDir))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	x := &GuardIndex{refsPath: GuardRefsPath(cacheDir), ids: map[string][]string{}, fresh: map[string]bool{}}
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !s.Scan() || s.Text() != guardIndexHeader {
		return nil, fmt.Errorf("knowledge: %s is not a guard index this magus reads", GuardIndexPath(cacheDir))
	}
	for s.Scan() {
		line := s.Text()
		tag, rest, _ := strings.Cut(line, " ")
		switch tag {
		case "root":
			x.root = rest
		case "symbols-fresh":
			x.symbolsFresh = rest == "true"
		case "f":
			fields := strings.SplitN(rest, " ", 4)
			if len(fields) != 4 {
				continue
			}
			mtime, _ := strconv.ParseInt(fields[1], 10, 64)
			size, _ := strconv.ParseInt(fields[2], 10, 64)
			x.files = append(x.files, guardFileStamp{rel: fields[3], classes: fields[0], mtime: mtime, size: size})
		case "d":
			digest, rel, ok := strings.Cut(rest, " ")
			if !ok {
				continue
			}
			v, err := strconv.ParseUint(digest, 16, 64)
			if err != nil {
				continue
			}
			x.dirs = append(x.dirs, guardDirStamp{rel: rel, digest: v})
		case "i":
			kind, id, ok := strings.Cut(rest, " ")
			if ok {
				x.ids[kind] = append(x.ids[kind], id)
			}
		}
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if x.root != absRoot {
		return nil, fmt.Errorf("knowledge: the guard index was written for %s, not %s", x.root, absRoot)
	}
	return x, nil
}

// IDs are the ids of kind, sorted; for GuardSymbol, the symbol names.
func (x *GuardIndex) IDs(kind string) []string { return x.ids[kind] }

// Has reports whether id is listed under kind.
func (x *GuardIndex) Has(kind, id string) bool {
	_, ok := slices.BinarySearch(x.ids[kind], id)
	return ok
}

// RefSites lists every file that defines or references a symbol named name, one entry per
// file, with its occurrence count and the first lines. Every symbol of that name counts,
// since a text search cannot tell them apart. Whether the answer is current is
// Fresh(GuardSymbol)'s to say. The refs file is read whole on the first call: a name lookup
// is one substring scan of it, cheaper than seeking, and the file is a few megabytes.
func (x *GuardIndex) RefSites(name string) ([]types.KnowledgeRefSite, error) {
	x.mu.Lock()
	if x.refs == nil && x.refsE == nil {
		x.refs, x.refsE = os.ReadFile(x.refsPath)
	}
	data, err := x.refs, x.refsE
	x.mu.Unlock()
	if err != nil {
		return nil, err
	}
	key := []byte(name + "\t")
	pos := 0
	if !bytes.HasPrefix(data, key) {
		i := bytes.Index(data, append([]byte{'\n'}, key...))
		if i < 0 {
			return nil, nil
		}
		pos = i + 1
	}
	byFile := map[string]*types.KnowledgeRefSite{}
	var order []string
	for pos < len(data) && bytes.HasPrefix(data[pos:], key) {
		end := bytes.IndexByte(data[pos:], '\n')
		if end < 0 {
			end = len(data) - pos
		}
		fields := strings.Split(string(data[pos+len(key):pos+end]), "\t")
		pos += end + 1
		if len(fields) != 3 {
			continue
		}
		count, _ := strconv.Atoi(fields[1])
		site := byFile[fields[0]]
		if site == nil {
			site = &types.KnowledgeRefSite{File: fields[0]}
			byFile[fields[0]] = site
			order = append(order, fields[0])
		}
		site.Count += count
		for _, l := range strings.Split(fields[2], ",") {
			if n, err := strconv.Atoi(l); err == nil {
				site.Lines = append(site.Lines, n)
			}
		}
	}
	out := make([]types.KnowledgeRefSite, 0, len(order))
	for _, f := range order {
		s := byFile[f]
		slices.Sort(s.Lines)
		out = append(out, *s)
	}
	return out, nil
}

// Fresh reports whether every source kind's ids were derived from is unchanged since the
// index was written, so a query of the graph answers what the index says. Diagnostics are
// compiled into the binary and always fresh. It stats the sources once per kind.
func (x *GuardIndex) Fresh(kind string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if v, ok := x.fresh[kind]; ok {
		return v
	}
	v := x.computeFresh(kind)
	x.fresh[kind] = v
	return v
}

func (x *GuardIndex) computeFresh(kind string) bool {
	if kind == types.KindDiagnostic {
		return true
	}
	if kind == GuardSymbol && !x.symbolsFresh {
		return false
	}
	// A file node comes from whichever index read the file, so every stamp counts for it.
	class := kindClass(kind)
	if class == 0 && kind != types.KindFile {
		return false
	}
	checks := make([]func() bool, 0, len(x.dirs)+len(x.files))
	for _, d := range x.dirs {
		checks = append(checks, func() bool {
			digest, err := guardDirDigest(filepath.Join(x.root, filepath.FromSlash(d.rel)))
			return err == nil && digest == d.digest
		})
	}
	for _, f := range x.files {
		if class != 0 && strings.IndexByte(f.classes, class) < 0 {
			continue
		}
		checks = append(checks, func() bool {
			info, err := os.Lstat(filepath.Join(x.root, filepath.FromSlash(f.rel)))
			return err == nil && info.ModTime().UnixNano() == f.mtime && info.Size() == f.size
		})
	}
	return allHold(checks)
}

// allHold runs checks across a few goroutines, since each is a syscall that mostly waits,
// and stops early once one fails.
func allHold(checks []func() bool) bool {
	const workers = 8
	var failed atomic.Bool
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := w; i < len(checks) && !failed.Load(); i += workers {
				if !checks[i]() {
					failed.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	return !failed.Load()
}
