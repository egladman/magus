package magus

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// ApprovedPolicy is the authority an unapproved loosening of a workspace guard rule waits
// on. The working tree applies wherever it is stricter; where it is looser, the approved
// sources decide, so an agent cannot relax the rules that grade it by editing them.
//
// The checked-out commit is the first implementation. A commit is not proof a person
// approved anything, which is why this is an interface: an approved pin replaces HEAD here
// without the evaluator, the stricter-of merge or the trail provenance changing.
type ApprovedPolicy interface {
	// Pending returns the absolute paths of the workspace sources that may differ from
	// their approved content: every .buzz source, the config that shapes a load, and each
	// untracked directory, which ends in a separator and stands for everything under it.
	// A path it does not cover holds its approved content in the working tree, which is
	// what lets a caller read it from disk. Empty lets the caller skip evaluating the
	// approved sources at all.
	//
	// A non-empty scope limits the answer to those absolute paths and what lies under
	// them; outside the scope nothing is reported, pending or not. Nil asks about the
	// whole workspace.
	Pending(ctx context.Context, scope []string) ([]string, error)
	// ReadFile returns the approved content of an absolute path inside the workspace, and
	// an error when the approved state has no such file.
	ReadFile(ctx context.Context, path string) ([]byte, error)
}

// headPolicy approves what the checked-out commit holds, read one file at a time through
// the VCS layer rather than by materializing the revision.
//
// The repository root costs a VCS process, and a hook asks on every shell command, almost
// always about a clean tree that never needs it, so it is resolved on first use.
type headPolicy struct {
	workspace string
	driver    types.VCSDriver
	repoRoot  func(context.Context) (string, error)
	// batch is the shared object process for this authority, when the driver
	// can open one. One hook reads every pending policy source; a process per
	// source was most of that wait.
	batch *objectBatchState
}

// objectBatchState is the one batch headPolicy opens, shared across the copies
// an interface value makes of the struct.
type objectBatchState struct {
	once  sync.Once
	batch vcs.ObjectBatch
	err   error
}

// loadShapingFiles are the files besides Buzz sources whose edit can change what a load
// registers, or whether it loads at all.
var loadShapingFiles = []string{workspaceMarker, remotespell.LockFile}

func (h headPolicy) Pending(ctx context.Context, scope []string) ([]string, error) {
	var pathspecs []string
	for _, p := range scope {
		rel, err := filepath.Rel(h.workspace, p)
		if err != nil || !filepath.IsLocal(rel) {
			// Outside the workspace nothing is versioned here, so nothing there is pending.
			continue
		}
		pathspecs = append(pathspecs, filepath.ToSlash(rel))
	}
	if len(scope) > 0 && len(pathspecs) == 0 {
		return nil, nil
	}
	// The scope's bytes and the checkout id already say whether this answer
	// can have changed. Status is a process, and a hook pays it on every command.
	var (
		preID, files  string
		fingerprinted bool
	)
	if len(scope) > 0 {
		if id, hashed, ok := h.pendingFingerprint(scope); ok {
			preID, files, fingerprinted = id, hashed, true
			if pending, hit := readPending(h.workspace, scope, pendingKey(id, hashed)); hit {
				return pending, nil
			}
		}
	}
	dirty, err := h.driver.DirtyFiles(ctx, h.workspace, pathspecs)
	if err != nil {
		if _, rootErr := h.repoRoot(ctx); rootErr != nil {
			//nolint:nilerr // a workspace outside any repository has no approved state to defer to
			return nil, nil
		}
		return nil, err
	}
	if len(dirty) == 0 {
		if fingerprinted {
			h.keepPending(scope, preID, files, nil)
		}
		return nil, nil
	}
	repoRoot, err := h.repoRoot(ctx)
	if err != nil {
		//nolint:nilerr // a workspace outside any repository has no approved state to defer to
		return nil, nil
	}
	var out []string
	for _, p := range dirty {
		switch {
		case strings.HasSuffix(p, "/"):
			// A status entry ending in a separator is an untracked directory: no file under
			// it is listed on its own, and none of them is committed.
			out = append(out, filepath.Join(repoRoot, filepath.FromSlash(p))+string(filepath.Separator))
		case strings.HasSuffix(p, ".buzz") || slices.Contains(loadShapingFiles, path.Base(p)):
			out = append(out, filepath.Join(repoRoot, filepath.FromSlash(p)))
		}
	}
	if fingerprinted {
		h.keepPending(scope, preID, files, out)
	}
	return out, nil
}

