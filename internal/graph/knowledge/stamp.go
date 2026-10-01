package knowledge

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/vcs"
)

// ShardClass groups the shards assembled from one input set. It is the granularity an
// input stamp is recorded and compared at: the passes inside a class read each other's
// nodes (the I/O pass resolves globs against the doc and buzz shards, @dirs rolls up every
// path-bearing one), so a class is reassembled whole or not at all.
type ShardClass string

const (
	// ClassDomain is every shard the default graph merges except @runtime: the registry,
	// the projects, and everything read off the working tree and its history.
	ClassDomain ShardClass = "domain"
	// ClassRuntime is @runtime, read from local run records rather than from sources.
	ClassRuntime ShardClass = "runtime"
	// ClassSymbols is the per-project @symbols shards, read from the SCIP indexes.
	ClassSymbols ShardClass = "symbols"
	// ClassCoverage is the @coverage overlay.
	ClassCoverage ShardClass = "coverage"
	// ClassSession is the @session overlay. It resolves against every other class's
	// nodes, so its stamp folds in all of theirs.
	ClassSession ShardClass = "session"
)

// DefaultClasses are merged into the default graph; LazyClasses are persisted beside them
// and merged only on the symbol-load path (see isLazyShard).
var (
	DefaultClasses = []ShardClass{ClassDomain, ClassRuntime}
	LazyClasses    = []ShardClass{ClassSymbols, ClassCoverage, ClassSession}
	AllClasses     = []ShardClass{ClassDomain, ClassRuntime, ClassSymbols, ClassCoverage, ClassSession}
)

// shardClass names the class a shard belongs to. One switch, for the reason isLazyShard
// gives: a new lazy shard that missed it would be stamped as domain and go stale silently.
func shardClass(name string) ShardClass {
	switch {
	case isSymbolsShard(name):
		return ClassSymbols
	case isCoverageShard(name):
		return ClassCoverage
	case isSessionShard(name):
		return ClassSession
	case isRuntimeShard(name):
		return ClassRuntime
	default:
		return ClassDomain
	}
}

// Stamps maps a class to a digest of every input its shards read. A class that is absent,
// or mapped to "", has no stamp and is reassembled on every build: a stamp the caller
// could not compute must never read as a match.
type Stamps map[ShardClass]string

// racyWindow is how recently a file may have been modified before its mtime stops being
// evidence on its own. A filesystem with coarse timestamps (HFS+ and FAT keep seconds) can
// give two writes one mtime, so a file stamped inside that window has its content hashed
// too, the way git re-reads a racily clean index entry.
const racyWindow = 2 * time.Second

// InputHash folds input identities into one stamp. An identity is what a stat can see:
// whether a path exists, its kind, size and modification time.
type InputHash struct {
	h   hash.Hash
	buf []byte
	now time.Time
}

// NewInputHash starts a stamp labeled with label, so two stamps over the same inputs for
// different classes never collide.
func NewInputHash(label string) *InputHash {
	s := &InputHash{h: sha256.New(), buf: make([]byte, 0, 256), now: time.Now()}
	s.String(label)
	return s
}

// String folds one value, length-prefixed so adjacent values cannot run together.
func (s *InputHash) String(v string) {
	s.buf = binary.BigEndian.AppendUint64(s.buf[:0], uint64(len(v)))
	s.buf = append(s.buf, v...)
	_, _ = s.h.Write(s.buf) // hash.Hash never errors
}

// Path folds the identity of whatever is at p, following symlinks: absent, a file's size
// and mtime, or for a directory every entry beneath it in walk order.
func (s *InputHash) Path(p string) {
	s.String(p)
	info, err := os.Stat(p)
	if err != nil {
		s.String("absent")
		return
	}
	if !info.IsDir() {
		s.file(p, info)
		return
	}
	_ = filepath.WalkDir(p, func(sub string, d fs.DirEntry, err error) error {
		if err != nil || sub == p {
			return nil //nolint:nilerr // an unreadable entry contributes its absence below
		}
		s.String(sub)
		info, err := os.Stat(sub)
		if err != nil {
			s.String("absent")
			return nil
		}
		if !info.IsDir() {
			s.file(sub, info)
		}
		return nil
	})
}

// Binary folds the identity of the executable at p without ever reading it: a build
// rewrites the whole file, so its size and mtime move with it, and hashing a freshly
// built binary's tens of megabytes inside the racy window would also make the stamp flip
// the moment the binary aged out of it.
func (s *InputHash) Binary(p string) {
	s.String(p)
	info, err := os.Stat(p)
	if err != nil {
		s.String("absent")
		return
	}
	s.buf = binary.BigEndian.AppendUint64(s.buf[:0], uint64(info.Size()))
	s.buf = binary.BigEndian.AppendUint64(s.buf, uint64(info.ModTime().UnixNano()))
	_, _ = s.h.Write(s.buf)
}

