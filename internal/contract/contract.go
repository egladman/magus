package contract

import (
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
// spellDir.
const (
	lockPath = "internal/commands/testdata/api.lock"
	spellDir = "internal/spell"
)

var buzzDirs = []string{"gen/decls", "gen/types"}

// digest is the contract's sha256 over lock, the api.lock bytes, and each
// .buzz file under buzzDirs in buzz, in path order. A binary hashes its
// embedded copies and a tree its own, so equal digests mean the binary reads
// the tree as it was built to.
func digest(lock []byte, buzz fs.FS) (string, error) {
	h := sha256.New()
	write := func(name string, body []byte) {
		fmt.Fprintf(h, "%s\x00%d\x00", name, len(body))
		_, _ = h.Write(body) // a hash.Hash never returns an error from Write
	}
	write("api.lock", lock)
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
			if !d.IsDir() && path.Ext(p) == ".buzz" && !hidden(d.Name()) {
				names = append(names, p)
			}
			return nil
		})
		if err != nil {
			return "", fmt.Errorf("contract: walk %s: %w", dir, err)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		body, err := fs.ReadFile(buzz, name)
		if err != nil {
			return "", fmt.Errorf("contract: read %s: %w", name, err)
		}
		write(name, body)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hidden is a name go:embed leaves out, so the tree's walk leaves it out too.
func hidden(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

// Binary is the digest of the contract this binary was built with.
func Binary() string { return binary() }

var binary = sync.OnceValue(func() string {
	d, err := digest(commands.Lock(), spell.GeneratedBuzz())
	if err != nil {
		panic("contract: the embedded contract does not hash: " + err.Error())
	}
	return d
})

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

// Skew is a binary's contract against a tree's.
type Skew struct {
	// Binary is the judging binary's digest.
	Binary string `json:"binary"`
	// Tree is the tree's digest, "" when it carries none.
	Tree string `json:"tree,omitempty"`
	// Root is the tree's checkout.
	Root string `json:"root,omitempty"`
}

// Differs reports a tree that carries a contract other than the binary's.
// A tree that carries none never differs: nothing says what it needs.
func (s Skew) Differs() bool { return s.Tree != "" && s.Tree != s.Binary }

// Check is this binary's contract against the checkout at root's. A tree
// that carries none is no error and never differs.
func Check(root string) (Skew, error) {
	tree, err := Tree(root)
	if errors.Is(err, ErrNotCarried) {
		return Skew{Binary: Binary(), Root: root}, nil
	}
	if err != nil {
		return Skew{}, err
	}
	return Skew{Binary: Binary(), Tree: tree, Root: root}, nil
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
		s.Root, s.Binary, s.Tree, ward.RelinkAt(s.Root))
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
