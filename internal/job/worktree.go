package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// binaryName is the file a workspace runs its own magus from, at its root.
const binaryName = "magus"

// MirroredBinary is the magus a fork places in the checkout it makes: exe when it is the
// forking checkout's own ./magus, so the job's hooks judge with the binary its forker
// runs, and "" when exe is anywhere else, such as on PATH, where the job finds it too.
func MirroredBinary(exe, root string) string {
	if exe == "" || root == "" {
		return ""
	}
	ran, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	local, err := os.Stat(filepath.Join(root, binaryName))
	if err != nil || !os.SameFile(ran, local) {
		return ""
	}
	return exe
}

// ForkWorktree makes job id's checkout at dir on a branch named id, at checkpoint or the
// forker's revision, or adopts dir when it already stands as a linked checkout (made false).
// binary (see [MirroredBinary]) is copied in only where [BuildsItsOwnMagus] is false; a
// checkout that builds its own is left for [CheckoutBootstrap], so a dirty forker never blocks.
// A job holding another checkout its fork made is refused; one holding an adopted checkout is
// bound to dir, since moving it loses nothing. Nothing is stored: the caller records wt, or
// discards it with [Store.DiscardWorktree] when the fork is refused.
func (s *Store) ForkWorktree(ctx context.Context, id, dir, checkpoint, binary string) (wt types.JobWorktree, made bool, err error) {
	dir = filepath.Clean(dir)
	rows, err := s.List()
	if err != nil {
		return types.JobWorktree{}, false, err
	}
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 && rows[i].Worktree.Standing() {
		held := *rows[i].Worktree
		switch {
		case samePath(held.Root, dir):
			held.Kept = ""
			return held, false, nil
		case held.Made:
			return types.JobWorktree{}, false, fmt.Errorf("job: %s already owns the checkout %s, which its fork made and magus removes once the job ends, so fork it with `--checkout %s` or let it end first",
				id, held.Root, held.Root)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		wt, err := s.adoptCheckout(ctx, id, dir)
		return wt, false, err
	}
	revision, digest := types.ParseCheckpointToken(checkpoint)
	if digest != "" {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkpoint %s carries uncommitted changes, which a new checkout made at %s would not hold: commit them, then fork at the commit",
			id, checkpoint, revision)
	}
	if revision == "" {
		// A job that names no checkpoint starts from what its forker is on; the forker's
		// uncommitted changes stay where they are.
		token, err := s.CheckoutToken(ctx)
		if err != nil {
			return types.JobWorktree{}, false, fmt.Errorf("%s names a checkout and no checkpoint, and the forking checkout's revision cannot be read: %w", id, err)
		}
		revision, _ = types.ParseCheckpointToken(token)
	}
	source, err := filepath.Abs(s.root)
	if err != nil {
		return types.JobWorktree{}, false, err
	}
	drv, why := resolveDriver(ctx, source)
	if drv == nil {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s names a checkout, and %s", id, why)
	}
	base, err := drv.FindCommit(ctx, source, revision)
	if err != nil {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkout starts at %s, which %s cannot find: %w", id, revision, drv.Name(), err)
	}
	if err := drv.CreateCheckout(ctx, source, dir, base.ID, id); err != nil {
		return types.JobWorktree{}, false, fmt.Errorf("job: %s's checkout at %s: %w", id, dir, err)
	}
	wt = types.JobWorktree{Root: dir, Source: source, Base: base.ID, Created: time.Now().Unix(), Made: true}
	if binary != "" && !BuildsItsOwnMagus(dir) {
		placed, err := placeBinary(binary, dir)
		if err != nil {
			return types.JobWorktree{}, false, errors.Join(fmt.Errorf("placing %s in %s: %w", binary, dir, err), drv.RemoveCheckout(ctx, source, dir))
		}
		wt.Binary = placed
	}
	return wt, true, nil
}

// BuildsItsOwnMagus reports whether the checkout at dir holds [CheckoutBootstrapScript], and
// so builds its ./magus from its own sources instead of taking a copy.
func BuildsItsOwnMagus(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(CheckoutBootstrapScript)))
	return err == nil && info.Mode().IsRegular()
}

