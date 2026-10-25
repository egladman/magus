package guard

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/secret"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// headPolicy is the authority an unapproved loosening of a workspace guard rule waits on:
// the checked-out commit, read one file at a time through the VCS layer rather than by
// materializing the revision. The working tree applies wherever it is stricter; where it
// is looser, the approved sources decide, so an agent cannot relax the rules that grade it
// by editing them.
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
	// cacheDir resolves the cache directory Pending records its answers in. Nil records
	// nothing, and every scoped Pending asks the VCS.
	cacheDir func(root string) (string, error)
}

// objectBatchState is the one batch headPolicy opens, shared across the copies
// a value receiver makes of the struct.
type objectBatchState struct {
	once  sync.Once
	batch vcs.ObjectBatch
}

// loadShapingFiles are the files besides Buzz sources whose edit can change what a load
// registers, or whether it loads at all.
var loadShapingFiles = []string{config.Filename, remotespell.LockFile}

// Pending returns the absolute paths of the workspace sources that may differ from their
// approved content: every .buzz source, the config that shapes a load, and each untracked
// directory, which ends in a separator and stands for everything under it. A path it does
// not cover holds its approved content in the working tree, which is what lets a caller
// read it from disk. Empty lets the caller skip evaluating the approved sources at all.
//
// A non-empty scope limits the answer to those absolute paths and what lies under them;
// outside the scope nothing is reported, pending or not. Nil asks about the whole
// workspace.
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
			if pending, hit := h.readPending(scope, pendingKey(id, hashed)); hit {
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
	h.writePending(scope, pendingKey(id, files), pending)
}

