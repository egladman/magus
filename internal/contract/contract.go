package contract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// The contract is every committed file a magus binary embeds and a load reads:
// api.lock, and the generated Buzz under internal/spell/gen. buzzDirs are under
// spellDir. They mirror magus's own go:embed lines; no workspace declares them.
const (
	lockPath = "internal/commands/testdata/api.lock"
	spellDir = "internal/spell"
)

var buzzDirs = []string{"gen/decls", "gen/types"}

// digest is the contract's sha256 over lock and the generated Buzz under
// buzzDirs in buzz. A binary hashes its embedded copies and a tree its own,
// so equal digests mean the binary reads the tree as it was built to.
func digest(lock []byte, buzz fs.FS) (string, error) {
	var names []string
	for _, dir := range buzzDirs {
		err := fs.WalkDir(buzz, dir, func(p string, d fs.DirEntry, err error) error {
			// An absent directory holds no generated Buzz.
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipDir
			}
			if err != nil {
				return err
			}
			if !d.IsDir() && embedded(p) {
				names = append(names, p)
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("contract: walk %s: %w", dir, err)
		}
	}
	return digestFiles(lock, names, func(name string) ([]byte, error) { return fs.ReadFile(buzz, name) })
}

// digestFiles hashes the api.lock bytes, then each of names in path order.
// names are under spellDir, and read returns each one's bytes.
func digestFiles(lock []byte, names []string, read func(name string) ([]byte, error)) (string, error) {
	h := sha256.New()
	write := func(name string, body []byte) {
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(body))
		_, _ = h.Write(body) // a hash.Hash never returns an error from Write
	}
	write("api.lock", lock)
	for _, name := range slices.Sorted(slices.Values(names)) {
		body, err := read(name)
		if err != nil {
			return "", fmt.Errorf("contract: read %s: %w", name, err)
		}
		write(name, body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// embedded reports a path under spellDir that go:embed keeps.
func embedded(p string) bool {
	return path.Ext(p) == ".buzz" && !hidden(path.Base(p))
}

// hidden is a name go:embed leaves out, so the tree's walk leaves it out too.
func hidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Binary is the digest of the contract this binary was built with. It fails
// when the embedded contract does not hash.
func Binary() (string, error) { return binary() }

var binary = sync.OnceValues(func() (string, error) {
	return embeddedDigest(commands.Lock(), spell.GeneratedBuzz())
})

// embeddedDigest is digest over the copies a binary embeds, with the error
// saying whose contract failed to hash.
func embeddedDigest(lock []byte, buzz fs.FS) (string, error) {
	d, err := digest(lock, buzz)
	if err != nil {
		return "", fmt.Errorf("hash the embedded contract: %w", err)
	}
	return d, nil
}

// ErrNotCarried is a tree that holds no contract: a workspace that is not a
// checkout of magus.
var ErrNotCarried = errors.New("the tree carries no magus contract")

// Tree is the digest of the contract the checkout at root carries. It fails
// with ErrNotCarried when root holds no api.lock.
func Tree(root string) (string, error) {
	lock, err := readLock(root)
	if err != nil {
		return "", err
	}
	return digest(lock, os.DirFS(filepath.Join(root, spellDir)))
}

func readLock(root string) ([]byte, error) {
	lock, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(lockPath)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotCarried
	}
	if err != nil {
		return nil, fmt.Errorf("contract: %w", err)
	}
	return lock, nil
}

// Revision is a checkout's version control, read at a revision.
type Revision interface {
	types.RevisionFileReader
	types.RevisionLister
}

// treeAt is Tree for the files the checkout at root holds at rev, read
// through vc; the working tree is never read. root is the repository's
// root. It fails with ErrNotCarried when rev holds no api.lock.
func treeAt(ctx context.Context, vc Revision, root, rev string) (string, error) {
	lock, err := readLockAt(ctx, vc, root, rev)
	if err != nil {
		return "", err
	}
	var names []string
	for _, dir := range buzzDirs {
		files, err := vc.ListFilesAt(ctx, root, rev, path.Join(spellDir, dir))
		if err != nil {
			return "", fmt.Errorf("contract: list %s at %s: %w", dir, rev, err)
		}
		for _, f := range files {
			if rel, ok := strings.CutPrefix(f, spellDir+"/"); ok && embedded(rel) {
				names = append(names, rel)
			}
		}
	}
	return digestFiles(lock, names, func(name string) ([]byte, error) {
		body, err := vc.ReadFileAt(ctx, root, rev, path.Join(spellDir, name))
		return []byte(body), err
	})
}

// readLockAt is readLock at rev. ReadFileAt cannot tell an absent file from
// a failed read, so the listing says whether rev carries one.
func readLockAt(ctx context.Context, vc Revision, root, rev string) ([]byte, error) {
	files, err := vc.ListFilesAt(ctx, root, rev, path.Dir(lockPath))
	if err != nil {
		return nil, fmt.Errorf("contract: list %s at %s: %w", path.Dir(lockPath), rev, err)
	}
	if !slices.Contains(files, lockPath) {
		return nil, ErrNotCarried
	}
	lock, err := vc.ReadFileAt(ctx, root, rev, lockPath)
	if err != nil {
		return nil, fmt.Errorf("contract: %w", err)
	}
	return []byte(lock), nil
}

// Skew is a binary's contract against a tree's.
type Skew struct {
	// Binary is the judging binary's digest.
	Binary string `json:"binary"`
	// Tree is the tree's digest, "" when it carries none.
	Tree string `json:"tree,omitempty"`
	// Root is the tree's checkout.
	Root string `json:"root,omitempty"`
	// Revision is the revision Tree was read at, "" for Root's working tree.
	Revision string `json:"revision,omitempty"`
}

// TreeName names the tree s compares: Root's working tree, or Root at
// Revision.
func (s Skew) TreeName() string {
	if s.Revision == "" {
		return s.Root
	}
	return fmt.Sprintf("%s at %.12s", s.Root, s.Revision)
}

// Differs reports a tree that carries a contract other than the binary's.
// A tree that carries none never differs: nothing says what it needs.
func (s Skew) Differs() bool { return s.Tree != "" && s.Tree != s.Binary }

// Check is this binary's contract against the checkout at root's. A tree
// that carries none is no error and never differs.
func Check(root string) (Skew, error) {
	return skew(Skew{Root: root}, func() (string, error) { return Tree(root) })
}

// CheckAt is Check against the tree at rev, read through vc. A leased call
// is judged by its authority's revision (ADR 0045), so neither the root
// moving nor the worker's own edits read as skew.
func CheckAt(ctx context.Context, vc Revision, root, rev string) (Skew, error) {
	return skew(Skew{Root: root, Revision: rev}, func() (string, error) { return treeAt(ctx, vc, root, rev) })
}

// skew fills s with this binary's digest and the one tree computes.
func skew(s Skew, tree func() (string, error)) (Skew, error) {
	bin, err := Binary()
	if err != nil {
		return Skew{}, err
	}
	s.Binary = bin
	sum, err := tree()
	if errors.Is(err, ErrNotCarried) {
		return s, nil
	}
	if err != nil {
		return Skew{}, err
	}
	s.Tree = sum
	return s, nil
}

// Diagnostic is the skew as MGS1021 naming the one remedy, nil when the
// contracts agree.
func (s Skew) Diagnostic() *types.DiagnosticError {
	if !s.Differs() {
		return nil
	}
	return types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus,
		"this magus was built from another contract than %s carries "+
			"(binary %.12s, tree %.12s); relink it, alone on its line: `%s`",
		s.TreeName(), s.Binary, s.Tree, ward.RelinkAt(s.Root))
}

// ReadsFormat reports whether a binary built from the checkout at root reads
// rows of format, as its api.lock says. It fails with ErrNotCarried when root
// holds no api.lock.
func ReadsFormat(root string, format types.APIVersion) (bool, error) {
	lock, err := readLock(root)
	if err != nil {
		return false, err
	}
	lines := strings.Split(string(lock), "\n")
	return slices.Contains(lines, "store "+format.String()), nil
}