// adoptCheckout records dir, a standing linked checkout of this repository, as job id's:
// nothing is made or placed, and its base is what it has checked out. Its source is the
// primary checkout, so removing it once the job ends never runs from inside it.
func (s *Store) adoptCheckout(ctx context.Context, id, dir string) (types.JobWorktree, error) {
	source, err := filepath.Abs(s.root)
	if err != nil {
		return types.JobWorktree{}, err
	}
	drv, why := resolveDriver(ctx, source)
	if drv == nil {
		return types.JobWorktree{}, fmt.Errorf("job: %s names the checkout %s, and %s", id, dir, why)
	}
	registered, err := drv.RegisteredCheckouts(ctx, source)
	if err != nil {
		return types.JobWorktree{}, fmt.Errorf("job: %s names the checkout %s, and the checkouts %s registers cannot be read: %w", id, dir, drv.Name(), err)
	}
	i := slices.IndexFunc(registered, func(c types.RegisteredCheckout) bool { return !c.Primary && samePath(c.Root, dir) })
	p := slices.IndexFunc(registered, func(c types.RegisteredCheckout) bool { return c.Primary })
	if i < 0 || p < 0 {
		return types.JobWorktree{}, fmt.Errorf("job: %s names %s as its checkout, which exists and is not a linked checkout of this repository: name a directory that does not exist yet, which the fork makes, or a linked checkout to adopt",
			id, dir)
	}
	wt := types.JobWorktree{Root: dir, Source: registered[p].Root, Base: registered[i].Head, Created: time.Now().Unix()}
	if info, err := os.Stat(filepath.Join(dir, binaryName)); err == nil && info.Mode().IsRegular() {
		wt.Binary = filepath.Join(dir, binaryName)
	}
	return wt, nil
}

// OwnedCheckout is the root of the standing checkout a row records that dir lies in, "" when
// no row records one there.
func (s *Store) OwnedCheckout(dir string) (string, error) {
	rows, err := s.List()
	if err != nil {
		return "", err
	}
	for _, r := range rows {
		if r.Worktree.Standing() && within(dir, r.Worktree.Root) {
			return r.Worktree.Root, nil
		}
	}
	return "", nil
}

// ParentCheckout is the checkout job parent works in when it is not here: the one its fork
// made or adopted (bound true, so a child inherits it), else the one it was taken in (bound
// false). It is "" when parent is unknown, works here, or has not been taken.
func (s *Store) ParentCheckout(here, parent string) (dir string, bound bool, err error) {
	rows, err := s.List()
	if err != nil {
		return "", false, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == parent })
	if i < 0 {
		return "", false, nil
	}
	bound = rows[i].Worktree.Standing()
	dir = heldCheckout(rows[i])
	if dir == "" || samePath(dir, here) {
		return "", false, nil
	}
	return dir, bound, nil
}

// LinkedCheckout reports whether dir is the top of a linked checkout of this store's
// repository, one that is not its primary.
func (s *Store) LinkedCheckout(ctx context.Context, dir string) (bool, error) {
	source, err := filepath.Abs(s.root)
	if err != nil {
		return false, err
	}
	drv, why := resolveDriver(ctx, source)
	if drv == nil {
		return false, errors.New(why)
	}
	registered, err := drv.RegisteredCheckouts(ctx, source)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(registered, func(c types.RegisteredCheckout) bool { return !c.Primary && samePath(c.Root, dir) }), nil
}

// moduleFiles decide the module graph a Go build resolves, at the root.
var moduleFiles = []string{"go.mod", "go.sum", "go.work", "go.work.sum"}

// rootScope marks the root package among the scopes. It is not a prefix: "" or "./" as one
// would take in every file at the root.
const rootScope = "./"

