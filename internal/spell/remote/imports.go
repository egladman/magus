package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/oci"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// UpdateHint is the remedy every stale or missing pin names. Only the update charm
// resolves a tag, so it is the one fix; spell-lock is the target name this repository
// and the docs use for the target that writes magus.lock. The CLI form is named too:
// when the magusfile owning that target imports the stale spell itself, it cannot
// load until the lock is repaired.
const UpdateHint = "run the target that writes magus.lock with the update charm (`magus run spell-lock:update`), " +
	"or `magus spell lock --update` when that magusfile imports this spell itself"

// Imports is a workspace's declared spell imports, resolved once per workspace load:
// each remote spell read from magus.lock, verified into the cache, and laid out under
// View by its import path, and each override pointing at a workspace directory. The
// zero value and nil both declare nothing. Safe for concurrent use; it never changes
// after LoadImports returns.
type Imports struct {
	view      string
	remote    map[string]string // import path -> <view>/<path>
	overrides map[string]string // import path -> absolute override dir
	// failed holds the remote paths whose pin could not be served, reported when an
	// import names one rather than at load, so a stale lock never stops the workspace
	// from loading the target that repairs it.
	failed map[string]error
}

// LoadOptions configures LoadImports and Relock.
type LoadOptions struct {
	// CacheRoot overrides the user cache, as Options.CacheRoot does.
	CacheRoot string
	// Client returns the registry client for host. nil pulls anonymously over
	// http.DefaultClient. LoadImports calls it only when a spell is not cached.
	Client func(ctx context.Context, host string) (*oci.Client, error)
	// Embedded reports whether name is a spell this magus ships, so an override of
	// magus/spell/<name> can be refused when there is nothing to replace. nil accepts
	// every name.
	Embedded func(name string) bool
}

func (o LoadOptions) client(ctx context.Context, host string) (*oci.Client, error) {
	if o.Client == nil {
		return &oci.Client{}, nil
	}
	return o.Client(ctx, host)
}

// resolveOptions defers the registry client, and so any secret behind it, to the pull
// that needs it.
func (o LoadOptions) resolveOptions(host string) Options {
	return Options{
		CacheRoot: o.CacheRoot,
		Connect:   func(ctx context.Context) (*oci.Client, error) { return o.client(ctx, host) },
	}
}