func pendingKey(id, files string) string { return fmt.Sprintf("%s %s", id, files) }

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
func (h headPolicy) readPending(scope []string, fp string) ([]string, bool) {
	path, ok := h.pendingCacheFile(scope)
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
func (h headPolicy) writePending(scope []string, fp string, pending []string) {
	for _, p := range pending {
		if strings.Contains(p, "\n") {
			return
		}
	}
	path, ok := h.pendingCacheFile(scope)
	if !ok {
		return
	}
	body := fmt.Sprintf("%s\n%s", fp, strings.Join(pending, "\n"))
	if len(pending) > 0 {
		body += "\n"
	}
	_ = file.ReplaceFile(path, []byte(body), 0o600)
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

// pendingCacheFile is where the pending answer for one scope is recorded. Each scope has
// its own file, so the scopes a hook asks about in turn do not evict each other. ok is
// false when no cache directory resolves, and nothing is recorded.
func (h headPolicy) pendingCacheFile(scope []string) (string, bool) {
	if h.cacheDir == nil {
		return "", false
	}
	dir, err := h.cacheDir(h.workspace)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s", h.workspace, strings.Join(scope, "\x00"))))
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

// ReadFile returns the approved content of an absolute path inside the workspace, and an
// error when the approved state has no such file.
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
		h.batch.batch, _ = opener.OpenObjectBatch(ctx, repoRoot, "")
	})
	if h.batch.batch == nil {
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

// batched reports whether h serves its reads from one process.
func (h headPolicy) batched() bool {
	_, ok := h.driver.(vcs.ObjectBatchOpener)
	return ok
}

// approvalAuthority is the loosening authority of the workspace at root; ok is false when it
// has none: version control disabled or no repository. Without one the working tree is the
// whole policy. cacheDir is where Pending records its answers, nil for nowhere.
func approvalAuthority(ctx context.Context, root string, opts types.VCSOptions, cacheDir func(string) (string, error)) (headPolicy, bool, error) {
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil {
		return headPolicy{}, false, err
	}
	if res.VCS == nil {
		return headPolicy{}, false, nil
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
	return headPolicy{workspace: root, driver: driver, repoRoot: resolveRoot, batch: &objectBatchState{}, cacheDir: cacheDir}, true, nil
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

// LoadApprovedSpawnRule is WorkspaceRules.ApprovedSpawnRule for the workspace at root when
// its working tree does not load. Nothing in the working tree is trusted: its magus.yaml is
// not read, since a broken or edited config (a version floor this binary is below, say)
// may be the failure, and the approved sources are evaluated whether or not any of them
// looks pending.
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

// LoadApprovedBuiltins is LoadApprovedSpawnRule for the magus\guard.builtins settings: the
// approved sources' declaration over their magus\guard.profile level, raised by the
// person's agents.profile, resolved over the compiled defaults. With no approval authority
// it is the defaults at that level, since the working tree that would otherwise decide is
// the one that did not load. A magus.yaml that does not load is an error.
func LoadApprovedBuiltins(ctx context.Context, root string) (map[string]builtin.Setting, error) {
	reg, err := loadApprovedRegistry(ctx, root)
	if err != nil {
		return nil, err
	}
	return approvedBuiltins(root, reg)
}

// approvedBuiltins is LoadApprovedBuiltins over a registry already resolved, nil for no
// approval authority.
func approvedBuiltins(root string, reg *workspace.WorkspaceRegistry) (map[string]builtin.Setting, error) {
	agents, err := config.LoadAgents(root)
	if err != nil {
		return nil, err
	}
	var declared map[string]builtin.Setting
	var profile types.GuardProfile
	if reg != nil {
		declared, profile = reg.Builtins(), reg.Profile()
	}
	expanded, err := builtin.ResolveProfile(profile.Stricter(agents.Profile), declared)
	if err != nil {
		return nil, err
	}
	return builtin.Resolve(expanded)
}

// LoadApprovedProfile is LoadApprovedSpawnRule for the magus\guard.profile level: the
// approved sources' level, light when they declare none or there is no approval authority.
// It does not read agents.profile; EffectiveProfile does.
func LoadApprovedProfile(ctx context.Context, root string) (types.GuardProfile, error) {
	reg, err := loadApprovedRegistry(ctx, root)
	if err != nil {
		return "", err
	}
	if reg == nil {
		return types.GuardProfileLight, nil
	}
	return reg.Profile().Effective(), nil
}

// EffectiveProfile is the level a hook's compiled-rule settings start from: the stricter of
// the approved and the working-tree magus\guard.profile, raised by the person's
// agents.profile. rules is the working tree's load, nil when it did not load, in which case
// root's approved sources decide alone. A side that does not load counts as light, and a
// magus.yaml that does not load raises nothing.
//
// It loads what BuiltinSettings loads, so it suits a report of the level, not a per-rule
// lookup: the level's settings are already in BuiltinSettings.
func EffectiveProfile(ctx context.Context, rules *WorkspaceRules, root string) types.GuardProfile {
	var level types.GuardProfile
	if rules != nil {
		root = rules.root
		level = rules.registry.Profile().Effective()
		reg, err := rules.approvedIfChanged(ctx)
		switch {
		case err != nil:
			level = level.Stricter(types.GuardProfileLight)
		case reg != nil:
			level = level.Stricter(reg.Profile().Effective())
		}
	} else {
		approved, err := LoadApprovedProfile(ctx, root)
		if err != nil {
			approved = types.GuardProfileLight
		}
		level = approved
	}
	if agents, err := config.LoadAgents(root); err == nil {
		level = level.Stricter(agents.Profile)
	}
	return level
}

// BuiltinSettings is the compiled rules' effective settings for a hook. rules is the
// working tree's load, nil when it did not load, in which case root's approved sources
// decide alone, as they do for the function rules.
//
// A side that does not resolve leaves the defaults at the light level in its place rather
// than failing: a broken commit must not take down every hook any more than a broken
// working tree does.
func BuiltinSettings(ctx context.Context, rules *WorkspaceRules, root string) map[string]builtin.Setting {
	var effective map[string]builtin.Setting
	if rules != nil {
		effective, _ = rules.effectiveBuiltins(ctx)
	} else if approved, err := LoadApprovedBuiltins(ctx, root); err == nil {
		effective = approved
	}
	if effective != nil {
		return effective
	}
	return lightBuiltins()
}

// lightBuiltins is every compiled rule's setting at the light level. The guard reads nil as
// the bare defaults, which would drop what the light level sets, so nil is the answer only
// when the compiled-in light row does not resolve: a magus bug, which
// TestBuiltinSettingsFallsBackToTheLightLevel pins away, and which leaves the defaults
// rather than taking down every hook.
func lightBuiltins() map[string]builtin.Setting {
	light, err := builtin.ResolveProfile(types.GuardProfileLight, nil)
	if err != nil || len(light) == 0 {
		return nil
	}
	effective, err := builtin.Resolve(light)
	if err != nil {
		return nil
	}
	return effective
}

func loadApprovedRegistry(ctx context.Context, root string) (*workspace.WorkspaceRegistry, error) {
	approved, ok, err := approvalAuthority(ctx, root, types.VCSOptions{}, nil)
	if err != nil || !ok {
		return nil, err
	}
	pending, err := approved.Pending(ctx, nil)
	if err != nil {
		return nil, err
	}
	return approvedRegistry(ctx, root, approved, pending)
}

// approvedReader reads the approved bytes of each path once. A batched authority
// serves every path, since pending may be scoped to what the working-tree load read
// and a path outside that scope can still be pending. Without a batch each read is
// a process, so a path pending does not cover is read from disk, which is only sound
// because the caller passed the whole tree's pending answer.
func approvedReader(ctx context.Context, approved headPolicy, pending []string) func(string) ([]byte, error) {
	memo := map[string][]byte{}
	disk := !approved.batched()
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

// approvedRegistry evaluates the root magusfile at root as approved holds it and returns
// what it registered. pending names the sources the working tree changed, so a file it
// deleted is still found. It must be the whole tree's answer when approved is not
// batched. Nil with no error when there is no magusfile to evaluate, and a
// *types.EngineNotLinkedError when there is one and this program links no Buzz engine.
//
// The load may run up to approvedLoadGrace past ctx's deadline; see approvedLoadContext.
func approvedRegistry(ctx context.Context, root string, approved headPolicy, pending []string) (*workspace.WorkspaceRegistry, error) {
	defer func() { _ = approved.Close() }()
	ctx, cancel := approvedLoadContext(ctx)
	defer cancel()
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
	if !interp.Available() {
		return nil, &types.EngineNotLinkedError{Op: "load the approved guard rules"}
	}
	reg := workspace.NewWorkspaceRegistry()
	lctx := interp.WithGuardRules(workspace.ContextWithRegistry(ctx, reg))
	lctx = secret.ContextWithResolver(lctx, secret.New())
	lctx = interp.WithProjectPath(lctx, ".")
	lctx = interp.WithApprovedSourceReader(lctx, read)
	for _, src := range interp.Sources(root, files) {
		if _, err := interp.Parse(lctx, src); err != nil {
			return nil, fmt.Errorf("approved magusfile: %w", err)
		}
	}
	return reg, nil
}

// approvedLoadGrace is how far past its caller's deadline the approved load may run.
const approvedLoadGrace = 2 * time.Second

// approvedLoadContext bounds the approved load to approvedLoadGrace past ctx's deadline, and
// leaves a ctx with no deadline as it is. A compile does not stop at a deadline anyway, but
// the reads and the run after it do, and a chunk is stored only once its run finishes: a
// load cut off there would spend every hook's time on the same compile and keep none of it.
// The caller still sees its own deadline pass and reads the answer as late. Cancelling ctx
// for any other reason cancels the load.
func approvedLoadContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ctx, func() {}
	}
	lctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline.Add(approvedLoadGrace))
	stop := context.AfterFunc(ctx, func() {
		if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			cancel()
		}
	})
	return lctx, func() {
		stop()
		cancel()
	}
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

// WorkspacePolicy is the effective set of workspace guard rules as one load sees it.
// Nothing in it runs a process, so a hook reads it on every call.
type WorkspacePolicy struct {
	// Digest names the rule set; "" when the working tree declares no guard rule.
	Digest string
	// Sources are the files the root magusfile's load read, which is where a spawn rule can
	// come from, each named by the ContentID of the bytes read.
	Sources     []interp.SourceFile
	ShellRules  int
	SpawnRule   bool
	CommandRule bool
	WriteRule   bool
	// Builtins counts the compiled rules magus\guard.builtins set.
	Builtins int
}

// buildPolicy describes a rule set. A spawn, command or write rule is code, so its digest
// covers every file its load read: an edit to any of them counts as a policy change while
// one is registered. Shell rules are data and count only for themselves.
func buildPolicy(rules []workspace.ShellRule, spawn, command, write bool, builtins map[string]builtin.Setting, sources []interp.SourceFile) WorkspacePolicy {
	policy := WorkspacePolicy{Sources: sources, ShellRules: len(rules), SpawnRule: spawn, CommandRule: command, WriteRule: write, Builtins: len(builtins)}
	if len(rules) == 0 && !spawn && !command && !write && len(builtins) == 0 {
		return policy
	}
	h := sha256.New()
	for _, r := range rules {
		fmt.Fprintf(h, "shell %q %q %q %q %q %q\n", r.Name, r.Decision, r.Program, r.Args, r.Reason, r.Dialect)
	}
	// Settings are data, like shell rules, so they count only for themselves.
	for _, name := range slices.Sorted(maps.Keys(builtins)) {
		s := builtins[name]
		fmt.Fprintf(h, "builtin %q %q %d\n", name, s.Decision, s.Lines)
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

// WorkspaceRules are the workspace guard rules the root magusfile registers, loaded from
// that magusfile alone. An agent host calls the guard before every tool call, and the rules
// need none of what the full workspace load computes, which costs an order of magnitude
// more.
type WorkspaceRules struct {
	root     string
	opts     types.VCSOptions
	registry *workspace.WorkspaceRegistry
	sources  []interp.SourceFile
	// cacheDir is where the approval authority records what it found pending, nil for
	// nowhere. NewDependencies sets it from its Wiring.
	cacheDir func(root string) (string, error)
	approved approvedOnce
}

// approvedOnce keeps the first approved registry a judgment resolved, so the compiled rules'
// settings, the level and each function rule share one VCS status and at most one load of
// the approved sources. A WorkspaceRules lives for one judgment, which is what bounds how
// stale the kept answer can be.
type approvedOnce struct {
	mu   sync.Mutex
	done bool
	reg  *workspace.WorkspaceRegistry
	err  error
}

// get answers from the kept resolve, or runs resolve and keeps its answer. An answer read
// under an expired or cancelled ctx is returned and not kept: "no rule" from a status cut
// short may be a read that failed.
func (o *approvedOnce) get(ctx context.Context, resolve func(context.Context) (*workspace.WorkspaceRegistry, error)) (*workspace.WorkspaceRegistry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return o.reg, o.err
	}
	reg, err := resolve(ctx)
	if ctx.Err() == nil {
		o.done, o.reg, o.err = true, reg, err
	}
	return reg, err
}

// LoadWorkspaceRules evaluates the root magusfile of the workspace at root on its own, and
// returns the guard rules it registers. opts selects the version control the approved side
// is read through.
//
// A workspace with no magusfile has no rules and no error. A magusfile that does not load
// is an error, which the caller must not read as "no rules", and so is a magusfile in a
// program that links no Buzz engine: a *types.EngineNotLinkedError.
func LoadWorkspaceRules(ctx context.Context, root string, opts types.VCSOptions) (*WorkspaceRules, error) {
	rules := &WorkspaceRules{root: root, opts: opts, registry: workspace.NewWorkspaceRegistry()}
	srcs, err := interp.FindAll(root)
	if errors.Is(err, interp.ErrNoMagusfile) {
		return rules, nil
	}
	if err != nil {
		return nil, err
	}
	if !interp.Available() {
		return nil, &types.EngineNotLinkedError{Op: "load guard rules"}
	}
	log := &interp.SourceLog{}
	// Rules only. The rest of the magusfile (harness spells, release tooling,
	// target imports) is not a guard rule, and compiling it is most of a hook's
	// cost. See guardRulesFilter in internal/interp.
	lctx := interp.WithGuardRules(workspace.ContextWithRegistry(ctx, rules.registry))
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

// ShellRules returns the magus\guard.shell rules in declaration order, as the guard applies
// them, and the last non-empty dialect among them for the outer parse.
func (r *WorkspaceRules) ShellRules() ([]WorkspaceShellRule, Dialect) {
	src := r.registry.ShellRules()
	if len(src) == 0 {
		return nil, ""
	}
	out := make([]WorkspaceShellRule, len(src))
	var dialect Dialect
	for i, s := range src {
		out[i] = WorkspaceShellRule{
			Name:     s.Name,
			Decision: s.Decision,
			Program:  s.Program,
			Args:     s.Args,
			Reason:   s.Reason,
			Dialect:  s.Dialect,
		}
		if s.Dialect != "" {
			dialect = Dialect(s.Dialect)
		}
	}
	return out, dialect
}

// SpawnRule returns the magus\guard.spawn rule, or nil.
func (r *WorkspaceRules) SpawnRule() workspace.SpawnRule { return r.registry.SpawnRule() }

// CommandRule returns the magus\guard.command rule, or nil.
func (r *WorkspaceRules) CommandRule() workspace.CommandRule { return r.registry.CommandRule() }

// WriteRule returns the magus\guard.write rule, or nil.
func (r *WorkspaceRules) WriteRule() workspace.WriteRule { return r.registry.WriteRule() }

// Builtins returns the compiled rules magus\guard.builtins set, only those it named, or nil.
func (r *WorkspaceRules) Builtins() map[string]builtin.Setting { return r.registry.Builtins() }

// DeclaredBuiltinSettings is each rule the root magusfile sets through
// magus\guard.builtins, rendered as `magus describe rules` shows it: the decision, then
// the line limit when one is set.
func (r *WorkspaceRules) DeclaredBuiltinSettings() map[string]string {
	declared := r.Builtins()
	out := make(map[string]string, len(declared))
	for name, s := range declared {
		out[name] = s.Decision.String()
		if s.Lines != 0 {
			out[name] = fmt.Sprintf("%s, lines %d", s.Decision, s.Lines)
		}
	}
	return out
}

// Policy describes the rules for the trail's lineage.
func (r *WorkspaceRules) Policy() WorkspacePolicy {
	return buildPolicy(r.registry.ShellRules(), r.SpawnRule() != nil, r.CommandRule() != nil, r.WriteRule() != nil, r.Builtins(), r.sources)
}

// policyState is Policy as the trail's lineage records it. The approved ids are left to a
// callback, since reading them runs a process per file and the lineage asks only when the
// policy moved.
func (r *WorkspaceRules) policyState() PolicyState {
	policy := r.Policy()
	files := make([]string, len(policy.Sources))
	for i, s := range policy.Sources {
		files[i] = s.Path
	}
	return PolicyState{
		Files:       files,
		Digest:      policy.Digest,
		ShellRules:  policy.ShellRules,
		SpawnRule:   policy.SpawnRule,
		CommandRule: policy.CommandRule,
		WriteRule:   policy.WriteRule,
		Sources: func(ctx context.Context) []PolicySource {
			approved := r.approvedContentIDs(ctx, files)
			if approved == nil {
				return nil
			}
			out := make([]PolicySource, len(policy.Sources))
			for i, s := range policy.Sources {
				out[i] = PolicySource{Path: s.Path, Worktree: s.ContentID, Approved: approved[s.Path]}
			}
			return out
		},
	}
}

// approvedContentIDs maps each path to the interp.ContentID of its approved content, ""
// when the approved state has no such file. Nil when the workspace has no approval
// authority. A path Pending does not cover is hashed from disk; the rest are read through
// the authority, which can cost a process per file.
func (r *WorkspaceRules) approvedContentIDs(ctx context.Context, paths []string) map[string]string {
	approved, ok, err := approvalAuthority(ctx, r.root, r.opts, r.cacheDir)
	if err != nil || !ok {
		return nil
	}
	defer func() { _ = approved.Close() }()
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

// approvedIfChanged is the approved sources' registry for these rules, nil when it cannot
// differ from what the working tree's load registered: no approval authority, nothing
// pending, or nothing pending that the root load read or could read. The first answer is
// kept for every later caller on r.
func (r *WorkspaceRules) approvedIfChanged(ctx context.Context) (*workspace.WorkspaceRegistry, error) {
	return r.approved.get(ctx, r.resolveApprovedIfChanged)
}

func (r *WorkspaceRules) resolveApprovedIfChanged(ctx context.Context) (*workspace.WorkspaceRegistry, error) {
	approved, ok, err := approvalAuthority(ctx, r.root, r.opts, r.cacheDir)
	if err != nil || !ok {
		return nil, err
	}
	pending, err := approved.Pending(ctx, loadScope(r.root, r.sources))
	if err != nil || len(pending) == 0 {
		return nil, err
	}
	if !pendingTouchesLoad(r.root, newPendingSet(pending), r.sources) {
		// Every file the root load read holds its approved content, so the approved load
		// would read the same bytes and register the working tree's rules.
		return nil, nil //nolint:nilnil // no approved difference is the documented nil answer
	}
	if !approved.batched() {
		// One status over the whole tree lets approvedReader read clean files from
		// disk instead of starting a process for each.
		if pending, err = approved.Pending(ctx, nil); err != nil {
			return nil, err
		}
	}
	return approvedRegistry(ctx, r.root, approved, pending)
}

// effectiveBuiltins returns every compiled rule's setting as the guard applies it: per
// rule, the stricter of the approved and the working-tree declaration, each expanded from
// its own magus\guard.profile level raised by the person's agents.profile and resolved
// over the compiled defaults, so a loosening waits for approval and a tightening applies
// at once. Nil when neither side sets any rule, as at the off level with nothing declared,
// which the guard reads as the defaults; an unset level is light, which sets rules.
//
// When the approved declaration cannot be resolved the defaults at the light level stand
// in for it, beside the error: a working tree can still tighten, and cannot loosen past
// what magus ships. A magus.yaml that does not load raises nothing, also beside the error.
func (r *WorkspaceRules) effectiveBuiltins(ctx context.Context) (map[string]builtin.Setting, error) {
	declared, approvedProfile := r.Builtins(), r.registry.Profile()
	// One approved load for the declaration and the level both: each costs a VCS status,
	// and a second load of the root magusfile when the working tree differs.
	reg, approvedErr := r.approvedIfChanged(ctx)
	switch {
	case approvedErr != nil:
		declared, approvedProfile = nil, ""
	case reg != nil:
		declared, approvedProfile = reg.Builtins(), reg.Profile()
	}
	agents, agentsErr := config.LoadAgents(r.root)
	approved, err := builtin.ResolveProfile(approvedProfile.Stricter(agents.Profile), declared)
	if err != nil {
		return nil, err
	}
	working, err := builtin.ResolveProfile(r.registry.Profile().Stricter(agents.Profile), r.Builtins())
	if err != nil {
		return nil, err
	}
	sideErr := errors.Join(approvedErr, agentsErr)
	if len(approved) == 0 && len(working) == 0 {
		return nil, sideErr
	}
	effective, err := stricterBuiltins(approved, working)
	if err != nil {
		return nil, err
	}
	return effective, sideErr
}

// stricterBuiltins resolves both declarations and keeps the stricter setting of each rule.
func stricterBuiltins(approved, working map[string]builtin.Setting) (map[string]builtin.Setting, error) {
	out, err := builtin.Resolve(approved)
	if err != nil {
		return nil, fmt.Errorf("approved magus\\guard.builtins: %w", err)
	}
	w, err := builtin.Resolve(working)
	if err != nil {
		return nil, err
	}
	for name, s := range w {
		out[name] = builtin.Stricter(out[name], s)
	}
	return out, nil
}

// ApprovedSpawnRule returns the magus\guard.spawn rule as the approved sources register it.
// Nil with no error when nothing is pending (the working tree's rule already is the
// approved one), when the workspace has no approval authority, or when the approved sources
// register no rule. An error means the approved rule could not be resolved, which is not
// the fact that there is none. The guard runs it beside SpawnRule and keeps the stricter
// answer.
//
// It costs one VCS status when no file the root load read differs from its approved
// content, and a second load of the root magusfile when one does, paid once per r: the
// settings, the level and the other Approved rules answer from the same resolve.
func (r *WorkspaceRules) ApprovedSpawnRule(ctx context.Context) (workspace.SpawnRule, error) {
	reg, err := r.approvedIfChanged(ctx)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.SpawnRule(), nil
}

// ApprovedCommandRule is ApprovedSpawnRule for the magus\guard.command rule.
func (r *WorkspaceRules) ApprovedCommandRule(ctx context.Context) (workspace.CommandRule, error) {
	reg, err := r.approvedIfChanged(ctx)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.CommandRule(), nil
}

// ApprovedWriteRule is ApprovedSpawnRule for the magus\guard.write rule.
func (r *WorkspaceRules) ApprovedWriteRule(ctx context.Context) (workspace.WriteRule, error) {
	reg, err := r.approvedIfChanged(ctx)
	if err != nil || reg == nil {
		return nil, err
	}
	return reg.WriteRule(), nil
}

// Wiring is what NewDependencies takes from its caller: the facts this package cannot
// resolve without importing the code that judges through it, as values and closures.
type Wiring struct {
	// Root is the workspace whose rules are loaded, "" when the caller stands in none, which
	// leaves the compiled rules alone.
	Root string
	// Config is the caller's loaded configuration: the VCS options, the notes store and the
	// version floor a load failure is explained against.
	Config config.Config
	// BinaryVersion and Binary are the running build's version and path, recorded on every
	// trail row. Binary may be "" when the caller is not a magus binary.
	BinaryVersion string
	Binary        string
	// Inspect, HumanOnlyRecord and CacheDir become the Dependencies members of the same name.
	Inspect         func(ctx context.Context, root string) (types.WorkspaceRepository, error)
	HumanOnlyRecord func(ctx context.Context, root string) (types.HumanOnlyIndex, bool)
	CacheDir        func(root string) (string, error)
	// Trace starts the named phase of the caller's trace and returns its end. Nil traces
	// nothing.
	Trace func(ctx context.Context, phase string) (end func())
}

func (w Wiring) trace(ctx context.Context, phase string) func() {
	if w.Trace == nil {
		return func() {}
	}
	return w.Trace(ctx, phase)
}

func (w Wiring) vcsOptions() types.VCSOptions {
	return types.VCSOptions{Enabled: w.Config.VCS.Enabled, Name: w.Config.VCS.Name, BaseRef: w.Config.VCS.BaseRef}
}

// NewDependencies builds what Judge needs for the workspace at w.Root: its rules, loaded
// from the root magusfile alone, and the approved rules beside them, the compiled rules'
// settings, the VCS reads and w's closures. The rules come back too, nil when none loaded,
// for a caller that must know which files they came from.
//
// A working tree whose rules do not load is not an error here: Dependencies.LoadFailure
// says why, the approved sources still judge, and the verdict reports the failure. The
// graph-index lookups and the model facts are left nil for a caller that has them.
func NewDependencies(ctx context.Context, w Wiring) (Dependencies, *WorkspaceRules) {
	end := w.trace(ctx, "guard.load_rules")
	rules, loaded, loadErr := TimeRuleLoad(ctx, func() (*WorkspaceRules, error) {
		if w.Root == "" {
			return nil, nil //nolint:nilnil // outside a workspace there are no rules and nothing failed
		}
		return LoadWorkspaceRules(ctx, w.Root, w.vcsOptions())
	})
	end()
	deps := Dependencies{
		Inspect:         w.Inspect,
		HumanOnlyRecord: w.HumanOnlyRecord,
		CacheDir:        w.CacheDir,
		NotesShared:     w.Config.Knowledge.Notes.Shared,
		Spells:          project.DefaultSpellRegistry().All,
		Revision:        revisionAt,
		TrackedFiles:    trackedFilesAt,
		CheckoutState:   checkoutStateAt,
		VCS:             w.vcsOptions(),
		DefaultCharms:   w.Config.DefaultCharms,
		BinaryVersion:   w.BinaryVersion,
		Binary:          w.Binary,
		RuleLoad:        loaded,
	}
	// LoadAgents, not w.Config: only the workspace tier sets the brief format. A load that
	// fails is reported by the builtins, which read the same section.
	if agents, err := config.LoadAgents(w.Root); err == nil {
		deps.BriefFormat = agents.BriefFormat
	}
	if rules != nil {
		rules.cacheDir = w.CacheDir
		deps.ShellRules, deps.ShellDialect = rules.ShellRules()
		deps.SpawnRule = rules.SpawnRule()
		deps.ApprovedSpawnRule = rules.ApprovedSpawnRule
		deps.CommandRule = rules.CommandRule()
		deps.ApprovedCommandRule = func(ctx context.Context) (workspace.CommandRule, error) {
			defer w.trace(ctx, "guard.approved_resolve")()
			return rules.ApprovedCommandRule(ctx)
		}
		deps.WriteRule = rules.WriteRule()
		deps.ApprovedWriteRule = rules.ApprovedWriteRule
		deps.Policy = rules.policyState
		deps.Builtins = w.builtins(ctx, rules)
		return deps, rules
	}
	if w.Root == "" {
		return deps, nil
	}
	// The approved sources load when the working tree does not, and they are what an agent
	// breaking the working tree must not be able to turn off.
	// Annotated like every other load failure: a binary older than the tree says so, and
	// the guard reads that off the error to decide what the failure costs the session.
	deps.LoadFailure = ward.ExplainStaleBinary(loadErr, w.BinaryVersion, w.Config.RequiredVersion)
	// One load of the approved sources for the settings and every function rule, as
	// WorkspaceRules keeps for a working tree that loads.
	var approved approvedOnce
	registry := func(ctx context.Context) (*workspace.WorkspaceRegistry, error) {
		return approved.get(ctx, func(ctx context.Context) (*workspace.WorkspaceRegistry, error) {
			return loadApprovedRegistry(ctx, w.Root)
		})
	}
	deps.Builtins = w.approvedBuiltins(ctx, registry)
	deps.ApprovedSpawnRule = func(ctx context.Context) (workspace.SpawnRule, error) {
		reg, err := registry(ctx)
		if err != nil || reg == nil {
			return nil, err
		}
		return reg.SpawnRule(), nil
	}
	deps.ApprovedCommandRule = func(ctx context.Context) (workspace.CommandRule, error) {
		reg, err := registry(ctx)
		if err != nil || reg == nil {
			return nil, err
		}
		return reg.CommandRule(), nil
	}
	deps.ApprovedWriteRule = func(ctx context.Context) (workspace.WriteRule, error) {
		reg, err := registry(ctx)
		if err != nil || reg == nil {
			return nil, err
		}
		return reg.WriteRule(), nil
	}
	return deps, nil
}

// builtins is BuiltinSettings for a working tree that loaded, traced.
func (w Wiring) builtins(ctx context.Context, rules *WorkspaceRules) map[string]builtin.Setting {
	defer w.trace(ctx, "guard.builtins_resolve")()
	return BuiltinSettings(ctx, rules, "")
}

// approvedBuiltins is BuiltinSettings for a working tree that did not load, traced, read off
// the registry the function rules share.
func (w Wiring) approvedBuiltins(ctx context.Context, registry func(context.Context) (*workspace.WorkspaceRegistry, error)) map[string]builtin.Setting {
	defer w.trace(ctx, "guard.builtins_resolve")()
	if reg, err := registry(ctx); err == nil {
		if settings, err := approvedBuiltins(w.Root, reg); err == nil && settings != nil {
			return settings
		}
	}
	return lightBuiltins()
}

// revisionAt answers the revision rev names in the checkout holding dir, for the push gate,
// or "" when it cannot be read. Empty rev is the checkout's current revision; empty dir is
// this process's working directory.
//
// Resolved through the VCS layer rather than by shelling out to git, because magus drives
// four backends and the hook runs in whichever the workspace uses. Every failure answers
// "": no VCS, an unreadable one, a rev that names nothing, or a repository with no commit
// yet all mean the push gate has nothing to match a recorded run against, and it stands
// down rather than refusing on an absence it cannot account for.
func revisionAt(ctx context.Context, dir, rev string) string {
	root, err := workspace.FindRoot(dir)
	if err != nil {
		return ""
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return ""
	}
	// Not Metadata: it runs a status among several processes and overruns the hook
	// budget on Mercurial and Sapling.
	c, err := res.VCS.FindCommit(ctx, root, rev)
	if err != nil {
		return ""
	}
	return cmp.Or(c.Short, c.ID)
}

// checkoutStateAt reads the checkout holding dir through the version control that resolves
// there, for a rule judging a push or a commit, with the base ref that resolution chose.
// Nil when there is none, when its driver cannot report the state, or when the read fails:
// a rule reads nil as unknown.
func checkoutStateAt(ctx context.Context, dir string) *types.CheckoutState {
	if dir == "" {
		return nil
	}
	res, err := vcs.Resolve(ctx, dir, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil
	}
	state, err := res.VCS.CheckoutState(ctx, dir)
	if err != nil {
		return nil
	}
	state.Base = res.Base
	return &state
}

// trackedFilesAt lists every file the checkout at root tracks, through whichever version
// control the workspace uses; false when it cannot say before ctx ends.
func trackedFilesAt(ctx context.Context, root string) ([]string, bool) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return nil, false
	}
	files, err := res.VCS.TrackedFiles(ctx, root, []string{"."})
	return files, err == nil && ctx.Err() == nil
}

type hostJudgedKey struct{}

// WithHostJudged marks ctx as a call the guard must not judge in process: a CLI
// invocation, which an agent host's hook judged before it ran and which a person typing it
// is answerable for. An entry point that judges its own calls skips the judgment under
// this mark, so no call is judged twice.
func WithHostJudged(ctx context.Context) context.Context {
	return context.WithValue(ctx, hostJudgedKey{}, true)
}

// HostJudged reports whether ctx carries WithHostJudged's mark.
func HostJudged(ctx context.Context) bool {
	judged, _ := ctx.Value(hostJudgedKey{}).(bool)
	return judged
}