// ChangedBuildInputs are the paths differing between commits from and to, in the checkout
// at root, that can change the binary ./cmd/magus compiles to: a non-test Go file in a
// package `go list -deps` names, a root module file, or another file in such a package's
// directory, where go:embed reads. Each side's changes since the two diverged count. It fails
// when either commit or the package list cannot be read, so unknown inputs never match;
// equal commits run no process. hack/dev/bootstrap-worktree.buzz holds the same rule.
func ChangedBuildInputs(ctx context.Context, drv types.VCSDriver, root, from, to string) ([]string, error) {
	if from == to {
		return nil, nil
	}
	var changed []string
	for _, side := range [][2]string{{from, to}, {to, from}} {
		files, err := drv.RangeFiles(ctx, root, side[0], side[1], nil)
		if err != nil {
			return nil, fmt.Errorf("job: the paths %s changed since %s: %w", side[1], side[0], err)
		}
		changed = append(changed, files...)
	}
	slices.Sort(changed)
	changed = slices.Compact(changed)
	scopes, err := binaryScopes(ctx, root)
	if err != nil {
		return nil, err
	}
	return buildInputs(changed, scopes), nil
}

// binaryScopes asks the Go toolchain which packages under root ./cmd/magus links, as
// [packageScopes] reads them. go list loads packages without compiling any; magus refuses
// to load without jsonv2.
func binaryScopes(ctx context.Context, root string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-f",
		`{{if not .Standard}}{{.Dir}}{{range .EmbedFiles}}|{{.}}{{end}}{{end}}`, "./cmd/magus")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOEXPERIMENT=jsonv2")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("job: `go list -deps ./cmd/magus` in %s says %q: %w", root, strings.TrimSpace(stderr.String()), err)
	}
	return packageScopes(string(out), root), nil
}

// packageScopes are where a file can reach the binary, root-relative: "dir/" for a package
// below root, rootScope for the root package, a bare path for a file the root package embeds.
// listed is one package per line, its absolute directory then each go:embed file after "|";
// a directory outside root (stdlib, module cache) drops out.
func packageScopes(listed, root string) []string {
	base := filepath.Clean(root)
	var scopes []string
	for line := range strings.Lines(listed) {
		fields := strings.Split(strings.TrimSpace(line), "|")
		if fields[0] == "" {
			continue
		}
		dir := filepath.Clean(fields[0])
		if dir == base {
			scopes = append(scopes, rootScope)
			scopes = append(scopes, fields[1:]...)
			continue
		}
		if rel, ok := strings.CutPrefix(dir, base+string(filepath.Separator)); ok {
			scopes = append(scopes, filepath.ToSlash(rel)+"/")
		}
	}
	return scopes
}

// buildInputs are the paths in changed that scopes reach. A linked package deleted since
// the listing is not missed: whatever imported it changed too.
func buildInputs(changed, scopes []string) []string {
	var inputs []string
	for _, p := range changed {
		if isBuildInput(p, scopes) {
			inputs = append(inputs, p)
		}
	}
	return inputs
}

func isBuildInput(p string, scopes []string) bool {
	dir, name := path.Split(p)
	if dir == "" {
		dir = rootScope
	}
	switch {
	case dir == rootScope && slices.Contains(moduleFiles, name):
		return true
	case strings.HasSuffix(name, "_test.go"):
		return false
	case strings.HasSuffix(name, ".go"):
		return slices.Contains(scopes, dir)
	}
	return slices.ContainsFunc(scopes, func(s string) bool {
		return s == p || (s != rootScope && strings.HasSuffix(s, "/") && strings.HasPrefix(p, s))
	})
}

// CheckoutBootstrapScript builds ./magus in a checkout at the checkout's own revision.
const CheckoutBootstrapScript = "hack/dev/bootstrap-checkout.buzz"

// CheckoutBootstrap is the command that builds ./magus in dir, a checkout ForkWorktree made
// at a commit no placed binary was built at.
func CheckoutBootstrap(dir string) string {
	return fmt.Sprintf("./magus buzz %s -- --checkout %s", CheckoutBootstrapScript, dir)
}