// LoadImports resolves the spells cfg declares for the workspace at root. It reads
// magus.lock once, and only when a remote spell is declared, and it never resolves a
// tag. A declaration the lock does not pin, or pins for another tag, is MGS1043, and a
// digest the registry or cache cannot match is MGS1042; both are held against that one
// path and returned by Dir, since the fix is a run of the update charm and the target
// carrying it has to load first. An override whose directory holds no spell is MGS1044,
// and a workspace directory at a declared remote path that no override claims is
// MGS1002; those are edits to the workspace, so they fail the load. Every failure is
// reported, not just the first.
func LoadImports(ctx context.Context, root string, cfg config.SpellsConfig, opts LoadOptions) (*Imports, error) {
	im := &Imports{remote: map[string]string{}, overrides: map[string]string{}, failed: map[string]error{}}
	var remote []string
	var errs []error
	for _, path := range slices.Sorted(maps.Keys(cfg.Imports)) {
		decl := cfg.Imports[path]
		if decl.Path == "" {
			remote = append(remote, path)
			continue
		}
		dir, err := overrideDir(root, path, decl.Path, opts.Embedded)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		im.overrides[path] = dir
	}
	for _, path := range remote {
		if shadow := filepath.Join(root, filepath.FromSlash(path)); isDir(shadow) {
			errs = append(errs, types.DiagnosticErrorf(types.SpellShadowed,
				"spell import %q is declared remote in magus.yaml but %s also holds it, and a remote import never reads the workspace; "+
					"delete the directory, or declare `path: %s` under spells.%s to use it instead",
				path, shadow, filepath.ToSlash(path), path))
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	if len(remote) == 0 {
		return im, nil
	}
	lock, err := ReadLock(root)
	if err != nil {
		for _, path := range remote {
			im.failed[path] = err
		}
		return im, nil
	}
	var refs []Ref
	srcs := make(map[string]string, len(remote))
	for _, path := range remote {
		ref, err := frozenRef(path, cfg.Imports[path].Tag, lock)
		if err == nil {
			srcs[path], err = Resolve(ctx, ref, opts.resolveOptions(ref.OCI.Registry))
		}
		if err != nil {
			im.failed[path] = err
			delete(srcs, path)
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return im, nil
	}
	cacheRoot, err := Options{CacheRoot: opts.CacheRoot}.cacheRoot()
	if err != nil {
		return nil, err
	}
	if im.view, err = materialize(cacheRoot, refs, srcs); err != nil {
		return nil, err
	}
	for _, ref := range refs {
		im.remote[ref.Import] = filepath.Join(im.view, filepath.FromSlash(ref.Import))
	}
	return im, nil
}

// frozenRef pins a declared remote path to the digest the lock records for it,
// refusing a declaration the lock does not answer for its current tag.
func frozenRef(path, tag string, lock Lock) (Ref, error) {
	e, ok := lock.Spells[path]
	switch {
	case !ok:
		return Ref{}, types.DiagnosticErrorf(types.RemoteSpellLockStale,
			"spell %s is declared in magus.yaml but %s pins no digest for it; %s", path, LockFile, UpdateHint)
	case e.Tag != tag:
		return Ref{}, types.DiagnosticErrorf(types.RemoteSpellLockStale,
			"spell %s: magus.yaml tracks tag %q but %s was written for %q; %s", path, tag, LockFile, e.Tag, UpdateHint)
	}
	return Pinned(path, e.Digest)
}

// overrideDir resolves a declared override to the absolute directory holding its
// spell.buzz. The declaration is the acknowledgment, so it must point at a spell.
func overrideDir(root, importPath, rel string, embedded func(string) bool) (string, error) {
	if name, ok := strings.CutPrefix(importPath, spells.ModulePrefix); ok && embedded != nil && !embedded(name) {
		return "", types.DiagnosticErrorf(types.SpellOverrideInvalid,
			"magus.yaml spells.%s replaces an embedded spell this magus does not ship; check the name with `magus describe spells`", importPath)
	}
	dir := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(filepath.Join(dir, entryFile))
	if err != nil || !info.Mode().IsRegular() {
		return "", types.DiagnosticErrorf(types.SpellOverrideInvalid,
			"magus.yaml spells.%s: path %s holds no %s, so there is no spell to replace it with", importPath, rel, entryFile)
	}
	return dir, nil
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// View is the directory remote spells are laid out under by import path, so
// <View>/<import path>/spell.buzz is each one's entry. Empty when none is declared.
func (im *Imports) View() string {
	if im == nil {
		return ""
	}
	return im.view
}

// Override returns the workspace directory magus.yaml declares in place of importPath.
func (im *Imports) Override(importPath string) (string, bool) {
	if im == nil {
		return "", false
	}
	dir, ok := im.overrides[importPath]
	return dir, ok
}

// Dir returns the directory holding the spell a remote importPath names: its declared
// override, or its verified copy under View. A declared path whose pin could not be
// served returns that failure, MGS1043 or MGS1042; an undeclared path is MGS1041,
// naming the entry to add. The caller never falls back to the file search, where a
// crafted local directory could answer for it.
func (im *Imports) Dir(importPath string) (string, error) {
	if dir, ok := im.Override(importPath); ok {
		return dir, nil
	}
	if im != nil {
		if dir, ok := im.remote[importPath]; ok {
			return dir, nil
		}
		if err, ok := im.failed[importPath]; ok {
			return "", err
		}
	}
	return "", types.DiagnosticErrorf(types.RemoteSpellUndeclared,
		"import %q names a remote spell magus.yaml does not declare; add `spells: {%s: {tag: <tag>}}` to magus.yaml, then %s",
		importPath, importPath, UpdateHint)
}

// provider is how a workspace on the context hands over its imports: the root package's
// Magus implements it, and types stays free of this package.
type provider interface {
	SpellImports() *Imports
}

// ImportsFromContext returns the imports of the workspace on ctx, or nil when there is
// none or it declares nothing. A nil *Imports is usable and declares nothing.
func ImportsFromContext(ctx context.Context) *Imports {
	if p, ok := types.WorkspaceFromContext(ctx).(provider); ok {
		return p.SpellImports()
	}
	return nil
}

// verifiedViews holds the views this process has already checked against their
// layers, so a workspace loaded once per project checks each view once.
var verifiedViews sync.Map

// materialize lays each verified spell out under one directory by its import path, so
// the ordinary search templates find <view>/<path>/spell.buzz. The directory is named
// by the pins, and a different lock names a different view, so a view never changes
// once it exists; concurrent loads race only to create an identical one.
func materialize(cacheRoot string, refs []Ref, srcs map[string]string) (string, error) {
	h := sha256.New()
	for _, r := range refs {
		fmt.Fprintf(h, "%s@%s\n", r.Import, r.OCI.Digest)
	}
	views := filepath.Join(cacheRoot, "views")
	view := filepath.Join(views, hex.EncodeToString(h.Sum(nil))[:32])
	if _, ok := verifiedViews.Load(view); ok {
		return view, nil
	}
	if verifyView(view, refs, srcs) == nil {
		verifiedViews.Store(view, struct{}{})
		return view, nil
	}
	if err := os.MkdirAll(views, 0o755); err != nil {
		return "", fmt.Errorf("remote spells: %w", err)
	}
	tmp, err := os.MkdirTemp(views, ".view-")
	if err != nil {
		return "", fmt.Errorf("remote spells: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, r := range refs {
		if err := Unpack(srcs[r.Import], filepath.Join(tmp, filepath.FromSlash(r.Import))); err != nil {
			return "", fmt.Errorf("remote spell %s: %w", r.Import, err)
		}
	}
	if err := os.Rename(tmp, view); err != nil {
		// Occupied: a concurrent load won the rename, or an earlier view was edited.
		// Only the second is moved aside, so a path another load returned stays valid.
		if verifyView(view, refs, srcs) != nil {
			aside := tmp + ".stale"
			if err := os.Rename(view, aside); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return "", fmt.Errorf("remote spells: move unverified view aside: %w", err)
			}
			defer func() { _ = os.RemoveAll(aside) }()
			if err := os.Rename(tmp, view); err != nil && verifyView(view, refs, srcs) != nil {
				return "", fmt.Errorf("remote spells: %w", err)
			}
		}
	}
	verifiedViews.Store(view, struct{}{})
	return view, nil
}

// verifyView checks that each spell under view holds exactly its verified layer's files.
func verifyView(view string, refs []Ref, srcs map[string]string) error {
	for _, r := range refs {
		layer, err := os.ReadFile(filepath.Join(filepath.Dir(srcs[r.Import]), layerTitle))
		if err != nil {
			return err
		}
		want, err := tarFiles(layer)
		if err != nil {
			return err
		}
		have, err := dirFiles(filepath.Join(view, filepath.FromSlash(r.Import)))
		if err != nil {
			return err
		}
		if !slices.Equal(want, have) {
			return fmt.Errorf("%s does not match its layer", r.Import)
		}
	}
	return nil
}

// Err reports every declared remote spell whose pin could not be served, the check
// `magus spell lock` runs without --update. nil when every pin is served.
func (im *Imports) Err() error {
	if im == nil {
		return nil
	}
	errs := make([]error, 0, len(im.failed))
	for _, path := range slices.Sorted(maps.Keys(im.failed)) {
		errs = append(errs, im.failed[path])
	}
	return errors.Join(errs...)
}

// Relock asks the registry what each declared remote spell's tag names now, pulls and
// verifies every manifest, and returns the lock recording them. It is the only call
// that resolves a tag; LoadImports reads what it wrote. A workspace declaring no
// remote spell gets an empty lock.
func Relock(ctx context.Context, cfg config.SpellsConfig, opts LoadOptions) (Lock, error) {
	lock := Lock{Version: lockVersion, Spells: map[string]LockEntry{}}
	var errs []error
	for _, path := range slices.Sorted(maps.Keys(cfg.Imports)) {
		decl := cfg.Imports[path]
		if decl.Tag == "" {
			continue
		}
		e, err := relockOne(ctx, path, decl.Tag, opts)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		lock.Spells[path] = e
	}
	if len(errs) > 0 {
		return Lock{}, errors.Join(errs...)
	}
	return lock, nil
}

func relockOne(ctx context.Context, path, tag string, opts LoadOptions) (LockEntry, error) {
	repo, err := oci.ParseRepository(path)
	if err != nil {
		return LockEntry{}, fmt.Errorf("remote spell %s: %w", path, err)
	}
	repo.Tag = tag
	c, err := opts.client(ctx, repo.Registry)
	if err != nil {
		return LockEntry{}, fmt.Errorf("remote spell %s: %w", path, err)
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	pinned, err := Pin(ctx, c, repo)
	if err != nil {
		return LockEntry{}, fmt.Errorf("remote spell %s:%s: %w", path, tag, err)
	}
	ref, err := Pinned(path, pinned.Digest)
	if err != nil {
		return LockEntry{}, err
	}
	if _, err := Resolve(ctx, ref, Options{CacheRoot: opts.CacheRoot, Client: c.HTTP, Username: c.Username, Password: c.Password}); err != nil {
		return LockEntry{}, err
	}
	return LockEntry{Tag: tag, Digest: pinned.Digest}, nil
}

// Unlocked returns the paths lock pins that cfg no longer declares as remote spells. A
// load ignores them; `magus spell lock` refuses them, so the committed record never
// names a spell nothing imports.
func Unlocked(cfg config.SpellsConfig, lock Lock) []string {
	var out []string
	for _, path := range slices.Sorted(maps.Keys(lock.Spells)) {
		if cfg.Imports[path].Tag == "" {
			out = append(out, path)
		}
	}
	return out
}