// Dirs folds the modification time of the directory at p and of every directory beneath
// it, and nothing about the files in them. That suffices for a store whose files are
// only ever created, renamed into place or removed, since each of those moves its
// directory's mtime, and it costs one stat per directory rather than per file.
func (s *InputHash) Dirs(p string) {
	s.String(p)
	_ = filepath.WalkDir(p, func(sub string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // an unreadable directory contributes nothing
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished between the listing and the stat
		}
		s.String(sub)
		s.buf = binary.BigEndian.AppendUint64(s.buf[:0], uint64(info.ModTime().UnixNano()))
		_, _ = s.h.Write(s.buf)
		if s.now.Sub(info.ModTime()) >= racyWindow {
			return nil
		}
		// Too recent for the mtime alone (see racyWindow): fold the entries themselves.
		entries, err := os.ReadDir(sub)
		if err != nil {
			return nil //nolint:nilerr // vanished between the walk and the read
		}
		for _, e := range entries {
			s.String(e.Name())
			if fi, err := e.Info(); err == nil && !fi.IsDir() {
				s.file(filepath.Join(sub, e.Name()), fi)
			}
		}
		return nil
	})
}

// Sum returns the stamp.
func (s *InputHash) Sum() string { return hex.EncodeToString(s.h.Sum(nil)) }

// file folds one file's identity, plus its content when its mtime is too recent to trust.
func (s *InputHash) file(abs string, info fs.FileInfo) {
	s.buf = binary.BigEndian.AppendUint32(s.buf[:0], uint32(info.Mode().Type()))
	s.buf = binary.BigEndian.AppendUint64(s.buf, uint64(info.Size()))
	s.buf = binary.BigEndian.AppendUint64(s.buf, uint64(info.ModTime().UnixNano()))
	_, _ = s.h.Write(s.buf)
	if s.now.Sub(info.ModTime()) >= racyWindow {
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		s.String("unreadable")
		return
	}
	defer f.Close()
	if _, err := io.Copy(s.h, f); err != nil {
		s.String("unreadable")
	}
}

// The tree scans, as bits: a file's bit is set when every directory between the root and
// it is one that scan descends into. findBuzzFiles and findCommentSources skip the same
// directories, so they share one.
const (
	walkDocs uint8 = 1 << iota
	walkBuzz
	walkPackages
	walkMarkers
	walkAll = walkDocs | walkBuzz | walkPackages | walkMarkers
)

// treeFile is one non-directory entry the walk found.
type treeFile struct {
	rel   string // slash-separated, relative to the root
	scans uint8
	info  fs.FileInfo // Lstat: a symlink is reported as one
}

// TreeWalk is one walk of the workspace serving every tree scan the assembler runs. It
// descends every directory at least one scan descends into, recording which scans admit
// each file, so the five scans read one listing instead of walking the tree five times,
// and the domain stamp hashes exactly the files those scans can read.
type TreeWalk struct {
	root  string
	files []treeFile // sorted by rel
}

// WalkTree walks root once. Unreadable entries are skipped, as each scan skipped them.
func WalkTree(root string) *TreeWalk {
	w := &TreeWalk{root: root}
	scansOf := map[string]uint8{root: walkAll}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // WalkDir: skip unreadable entries, continue walking
		}
		if p == root {
			return nil
		}
		parent := scansOf[filepath.Dir(p)]
		if d.IsDir() {
			scans := parent & dirScans(p, d.Name())
			if scans == 0 {
				return fs.SkipDir
			}
			scansOf[p] = scans
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished between the listing and the stat
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil //nolint:nilerr // outside the root cannot happen under WalkDir
		}
		w.files = append(w.files, treeFile{rel: filepath.ToSlash(rel), scans: parent, info: info})
		return nil
	})
	slices.SortFunc(w.files, func(a, b treeFile) int { return strings.Compare(a.rel, b.rel) })
	return w
}

// dirScans reports which scans descend into the directory at p named name. Each clause is
// the skip rule of the scan it names, so a change to one of them belongs here too.
func dirScans(p, name string) uint8 {
	var scans uint8
	secondary := -1 // vcs.IsSecondaryCheckout stats; asked at most once, and only if needed
	isSecondary := func() bool {
		if secondary < 0 {
			secondary = 0
			if vcs.IsSecondaryCheckout(p) {
				secondary = 1
			}
		}
		return secondary == 1
	}
	if !skipDocWalkName(name) && !isSecondary() {
		scans |= walkDocs
	}
	if !project.IsIgnoreDir(name) && name != "testdata" {
		scans |= walkBuzz
	}
	if name == "gen" || (!project.IsIgnoreDir(name) && name != "testdata") {
		scans |= walkPackages
	}
	switch {
	case strings.HasPrefix(name, "."), name == "testdata", name == "vendor", name == "node_modules", name == "target":
	default:
		if !isSecondary() {
			scans |= walkMarkers
		}
	}
	return scans
}

// Digest identifies the walked tree: every file's path, kind, size and mtime, with a
// symlink's target folded in, since the scans read through it.
func (w *TreeWalk) Digest() string {
	s := NewInputHash("tree")
	for _, f := range w.files {
		s.String(f.rel)
		s.buf = append(s.buf[:0], f.scans)
		_, _ = s.h.Write(s.buf)
		abs := filepath.Join(w.root, filepath.FromSlash(f.rel))
		s.file(abs, f.info)
		if f.info.Mode()&fs.ModeSymlink != 0 {
			if target, err := os.Stat(abs); err == nil {
				s.file(abs, target)
			} else {
				s.String("dangling")
			}
		}
	}
	return s.Sum()
}

// Contains reports whether the walk saw the file at rel.
func (w *TreeWalk) Contains(rel string) bool {
	_, ok := slices.BinarySearchFunc(w.files, rel, func(f treeFile, rel string) int { return strings.Compare(f.rel, rel) })
	return ok
}