// keepPending records pending for the state status saw, which preID and files
// named before it ran. A file rewritten while status ran, or a checkout moved
// before the id is read again, would file one state's answer under another's
// key, so either drops the record. jj is the exception for the id: its first
// status of a dirty tree appends an operation without changing a byte, and
// the answer belongs to that new id.
func (h headPolicy) keepPending(scope []string, preID, files string, pending []string) {
	id, ok := checkoutID(h.driver, h.workspace)
	if !ok || strings.ContainsAny(id, " \n") {
		return
	}
	if id != preID && h.driver.Name() != "jj" {
		return
	}
	if again, ok := hashPending(h.workspace, scope); !ok || again != files {
		return
	}
	writePending(h.workspace, scope, pendingKey(id, files), pending)
}

func pendingKey(id, files string) string { return id + " " + files }

// checkoutID reads the id from the checkout dir sits in, which may be an
// ancestor: a workspace nested in a repository has no marker of its own.
func checkoutID(driver types.VCSDriver, dir string) (string, bool) {
	r, ok := driver.(vcs.CheckoutIDReader)
	if !ok {
		return "", false
	}
	for d := filepath.Clean(dir); ; {
		for _, marker := range driver.Claims() {
			if _, err := os.Lstat(filepath.Join(d, marker)); err == nil {
				return r.CheckoutID(d)
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// readPending returns the pending list recorded under fp. ok is false when the
// record is missing or was written for a different fingerprint, and the caller
// asks the tool.
func readPending(workspace string, scope []string, fp string) ([]string, bool) {
	path, ok := pendingCacheFile(workspace, scope)
	if !ok {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	line, rest, found := strings.Cut(string(b), "\n")
	if !found || line != fp {
		return nil, false
	}
	rest = strings.TrimSuffix(rest, "\n")
	if rest == "" {
		return []string{}, true
	}
	return strings.Split(rest, "\n"), true
}

// writePending records pending as the answer for fp. A write failure leaves the
// next call to ask git, which is the answer it would have given anyway. The
// record is renamed into place: another hook reading a half-written list would
// take every path it lost for approved.
func writePending(workspace string, scope []string, fp string, pending []string) {
	for _, p := range pending {
		if strings.Contains(p, "\n") {
			return
		}
	}
	path, ok := pendingCacheFile(workspace, scope)
	if !ok {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	body := fp + "\n" + strings.Join(pending, "\n")
	if len(pending) > 0 {
		body += "\n"
	}
	f, err := os.CreateTemp(dir, ".partial-")
	if err != nil {
		return
	}
	tmp := f.Name()
	_, werr := f.WriteString(body)
	cerr := f.Close()
	if werr != nil || cerr != nil || os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}

// pendingFingerprint identifies the worktree state Pending would ask the tool
// about. id is the driver's checkout id. files hashes the bytes of every
// scoped file, and the names and buzz bytes under a scoped directory, so an
// edit is a different fingerprint even when the size and mtime are not. The
// id stays outside that hash so a jj status that moves it can be recorded
// under the new id. ok is false when the id cannot be read without the tool.
func (h headPolicy) pendingFingerprint(scope []string) (id, files string, ok bool) {
	id, ok = checkoutID(h.driver, h.workspace)
	if !ok || strings.ContainsAny(id, " \n") {
		return "", "", false
	}
	files, ok = hashPending(h.workspace, scope)
	return id, files, ok
}

// Kinds of the records hashPending frames. Each record is its kind, a length
// and its bytes, so no file's content can read as a name or as a missing file.
const (
	framePath    = 'p'
	frameMissing = 'm'
	frameFile    = 'f'
	frameDir     = 'd'
	frameEntry   = 'e'
)

func frame(h hash.Hash, kind byte, b []byte) {
	var hdr [9]byte
	hdr[0] = kind
	binary.LittleEndian.PutUint64(hdr[1:], uint64(len(b)))
	_, _ = h.Write(hdr[:])
	_, _ = h.Write(b)
}

func hashPending(workspace string, scope []string) (string, bool) {
	paths := append([]string(nil), scope...)
	slices.Sort(paths)
	h := sha256.New()
	parents := map[string]struct{}{}
	for _, p := range paths {
		frame(h, framePath, []byte(p))
		info, err := os.Lstat(p)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				frame(h, frameMissing, nil)
				if parent := filepath.Dir(p); parent != workspace {
					parents[parent] = struct{}{}
				}
				continue
			}
			return "", false
		}
		if info.IsDir() {
			if !writeDirBuzz(h, p) {
				return "", false
			}
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return "", false
		}
		frame(h, frameFile, data)
		if parent := filepath.Dir(p); parent != workspace {
			parents[parent] = struct{}{}
		}
	}
	dirs := make([]string, 0, len(parents))
	for dir := range parents {
		dirs = append(dirs, dir)
	}
	slices.Sort(dirs)
	for _, dir := range dirs {
		frame(h, framePath, []byte(dir))
		if !writeDirBuzz(h, dir) {
			return "", false
		}
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

// writeDirBuzz hashes a directory's entry names, and the bytes of each buzz
// file directly in it. A new file changes the names; an edit to one changes
// its bytes. Nested directories are names only: the scope lists the files it
// cares about under them.
func writeDirBuzz(h hash.Hash, dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	slices.Sort(names)
	frame(h, frameDir, nil)
	for _, name := range names {
		frame(h, frameEntry, []byte(name))
		if !strings.HasSuffix(name, ".buzz") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false
		}
		frame(h, frameFile, data)
	}
	return true
}

// pendingCacheFile is where the pending answer for one scope of workspace is
// recorded. Each scope has its own file, so the scopes a hook asks about in
// turn do not evict each other. ok is false when the cache directory cannot be
// resolved.
func pendingCacheFile(workspace string, scope []string) (string, bool) {
	dir, err := ResolveCacheDir(workspace)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(workspace + "\x00" + strings.Join(scope, "\x00")))
	return filepath.Join(dir, "policy-pending", hex.EncodeToString(sum[:])), true
}

// pendingSet answers whether a path is one Pending covers. Paths are compared with their
// directory's symlinks resolved, because a load names its sources from the workspace root
// and Pending names them from the resolved repository root.
type pendingSet struct {
	files map[string]bool
	dirs  []string
}

func newPendingSet(pending []string) pendingSet {
	set := pendingSet{files: map[string]bool{}}
	for _, p := range pending {
		if strings.HasSuffix(p, string(filepath.Separator)) {
			set.dirs = append(set.dirs, canonicalPath(strings.TrimSuffix(p, string(filepath.Separator)))+string(filepath.Separator))
			continue
		}
		set.files[canonicalPath(p)] = true
	}
	return set
}

func (s pendingSet) covers(p string) bool {
	p = canonicalPath(p)
	if s.files[p] {
		return true
	}
	return slices.ContainsFunc(s.dirs, func(dir string) bool { return strings.HasPrefix(p, dir) })
}

// canonicalPath resolves the symlinks in the deepest existing directory above p, leaving
// the rest alone so a deleted file, or one in a directory not created yet, still has a
// name comparable with the same path reached another way.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	rest := filepath.Base(p)
	for dir := filepath.Dir(p); ; dir = filepath.Dir(dir) {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		if parent := filepath.Dir(dir); parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
	}
}

func (h headPolicy) ReadFile(ctx context.Context, path string) ([]byte, error) {
	// The directory is resolved, not the file, so a file the working tree deleted still
	// maps to its place in the repository. The driver reports the root resolved, and a temp
	// or home directory behind a symlink would otherwise read as outside it.
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		dir = filepath.Dir(path)
	}
	repoRoot, err := h.repoRoot(ctx)
	if err != nil {
		return os.ReadFile(path)
	}
	rel, err := filepath.Rel(repoRoot, filepath.Join(dir, filepath.Base(path)))
	if err != nil || !filepath.IsLocal(rel) {
		// Outside the repository nothing is versioned, so both sides read the same bytes.
		return os.ReadFile(path)
	}
	relSlash := filepath.ToSlash(rel)
	if content, ok, err := h.readBatch(ctx, repoRoot, relSlash); ok {
		if err != nil {
			return nil, err
		}
		return []byte(content), nil
	}
	content, err := h.driver.ReadFileAt(ctx, repoRoot, "", relSlash)
	if err != nil {
		return nil, err
	}
	return []byte(content), nil
}

// readBatch serves rel from the authority's object batch. ok is false when this
// driver has no batch or the batch failed to start, and the caller reads one
// path the way it always has. A missing object is ok with an error: that is the
// answer, and starting a second process would only repeat it.
func (h headPolicy) readBatch(ctx context.Context, repoRoot, rel string) (string, bool, error) {
	opener, ok := h.driver.(vcs.ObjectBatchOpener)
	if !ok || h.batch == nil {
		return "", false, nil
	}
	h.batch.once.Do(func() {
		h.batch.batch, h.batch.err = opener.OpenObjectBatch(ctx, repoRoot, "")
	})
	if h.batch.err != nil || h.batch.batch == nil {
		return "", false, nil
	}
	content, err := h.batch.batch.Read(rel)
	if err != nil && !errors.Is(err, vcs.ErrObjectMissing) {
		_ = h.batch.batch.Close()
		h.batch.batch = nil
		return "", false, nil
	}
	return content, true, err
}

// Close finishes the object batch, if this authority opened one.
func (h headPolicy) Close() error {
	if h.batch == nil || h.batch.batch == nil {
		return nil
	}
	err := h.batch.batch.Close()
	h.batch.batch = nil
	return err
}

func closeApproved(a ApprovedPolicy) {
	if c, ok := a.(io.Closer); ok {
		_ = c.Close()
	}
}

// approvalAuthority is the loosening authority of the workspace at root, nil when it has
// none: version control disabled or no repository. Without one the working tree is the whole
// policy.
func approvalAuthority(ctx context.Context, root string, opts types.VCSOptions) (ApprovedPolicy, error) {
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil {
		return nil, err
	}
	if res.VCS == nil {
		return nil, nil //nolint:nilnil // no authority is a documented answer, not a failure
	}
	driver := res.VCS
	var (
		once     sync.Once
		repoRoot string
		rootErr  error
	)
	resolveRoot := func(ctx context.Context) (string, error) {
		once.Do(func() {
			repoRoot, rootErr = driver.Root(ctx, root)
			if rootErr != nil {
				return
			}
			if resolved, err := filepath.EvalSymlinks(repoRoot); err == nil {
				repoRoot = resolved
			}
		})
		return repoRoot, rootErr
	}
	return headPolicy{workspace: root, driver: driver, repoRoot: resolveRoot, batch: &objectBatchState{}}, nil
}

// SpawnRule returns the magus\guard.spawn rule the root magusfile registered, or nil.
func (m *Magus) SpawnRule() workspace.SpawnRule {
	if m.wsReg == nil {
		return nil
	}
	return m.wsReg.SpawnRule()
}

// CommandRule returns the magus\guard.command rule the root magusfile registered, or nil.
func (m *Magus) CommandRule() workspace.CommandRule {
	if m.wsReg == nil {
		return nil
	}
	return m.wsReg.CommandRule()
}

// ApprovedSpawnRule returns the magus\guard.spawn rule as the approved sources register it.
// Nil with no error when nothing is pending (the working tree's rule already is the
// approved one), when the workspace has no approval authority, or when the approved sources
// register no rule. An error means the approved rule could not be resolved, which is not
// the fact that there is none. The guard runs it beside SpawnRule and keeps the stricter
// answer.
//
// It loads the root magusfile a second time, reading every source it and its Buzz imports
// read through the approval authority, so call it only for a spawn.
func (m *Magus) ApprovedSpawnRule(ctx context.Context) (workspace.SpawnRule, error) {
	reg, err := m.approvedRegistry(ctx)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.SpawnRule(), nil
}

// ApprovedCommandRule is ApprovedSpawnRule for the magus\guard.command rule. Call it only
// for a shell command, and only when a stricter answer could still change the verdict.
func (m *Magus) ApprovedCommandRule(ctx context.Context) (workspace.CommandRule, error) {
	reg, err := m.approvedRegistry(ctx)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.CommandRule(), nil
}

func (m *Magus) approvedRegistry(ctx context.Context) (*workspace.WorkspaceRegistry, error) {
	var sources []interp.SourceFile
	if m.policyLog != nil {
		sources = m.policyLog.Files()
	}
	return approvedRegistryIfChanged(ctx, m.ws.Root, m.rootProjectPath(), m.ws.VCSOptions, m.resolver, sources, m.policyLog != nil)
}

// approvedRegistryIfChanged is the approved sources' registry for a working tree whose root
// load read sources, nil when it cannot differ from what that load registered. loaded is
// false when the working tree's load is not known, which leaves nothing to compare with.
func approvedRegistryIfChanged(ctx context.Context, root, projectPath string, opts types.VCSOptions, resolver *secret.Resolver, sources []interp.SourceFile, loaded bool) (*workspace.WorkspaceRegistry, error) {
	approved, err := approvalAuthority(ctx, root, opts)
	if err != nil || approved == nil {
		return nil, err
	}
	var scope []string
	if loaded {
		scope = loadScope(root, sources)
	}
	pending, err := approved.Pending(ctx, scope)
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	if loaded && !pendingTouchesLoad(root, newPendingSet(pending), sources) {
		// Every file the root load read holds its approved content, so the approved load
		// would read the same bytes and register the working tree's rules.
		return nil, nil //nolint:nilnil // no approved difference is the documented nil answer
	}
	if scope != nil && !batched(approved) {
		// One status over the whole tree lets approvedReader read clean files from
		// disk instead of starting a process for each.
		if pending, err = approved.Pending(ctx, nil); err != nil {
			return nil, err
		}
	}
	return approvedRegistry(ctx, root, projectPath, resolver, approved, pending)
}

// loadScope is every path whose pending state can change what the root load registers,
// which is what pendingTouchesLoad reads: the files the load read, the root magusfile's
// candidates and the config that shapes a load.
func loadScope(root string, sources []interp.SourceFile) []string {
	scope := []string{filepath.Join(root, "magusfiles")}
	for _, s := range sources {
		scope = append(scope, s.Path)
	}
	if candidates, err := interp.MagusfileCandidates(root); err == nil {
		scope = append(scope, candidates...)
	}
	for _, name := range loadShapingFiles {
		scope = append(scope, filepath.Join(root, name))
	}
	slices.Sort(scope)
	return slices.Compact(scope)
}

// pendingTouchesLoad reports whether a pending path can change what the root magusfile's
// load registers: a file that load read, a config that shapes it, or a new root magusfile
// source the load did not read because it did not exist in the working tree.
//
// Imports are reached only through files the load read, so a pending file that none of
// them reaches cannot change the approved load either.
func pendingTouchesLoad(root string, pending pendingSet, sources []interp.SourceFile) bool {
	for _, s := range sources {
		if pending.covers(s.Path) {
			return true
		}
	}
	candidates, err := interp.MagusfileCandidates(root)
	if err != nil {
		return true
	}
	for _, name := range loadShapingFiles {
		candidates = append(candidates, filepath.Join(root, name))
	}
	if slices.ContainsFunc(candidates, pending.covers) {
		return true
	}
	// A root source the working tree does not have yet, added or restored.
	magusfiles := filepath.Dir(canonicalPath(filepath.Join(root, "magusfiles", "a.buzz")))
	for file := range pending.files {
		if filepath.Dir(file) == magusfiles && filepath.Ext(file) == ".buzz" {
			return true
		}
	}
	return slices.ContainsFunc(pending.dirs, func(dir string) bool {
		return strings.HasPrefix(magusfiles+string(filepath.Separator), dir)
	})
}

// LoadApprovedSpawnRule is ApprovedSpawnRule for the workspace at root when its working
// tree does not load. Nothing in the working tree is trusted: its magus.yaml is not read,
// since a broken or edited config (a version floor this binary is below, say) may be the
// failure, and the approved sources are evaluated whether or not any of them looks pending.
func LoadApprovedSpawnRule(ctx context.Context, root string) (workspace.SpawnRule, error) {
	reg, err := loadApprovedRegistry(ctx, root)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.SpawnRule(), nil
}

// LoadApprovedCommandRule is LoadApprovedSpawnRule for the magus\guard.command rule.
func LoadApprovedCommandRule(ctx context.Context, root string) (workspace.CommandRule, error) {
	reg, err := loadApprovedRegistry(ctx, root)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.CommandRule(), nil
}

// LoadApprovedWriteRule is LoadApprovedSpawnRule for the magus\guard.write rule.
func LoadApprovedWriteRule(ctx context.Context, root string) (workspace.WriteRule, error) {
	reg, err := loadApprovedRegistry(ctx, root)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.WriteRule(), nil
}

func loadApprovedRegistry(ctx context.Context, root string) (*workspace.WorkspaceRegistry, error) {
	approved, err := approvalAuthority(ctx, root, types.VCSOptions{})
	if err != nil || approved == nil {
		return nil, err
	}
	pending, err := approved.Pending(ctx, nil)
	if err != nil {
		return nil, err
	}
	return approvedRegistry(ctx, root, ".", secret.New(), approved, pending)
}

// approvedReader reads the approved bytes of each path once. A batched authority
// serves every path, since pending may be scoped to what the working-tree load read
// and a path outside that scope can still be pending. Without a batch each read is
// a process, so a path pending does not cover is read from disk, which is only sound
// because the caller passed the whole tree's pending answer.
func approvedReader(ctx context.Context, approved ApprovedPolicy, pending []string) func(string) ([]byte, error) {
	memo := map[string][]byte{}
	disk := !batched(approved)
	changed := newPendingSet(pending)
	return func(path string) ([]byte, error) {
		if data, ok := memo[path]; ok {
			return data, nil
		}
		if disk && !changed.covers(path) {
			if data, err := os.ReadFile(path); err == nil {
				memo[path] = data
				return data, nil
			}
		}
		data, err := approved.ReadFile(ctx, path)
		if err != nil {
			return nil, err
		}
		memo[path] = data
		return data, nil
	}
}

// batched reports whether approved serves its reads from one process.
func batched(approved ApprovedPolicy) bool {
	h, ok := approved.(headPolicy)
	if !ok {
		return false
	}
	_, ok = h.driver.(vcs.ObjectBatchOpener)
	return ok
}

// approvedRegistry evaluates the root magusfile at root as approved holds it and returns
// what it registered. pending names the sources the working tree changed, so a file it
// deleted is still found. It must be the whole tree's answer when approved is not
// batched. Nil with no error when there is no magusfile to evaluate.
func approvedRegistry(ctx context.Context, root, projectPath string, resolver *secret.Resolver, approved ApprovedPolicy, pending []string) (*workspace.WorkspaceRegistry, error) {
	defer closeApproved(approved)
	if !interp.Available() {
		return nil, nil //nolint:nilnil // without an interpreter no magusfile can register a rule
	}
	read := approvedReader(ctx, approved, pending)
	files, err := approvedMagusfiles(root, pending, read, func() ([]string, error) {
		// The single magusfile is absent from the approved state, so the load is
		// the directory form. A file that form still imports can be pending
		// without the working-tree load having read it.
		return approved.Pending(ctx, nil)
	})
	if err != nil || len(files) == 0 {
		return nil, err
	}
	reg := workspace.NewWorkspaceRegistry()
	lctx := interp.WithGuardRules(installWorkspaceRegistry(ctx, reg))
	lctx = secret.ContextWithResolver(lctx, resolver)
	lctx = interp.WithProjectPath(lctx, projectPath)
	lctx = interp.WithSourceReader(lctx, read)
	for _, src := range interp.Sources(root, files) {
		if _, err := interp.Parse(lctx, src); err != nil {
			return nil, fmt.Errorf("approved magusfile: %w", err)
		}
	}
	return reg, nil
}

// approvedMagusfiles is the root magusfile's files as approved holds them, in whichever
// form it holds them. The working tree only proposes candidates, so neither deleting a file
// nor adding the other form can hide one; a candidate approved lacks did not exist there.
func approvedMagusfiles(root string, pending []string, read func(string) ([]byte, error), expand func() ([]string, error)) ([]string, error) {
	candidates, err := interp.MagusfileCandidates(root)
	if err != nil {
		return nil, err
	}
	single := candidates[0]
	if _, err := read(single); err == nil {
		return []string{single}, nil
	}
	if expand != nil {
		pending, err = expand()
		if err != nil {
			return nil, err
		}
	}
	// Pending paths hang off the resolved repository root, which root may reach through a
	// symlink; each is renamed onto root so a file named both ways is read once.
	dir := filepath.Join(root, "magusfiles")
	pendingDir := dir
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		pendingDir = filepath.Join(resolved, "magusfiles")
	}
	for _, p := range pending {
		if filepath.Dir(p) == pendingDir && filepath.Ext(p) == ".buzz" {
			candidates = append(candidates, filepath.Join(dir, filepath.Base(p)))
		}
	}
	slices.Sort(candidates)
	var files []string
	for _, f := range slices.Compact(candidates) {
		if f == single {
			continue
		}
		if _, err := read(f); err == nil {
			files = append(files, f)
		}
	}
	return files, nil
}

// rootProjectPath is the path the root project is registered under, "." when none is.
func (m *Magus) rootProjectPath() string {
	for _, p := range m.All() {
		if filepath.Clean(p.Dir) == filepath.Clean(m.ws.Root) {
			return p.Path
		}
	}
	return "."
}

// GuardPolicy is the effective set of workspace guard rules as this load sees it. Nothing
// in it runs a process, so a hook reads it on every call.
type GuardPolicy struct {
	// Digest names the rule set; "" when the working tree declares no guard rule.
	Digest string
	// Sources are the files the root magusfile's load read, which is where a spawn rule can
	// come from, each named by the ContentID of the bytes read.
	Sources     []interp.SourceFile
	ShellRules  int
	SpawnRule   bool
	CommandRule bool
	WriteRule   bool
}

// GuardPolicy describes the workspace guard rules this load registered.
//
// A spawn, command or write rule is code, so its digest covers every file its load read:
// an edit to any of them counts as a policy change while one is registered. Shell rules
// are data and count only for themselves.
func (m *Magus) GuardPolicy() GuardPolicy {
	var sources []interp.SourceFile
	if m.policyLog != nil {
		sources = m.policyLog.Files()
	}
	var write bool
	if m.wsReg != nil {
		write = m.wsReg.WriteRule() != nil
	}
	return guardPolicyOf(m.ShellRules(), m.SpawnRule() != nil, m.CommandRule() != nil, write, sources)
}

func guardPolicyOf(rules []workspace.ShellRule, spawn, command, write bool, sources []interp.SourceFile) GuardPolicy {
	policy := GuardPolicy{Sources: sources, ShellRules: len(rules), SpawnRule: spawn, CommandRule: command, WriteRule: write}
	if len(rules) == 0 && !spawn && !command && !write {
		return policy
	}
	h := sha256.New()
	for _, r := range rules {
		fmt.Fprintf(h, "shell %q %q %q %q %q %q\n", r.Name, r.Decision, r.Program, r.Args, r.Reason, r.Dialect)
	}
	// Registering another rule over the same files is still a new digest. The spawn rule's
	// lines keep their spelling so its digests stay what they were.
	if command {
		fmt.Fprintln(h, "rule command")
	}
	if write {
		fmt.Fprintln(h, "rule write")
	}
	if spawn || command || write {
		for _, s := range sources {
			fmt.Fprintf(h, "spawn %s %s\n", s.Path, s.ContentID)
		}
	}
	policy.Digest = hex.EncodeToString(h.Sum(nil))
	return policy
}

// ApprovedContentIDs maps each path to the interp.ContentID of its approved content, ""
// when the approved state has no such file. Nil when the workspace has no approval
// authority. A path Pending does not cover is hashed from disk; the rest are read through
// the authority, which can cost a process per file, so call it only when the answer can
// change what is recorded.
func (m *Magus) ApprovedContentIDs(ctx context.Context, paths []string) map[string]string {
	return approvedContentIDs(ctx, m.ws.Root, m.ws.VCSOptions, paths)
}

func approvedContentIDs(ctx context.Context, root string, opts types.VCSOptions, paths []string) map[string]string {
	approved, err := approvalAuthority(ctx, root, opts)
	if err != nil || approved == nil {
		return nil
	}
	defer closeApproved(approved)
	pending, err := approved.Pending(ctx, paths)
	if err != nil {
		return nil
	}
	changed := newPendingSet(pending)
	out := make(map[string]string, len(paths))
	for _, p := range paths {
		if !changed.covers(p) {
			if data, err := os.ReadFile(p); err == nil {
				out[p] = interp.ContentID(data)
				continue
			}
		}
		data, err := approved.ReadFile(ctx, p)
		if err != nil {
			out[p] = ""
			continue
		}
		out[p] = interp.ContentID(data)
	}
	return out
}

// GuardRules are the workspace guard rules the root magusfile registers, loaded from that
// magusfile alone. An agent host calls the guard before every tool call, and the rules need
// none of what the full workspace load computes, which costs an order of magnitude more.
type GuardRules struct {
	root     string
	opts     types.VCSOptions
	registry *workspace.WorkspaceRegistry
	sources  []interp.SourceFile
}

// LoadGuardRules evaluates the root magusfile of the workspace at root on its own, and
// returns the guard rules it registers. opts selects the version control the approved side
// is read through. A workspace with no magusfile has no rules and no error; a magusfile
// that does not load is an error, which the caller must not read as "no rules".
func LoadGuardRules(ctx context.Context, root string, opts types.VCSOptions) (*GuardRules, error) {
	rules := &GuardRules{root: root, opts: opts, registry: workspace.NewWorkspaceRegistry()}
	if !interp.Available() {
		return rules, nil
	}
	srcs, err := interp.FindAll(root)
	if errors.Is(err, interp.ErrNoMagusfile) {
		return rules, nil
	}
	if err != nil {
		return nil, err
	}
	log := &interp.SourceLog{}
	// Rules only. The rest of the magusfile (harness spells, release tooling,
	// target imports) is not a guard rule, and compiling it is most of a hook's
	// cost. See guardRulesFilter in internal/interp.
	lctx := interp.WithGuardRules(installWorkspaceRegistry(ctx, rules.registry))
	lctx = secret.ContextWithResolver(lctx, secret.New())
	lctx = interp.WithProjectPath(lctx, ".")
	lctx = interp.WithSourceLog(lctx, log)
	for _, src := range srcs {
		if _, err := interp.Parse(lctx, src); err != nil {
			return nil, fmt.Errorf("magusfile: %w", err)
		}
	}
	rules.sources = log.Files()
	return rules, nil
}

// ShellRules returns the magus\guard.shell rules in declaration order.
func (g *GuardRules) ShellRules() []workspace.ShellRule { return g.registry.ShellRules() }

// SpawnRule returns the magus\guard.spawn rule, or nil.
func (g *GuardRules) SpawnRule() workspace.SpawnRule { return g.registry.SpawnRule() }

// CommandRule returns the magus\guard.command rule, or nil.
func (g *GuardRules) CommandRule() workspace.CommandRule { return g.registry.CommandRule() }

// WriteRule returns the magus\guard.write rule, or nil.
func (g *GuardRules) WriteRule() workspace.WriteRule { return g.registry.WriteRule() }

// Policy describes the rules for the trail's lineage, as Magus.GuardPolicy does.
func (g *GuardRules) Policy() GuardPolicy {
	return guardPolicyOf(g.ShellRules(), g.SpawnRule() != nil, g.CommandRule() != nil, g.WriteRule() != nil, g.sources)
}

// ApprovedSpawnRule is Magus.ApprovedSpawnRule for these rules. It costs one VCS status
// when no file the root load read differs from its approved content, and a second load of
// the root magusfile when one does.
func (g *GuardRules) ApprovedSpawnRule(ctx context.Context) (workspace.SpawnRule, error) {
	reg, err := approvedRegistryIfChanged(ctx, g.root, ".", g.opts, secret.New(), g.sources, true)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.SpawnRule(), nil
}

// ApprovedCommandRule is ApprovedSpawnRule for the magus\guard.command rule.
func (g *GuardRules) ApprovedCommandRule(ctx context.Context) (workspace.CommandRule, error) {
	reg, err := approvedRegistryIfChanged(ctx, g.root, ".", g.opts, secret.New(), g.sources, true)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.CommandRule(), nil
}

// ApprovedWriteRule is ApprovedSpawnRule for the magus\guard.write rule.
func (g *GuardRules) ApprovedWriteRule(ctx context.Context) (workspace.WriteRule, error) {
	reg, err := approvedRegistryIfChanged(ctx, g.root, ".", g.opts, secret.New(), g.sources, true)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.WriteRule(), nil
}

// ApprovedContentIDs is Magus.ApprovedContentIDs for these rules' workspace.
func (g *GuardRules) ApprovedContentIDs(ctx context.Context, paths []string) map[string]string {
	return approvedContentIDs(ctx, g.root, g.opts, paths)
}