// LandingBootstrap is [CheckoutBootstrap] for the store's own checkout, where a fork with no
// checkout of its own lands, when exe is that checkout's ./magus and was built before the Go
// build inputs of checkpoint ([ChangedBuildInputs]); "" when exe is any other magus, such as
// one on PATH, or no build input differs. checkpoint "" is the checkout's own revision. A
// build that records no commit, and commits or packages that cannot be read, are errors:
// whether the binary judges the job's tree cannot then be told.
func (s *Store) LandingBootstrap(ctx context.Context, exe, checkpoint string) (string, error) {
	if MirroredBinary(exe, s.root) == "" {
		return "", nil
	}
	build := types.MagusBuildFromContext(ctx)
	if build.Commit == "" || build.Commit == "unknown" {
		return "", fmt.Errorf("job: %s records no commit it was built from, so whether it judges the job's checkpoint cannot be told", exe)
	}
	revision, _ := types.ParseCheckpointToken(checkpoint)
	if revision == "" {
		token, err := s.CheckoutToken(ctx)
		if err != nil {
			return "", err
		}
		revision, _ = types.ParseCheckpointToken(token)
	}
	root, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	drv, why := resolveDriver(ctx, root)
	if drv == nil {
		return "", fmt.Errorf("job: %s", why)
	}
	built, err := drv.FindCommit(ctx, root, build.Commit)
	if err != nil {
		return "", fmt.Errorf("job: %s was built at %s, which %s cannot find: %w", exe, build.Commit, drv.Name(), err)
	}
	base, err := drv.FindCommit(ctx, root, revision)
	if err != nil {
		return "", fmt.Errorf("job: the checkpoint %s is a commit %s cannot find: %w", revision, drv.Name(), err)
	}
	inputs, err := ChangedBuildInputs(ctx, drv, root, built.ID, base.ID)
	if err != nil || len(inputs) == 0 {
		return "", err
	}
	return CheckoutBootstrap(root), nil
}

// DiscardWorktree removes a checkout [Store.ForkWorktree] just made for a fork that was then
// refused, before anything ran in it.
func (s *Store) DiscardWorktree(ctx context.Context, wt types.JobWorktree) error {
	drv, why := resolveDriver(ctx, wt.Source)
	if drv == nil {
		return fmt.Errorf("job: the checkout %s stays, since %s", wt.Root, why)
	}
	return drv.RemoveCheckout(ctx, wt.Source, wt.Root)
}

// placeBinary copies bin into dir by temp and rename, never in place: on darwin a later
// exec of an executable rewritten in place is killed.
func placeBinary(bin, dir string) (string, error) {
	body, err := os.ReadFile(bin)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, binaryName)
	return dst, file.WriteFileAtomic(dst, body, 0o755)
}

// RecordWorktree records wt as the checkout job id owns.
func (s *Store) RecordWorktree(ctx context.Context, id string, wt types.JobWorktree) (types.Job, error) {
	return s.writeWorktree(ctx, id, func(w *types.JobWorktree) { *w = wt })
}

// writeWorktree rewrites the worktree record of row id, the one field no declaration
// carries: see [types.Job.Worktree].
func (s *Store) writeWorktree(ctx context.Context, id string, fn func(*types.JobWorktree)) (types.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var stored types.Job
	err := s.update(ctx, func(f *jobsFile) error {
		i := slices.IndexFunc(f.Jobs, func(r types.Job) bool { return r.ID == id })
		if i < 0 {
			return fmt.Errorf("%w: %q", ErrUnknownJob, id)
		}
		if err := readOnly(f.Jobs[i]); err != nil {
			return err
		}
		row := f.Jobs[i].Clone()
		if row.Worktree == nil {
			row.Worktree = &types.JobWorktree{}
		}
		fn(row.Worktree)
		f.Jobs[i] = row
		stored = row.Clone()
		return nil
	})
	return stored, err
}

// WorkingCheckout is where row's holder works: the checkout it owns, else root. A row
// forked with --checkout is taken there and nowhere else (see [WrongCheckout]).
func WorkingCheckout(root string, row types.Job) string {
	if row.Worktree.Standing() {
		return row.Worktree.Root
	}
	return root
}

// WrongCheckout refuses taking row anywhere but the checkout it is bound to, nil when it is
// bound to none or here is that checkout.
func WrongCheckout(row types.Job, here string) error {
	if !row.Worktree.Standing() || samePath(row.Worktree.Root, here) {
		return nil
	}
	bound := fmt.Sprintf("owns the checkout %s, which its fork made for it", row.Worktree.Root)
	if !row.Worktree.Made {
		bound = fmt.Sprintf("is bound to the checkout %s, which its fork adopted for it", row.Worktree.Root)
	}
	return heldError{fmt.Sprintf("job: %s %s, and is taken there and nowhere else: run `%s` in %s",
		row.ID, bound, hint.JobExec.With(row.ID), row.Worktree.Root)}
}

// EndWorktrees removes the checkout each ended job owns, once removing it loses nothing,
// and records on the row that it did, or why it kept it. It returns the rows whose record
// changed. A checkout this store's own workspace sits in is kept, saying so, for a later
// call from elsewhere, since removing it would pull the directory out from under the caller.
// It is [Store.EndCheckouts] restoring nothing and given no ended job.
func (s *Store) EndWorktrees(ctx context.Context) ([]types.Job, error) {
	return s.EndCheckouts(ctx, nil)
}

// removeWorktree removes wt, the checkout ended job id worked in, and returns "", or
// returns why it may not: it holds a change nobody committed, or a commit nothing but it
// names. A fact that cannot be read keeps it. With written, what magus wrote in it is
// restored first (see [restoreWritten]).
func removeWorktree(ctx context.Context, id string, wt types.JobWorktree, written WrittenPaths) string {
	drv, why := resolveDriver(ctx, wt.Source)
	if drv == nil {
		return "whether it holds work cannot be read: " + why
	}
	if _, err := os.Stat(wt.Root); errors.Is(err, fs.ErrNotExist) {
		if err := drv.RemoveCheckout(ctx, wt.Source, wt.Root); err != nil {
			return "its directory is gone, and its registration cannot be removed: " + err.Error()
		}
		return ""
	}
	dirty, err := drv.DirtyFiles(ctx, wt.Root, nil)
	if err != nil {
		return "its status cannot be read: " + err.Error()
	}
	dirty = slices.DeleteFunc(dirty, func(p string) bool { return wt.Binary != "" && p == binaryName })
	if len(dirty) > 0 && written != nil {
		restored, err := restoreWritten(ctx, drv, wt.Root, dirty, written)
		if err != nil {
			return "restoring what magus wrote in it failed: " + err.Error()
		}
		dirty = slices.DeleteFunc(dirty, func(p string) bool { return slices.Contains(restored, p) })
	}
	if len(dirty) > 0 {
		return fmt.Sprintf("%d path(s) in it hold uncommitted work (%s): commit them on a branch or hand them back, and `%s` removes it",
			len(dirty), listPaths(dirty), hint.JobWait.With(id))
	}
	var bases []string
	if wt.Base != "" {
		bases = append(bases, wt.Base)
	}
	stranded, err := drv.StrandedRevisions(ctx, wt.Root, bases...)
	if err != nil {
		return "which of its commits a branch or bookmark holds cannot be read: " + err.Error()
	}
	if len(stranded) > 0 {
		return fmt.Sprintf("%d commit(s) in it, newest %.12s, are on no branch, bookmark, remote or its base %.12s; name them with a branch or bookmark", len(stranded), stranded[0], wt.Base)
	}
	if err := drv.RemoveCheckout(ctx, wt.Source, wt.Root); err != nil {
		return "removing it failed: " + err.Error()
	}
	return ""
}

// samePath compares two absolute paths by where they land, symlinks resolved.
func samePath(a, b string) bool {
	return a != "" && b != "" && resolved(a) == resolved(b)
}

// within reports whether p is root or lies under it, symlinks resolved.
func within(p, root string) bool {
	rel, err := filepath.Rel(resolved(root), resolved(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
