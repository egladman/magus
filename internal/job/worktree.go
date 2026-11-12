package job

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
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
// checkout that builds its own is left for its checkout.bootstrap
// hook, so a dirty forker never blocks.
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
			followed, err := s.followCheckpoint(ctx, id, held, checkpoint, false)
			return followed, false, err
		case held.Made:
			return types.JobWorktree{}, false, fmt.Errorf("job: %s already owns the checkout %s, which its fork made and magus removes once the job ends, so fork it with `--checkout %s` or let it end first",
				id, held.Root, held.Root)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		wt, err := s.adoptCheckout(ctx, id, dir)
		if err != nil {
			return types.JobWorktree{}, false, err
		}
		followed, err := s.followCheckpoint(ctx, id, wt, checkpoint, true)
		return followed, false, err
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
	if binary != "" && !BuildsItsOwnMagus(dir) && !builtAt(ctx, binary, base.ID) {
		slog.InfoContext(ctx, fmt.Sprintf("not placing %s in %s: it is not a clean build at %.12s", binary, dir, base.ID), attr.Notice("note"))
	} else if binary != "" && !BuildsItsOwnMagus(dir) {
		placed, err := placeBinary(binary, dir)
		if err != nil {
			return types.JobWorktree{}, false, errors.Join(fmt.Errorf("placing %s in %s: %w", binary, dir, err), drv.DiscardCheckout(ctx, source, dir, base.ID))
		}
		wt.Binary = placed
	}
	return wt, true, nil
}

// CheckpointAtHead reports whether checkpoint's revision is dir's head
// commit, and returns that head. A checkpoint naming no revision is at
// any head.
func CheckpointAtHead(ctx context.Context, dir, checkpoint string) (head string, at bool, err error) {
	drv, why := resolveDriver(ctx, dir)
	if drv == nil {
		return "", false, errors.New(why)
	}
	h, err := drv.FindCommit(ctx, dir, "")
	if err != nil {
		return "", false, err
	}
	revision, _ := types.ParseCheckpointToken(checkpoint)
	if revision == "" {
		return h.ID, true, nil
	}
	target, err := drv.FindCommit(ctx, dir, revision)
	if err != nil {
		return h.ID, false, err
	}
	return h.ID, target.ID == h.ID, nil
}

// followCheckpoint moves wt to the revision a fork names. The job's own base
// stays put, so a retry keeps its commits. Adopting allows an ancestor of the
// head, which a child in a shared checkout names.
func (s *Store) followCheckpoint(ctx context.Context, id string, wt types.JobWorktree, checkpoint string, adopting bool) (types.JobWorktree, error) {
	revision, digest := types.ParseCheckpointToken(checkpoint)
	if revision == "" {
		return wt, nil
	}
	drv, why := resolveDriver(ctx, wt.Source)
	if drv == nil {
		return wt, fmt.Errorf("job: %s names the checkpoint %s for the checkout %s, and %s", id, revision, wt.Root, why)
	}
	target, err := drv.FindCommit(ctx, wt.Source, revision)
	if err != nil {
		return wt, fmt.Errorf("job: %s's checkpoint %s is a commit %s cannot find: %w", id, revision, drv.Name(), err)
	}
	head, err := drv.FindCommit(ctx, wt.Root, "")
	if err != nil {
		return wt, fmt.Errorf("job: the head of %s, the checkout of %s, cannot be read: %w", wt.Root, id, err)
	}
	if target.ID == wt.Base {
		return wt, nil
	}
	if target.ID == head.ID {
		wt.Base = head.ID
		return wt, nil
	}
	if digest != "" {
		return wt, fmt.Errorf("job: %s's checkpoint %s carries uncommitted changes, which %s cannot move to: commit them, then fork at the commit", id, checkpoint, wt.Root)
	}
	if err := refuseUncommitted(ctx, drv, id, wt.Root, head.ID, target.ID); err != nil {
		return wt, err
	}
	contained, err := drv.IsAncestor(ctx, wt.Root, target.ID, head.ID)
	if err != nil {
		return wt, fmt.Errorf("job: whether %.12s holds %.12s, the checkpoint of %s, cannot be read: %w", head.ID, target.ID, id, err)
	}
	if contained {
		if adopting {
			return wt, nil
		}
		return wt, fmt.Errorf("job: %s's checkout %s stays at %.12s: its checkpoint %.12s is behind that head, so moving there would leave commits behind: fork at a descendant of the head, or end the job so its next fork makes a new checkout",
			id, wt.Root, head.ID, target.ID)
	}
	// Only the driver knows its head: jj's is usually the parent of an empty @.
	if err := drv.FastForward(ctx, wt.Root, target.ID); err != nil {
		return wt, fmt.Errorf("job: %s's checkout %s stays at %.12s, not its checkpoint %.12s, so fork at a descendant of the head, or end the job so its next fork makes a new checkout: %w",
			id, wt.Root, head.ID, target.ID, err)
	}
	wt.Base = target.ID
	return wt, nil
}

// refuseUncommitted keeps a checkout holding work at its head. Its own ./magus
// is not work.
func refuseUncommitted(ctx context.Context, drv types.VCSDriver, id, root, head, target string) error {
	dirty, err := drv.DirtyFiles(ctx, root, nil)
	if err != nil {
		return fmt.Errorf("job: the status of %s, the checkout of %s, cannot be read: %w", root, id, err)
	}
	dirty = slices.DeleteFunc(dirty, func(p string) bool { return p == binaryName })
	if len(dirty) > 0 {
		return fmt.Errorf("job: %s's checkout %s stays at %.12s, not its checkpoint %.12s: %d path(s) in it hold uncommitted work (%s), so commit or drop them, then fork again",
			id, root, head, target, len(dirty), listPaths(dirty))
	}
	return nil
}

// CheckoutHead is the revision the checkout at dir has checked out, "" when dir
// is no checkout or its head cannot be read.
func CheckoutHead(ctx context.Context, dir string) string {
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	drv, _ := resolveDriver(ctx, dir)
	if drv == nil {
		return ""
	}
	head, err := drv.FindCommit(ctx, dir, "")
	if err != nil {
		return ""
	}
	return head.ID
}

// StartCheckpoint is the checkpoint job id starts from in wt: declared, or
// wt's head when declared names no revision. A re-fork into the checkout
// rows record for id starts at that head too, and note says so. Its commits
// are the job's own, and exec would read them as a divergence. An unread
// head leaves declared, else wt.Base.
func StartCheckpoint(ctx context.Context, rows []types.Job, id, declared string, wt types.JobWorktree) (checkpoint, note string) {
	head, at, err := CheckpointAtHead(ctx, wt.Root, declared)
	revision, _ := types.ParseCheckpointToken(declared)
	switch {
	case head == "" && revision == "":
		return wt.Base, ""
	case head == "" || err != nil:
		return declared, ""
	case revision == "":
		return head, ""
	case at:
		return declared, ""
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 || !rows[i].Worktree.Standing() || !samePath(rows[i].Worktree.Root, wt.Root) {
		return declared, ""
	}
	return head, fmt.Sprintf("%s's checkout %s, which an earlier fork of it holds, stands at %.12s, not the checkpoint %.12s, so the job starts at %.12s",
		id, wt.Root, head, revision, head)
}

// RowLive reports whether job id has a row that has not ended, which a fork
// updates rather than declares.
func (s *Store) RowLive(id string) (bool, error) {
	rows, err := s.dry().List()
	if err != nil {
		return false, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	return i >= 0 && !rows[i].State.Terminal(), nil
}

// PlaceBuiltBinary copies into dir a ./magus built at base with nothing
// uncommitted: exe, or the one a standing checkout at base holds. It returns
// the source, or "" when none is. Each candidate's own version says what it
// holds, since a checkout's binary can outlive the base its row records.
func (s *Store) PlaceBuiltBinary(ctx context.Context, exe, base, dir string) (string, error) {
	rows, err := s.List()
	if err != nil {
		return "", err
	}
	candidates := []string{exe}
	for _, r := range rows {
		if r.Worktree.Standing() && r.Worktree.Base == base && r.Worktree.Binary != "" {
			candidates = append(candidates, r.Worktree.Binary)
		}
	}
	for _, bin := range candidates {
		if !builtAt(ctx, bin, base) {
			continue
		}
		if _, err := placeBinary(bin, dir); err != nil {
			return "", fmt.Errorf("placing %s in %s: %w", bin, dir, err)
		}
		return bin, nil
	}
	return "", nil
}

// builtVersion is what `magus version -o json` says about a build.
type builtVersion struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Embedded struct {
		Contract string `json:"contract_sha256"`
	} `json:"embedded"`
}

// askVersion is what the magus at bin says about its build. ok is false
// when it cannot answer.
func askVersion(ctx context.Context, bin string) (builtVersion, bool) {
	var v builtVersion
	if bin == "" {
		return v, false
	}
	out, err := exec.CommandContext(ctx, bin, "version", "--client", "-o", "json").Output()
	if err != nil {
		return v, false
	}
	return v, json.Unmarshal(out, &v) == nil
}

// builtAt reports whether the magus at bin says it was built at base, clean.
// A binary that cannot answer is not one to reuse.
func builtAt(ctx context.Context, bin, base string) bool {
	v, ok := askVersion(ctx, bin)
	return ok && v.Commit == base && !strings.HasSuffix(v.Version, "-dirty")
}

// BinaryContract is the contract digest the magus at bin was built with,
// "" when it cannot say. A binary built before the digest existed says "".
func BinaryContract(ctx context.Context, bin string) string {
	v, _ := askVersion(ctx, bin)
	return v.Embedded.Contract
}

// InstallProjects is the comma-joined projects with an install target that own
// a file row writes, "none" when no such project does. A project whose spells
// are unresolved cannot list its targets, so it is left out.
func InstallProjects(root string, row types.Job, projects []*types.Project) string {
	if row.ReadOnly {
		return noInstall
	}
	var found []string
	for _, f := range writtenFiles(root, row) {
		p := owningProject(projects, f)
		if p != nil && runsTarget(p, "install") && !slices.Contains(found, p.Path) {
			found = append(found, p.Path)
		}
	}
	if len(found) == 0 {
		return noInstall
	}
	slices.Sort(found)
	return strings.Join(found, ",")
}

// InstallTargets is the sorted paths of the projects that declare an install
// target. A project whose spells are unresolved cannot list its targets, so it
// is left out.
func InstallTargets(projects []*types.Project) []string {
	found := []string{}
	for _, p := range projects {
		if runsTarget(p, "install") {
			found = append(found, p.Path)
		}
	}
	slices.Sort(found)
	return found
}

// noInstall is what a checkout.bootstrap hook's --install reads as no project.
const noInstall = "none"

// BuildsItsOwnMagus reports whether the checkout at dir declares a
// checkout.bootstrap hook and holds it, and so builds its ./magus from its own
// sources instead of taking a copy. A magus.yaml that does not load reads as
// no hook, leaving the copy.
func BuildsItsOwnMagus(dir string) bool {
	hook, err := CheckoutHook(dir)
	if err != nil || hook == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(hook)))
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

// NoCheckoutHook says that a workspace declares no checkout.bootstrap hook.
const NoCheckoutHook = "no checkout.bootstrap declared"

// CheckoutHook is the checkout.bootstrap script the workspace at root declares
// in its own magus.yaml, slash-separated, or "" when it declares none. A path
// that leaves root is an error.
func CheckoutHook(root string) (string, error) {
	cfg, err := config.LoadWorkspaceOnly(root)
	if err != nil {
		return "", fmt.Errorf("job: read checkout.bootstrap in %s: %w", root, err)
	}
	hook := cfg.Checkout.Bootstrap
	if hook == "" {
		return "", nil
	}
	if !filepath.IsLocal(filepath.FromSlash(hook)) {
		return "", fmt.Errorf("job: checkout.bootstrap %q in %s leaves the workspace", hook, root)
	}
	return path.Clean(filepath.ToSlash(hook)), nil
}

// CheckoutBootstrap is the command that runs hook, a workspace's
// checkout.bootstrap, on dir. An empty hook is [NoCheckoutHook].
func CheckoutBootstrap(hook, dir string) string {
	if hook == "" {
		return NoCheckoutHook
	}
	return fmt.Sprintf("./magus buzz %s -- --checkout %s", hook, dir)
}

// RunsCheckoutHook reports whether script, the file a `magus buzz` call
// names, is hook. An empty hook matches nothing.
func RunsCheckoutHook(script, hook string) bool {
	return hook != "" && strings.HasSuffix(path.Clean("/"+script), "/"+hook)
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
	hook, err := CheckoutHook(root)
	if err != nil {
		return "", err
	}
	return CheckoutBootstrap(hook, root), nil
}

// reviewFetchRemote is the remote [ReviewCheckout.Checkout] fetches a ref from.
// Only git fetches a ref by name, and its default remote is origin.
const reviewFetchRemote = "origin"

// skippedBootstrap is the hook note a checkout made with no bootstrap carries.
const skippedBootstrap = "--no-bootstrap: nothing ran in the checkout"

// ReviewCheckoutRecord is what `magus vcs checkout -o json` prints. The
// console's CheckoutPull reads it, so its field names are fixed.
type ReviewCheckoutRecord struct {
	Path      string          `json:"path"`
	Revision  string          `json:"revision"`
	VCS       string          `json:"vcs"`
	Bootstrap ReviewBootstrap `json:"bootstrap"`
	Remove    string          `json:"remove"`
}

// ReviewBootstrap says what readied a review checkout. Install lists the
// projects whose install target ran there, Hook the checkout.bootstrap that ran
// or was skipped, and HookNote why none ran.
type ReviewBootstrap struct {
	Ran       bool     `json:"ran"`
	Install   []string `json:"install"`
	Harnesses bool     `json:"harnesses"`
	Hook      string   `json:"hook"`
	HookNote  string   `json:"hook_note"`
}

// ReviewCheckout makes checkouts of one revision each, for review, under Base.
// Root is the workspace asking. Installs lists the projects in a checkout that
// declare an install target, and Magus runs magus with args in a directory.
type ReviewCheckout struct {
	Root     string
	Base     string
	Driver   types.VCSDriver
	Installs func(ctx context.Context, dir string) ([]string, error)
	Magus    func(ctx context.Context, dir string, args ...string) error
}

// removeReviewCommand is the command that removes the review checkout at dir.
func removeReviewCommand(dir string) string {
	return "magus vcs checkout --remove " + dir
}

// Checkout fetches ref from [reviewFetchRemote] when it is set, makes a
// detached checkout of rev at Base/checkout-<short rev>, and readies it unless
// noBootstrap. Neither the fetch nor the checkout runs the revision's code.
// A step that fails once the checkout exists keeps it, and the error names
// the command that removes it.
func (r ReviewCheckout) Checkout(ctx context.Context, rev, ref string, noBootstrap bool) (ReviewCheckoutRecord, error) {
	if ref != "" {
		if _, err := r.Driver.FetchRef(ctx, r.Root, reviewFetchRemote, ref); err != nil {
			return ReviewCheckoutRecord{}, fmt.Errorf("vcs checkout: fetch %s from %s: %w", ref, reviewFetchRemote, err)
		}
	}
	commit, err := r.Driver.FindCommit(ctx, r.Root, rev)
	if err != nil {
		return ReviewCheckoutRecord{}, fmt.Errorf("vcs checkout: %s cannot find %s: %w", r.Driver.Name(), rev, err)
	}
	if err := os.MkdirAll(r.Base, 0o755); err != nil {
		return ReviewCheckoutRecord{}, fmt.Errorf("vcs checkout: %w", err)
	}
	dir := filepath.Join(r.Base, "checkout-"+ShortRevision(commit.ID))
	if err := r.Driver.CreateCheckout(ctx, r.Root, dir, commit.ID, ""); err != nil {
		return ReviewCheckoutRecord{}, fmt.Errorf("vcs checkout: %s cannot check out %s at %s: %w", r.Driver.Name(), commit.ID, dir, err)
	}
	rec := ReviewCheckoutRecord{Path: dir, Revision: commit.ID, VCS: r.Driver.Name(), Remove: removeReviewCommand(dir)}
	rec.Bootstrap, err = r.bootstrap(ctx, dir, noBootstrap)
	if err != nil {
		return rec, fmt.Errorf("vcs checkout: readying %s failed, so it stays, and `%s` removes it: %w", dir, rec.Remove, err)
	}
	return rec, nil
}

// bootstrap runs the declared install targets in dir, then the harness
// install, then Root's checkout.bootstrap hook from Root.
func (r ReviewCheckout) bootstrap(ctx context.Context, dir string, skip bool) (ReviewBootstrap, error) {
	hook, err := CheckoutHook(r.Root)
	rec := ReviewBootstrap{Install: []string{}, Hook: hook}
	switch {
	case err != nil:
		return rec, err
	case skip:
		rec.HookNote = skippedBootstrap
		return rec, nil
	}
	rec.Ran = true
	projects, err := r.Installs(ctx, dir)
	if err != nil {
		return rec, err
	}
	for _, p := range projects {
		if err := r.Magus(ctx, dir, "run", "install", p); err != nil {
			return rec, fmt.Errorf("magus run install %s: %w", p, err)
		}
		rec.Install = append(rec.Install, p)
	}
	if err := r.Magus(ctx, dir, "agent", "harness", "install"); err != nil {
		return rec, fmt.Errorf("magus agent harness install: %w", err)
	}
	rec.Harnesses = true
	if hook == "" {
		rec.HookNote = NoCheckoutHook
		return rec, nil
	}
	if err := r.Magus(ctx, r.Root, "buzz", hook, "--", "--checkout", dir, "--install", noInstall); err != nil {
		return rec, fmt.Errorf("`%s`: %w", CheckoutBootstrap(hook, dir), err)
	}
	return rec, nil
}

// Remove removes the review checkout at dir, which must lie under Base.
func (r ReviewCheckout) Remove(ctx context.Context, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("vcs checkout --remove: %w", err)
	}
	if rel, err := filepath.Rel(r.Base, abs); err != nil || rel == "." || !filepath.IsLocal(rel) {
		return fmt.Errorf("vcs checkout --remove: %s is not under %s, where magus vcs checkout makes its checkouts", abs, r.Base)
	}
	if err := r.Driver.RemoveCheckout(ctx, r.Root, abs); err != nil {
		return fmt.Errorf("vcs checkout --remove: %s cannot remove %s: %w", r.Driver.Name(), abs, err)
	}
	return nil
}

// ShortRevision is the first twelve characters of a revision id.
func ShortRevision(id string) string {
	return id[:min(len(id), 12)]
}

// DiscardWorktree removes a checkout [Store.ForkWorktree] just made for a fork that was then
// refused, before anything ran in it.
// Its branch goes too while it sits at the base.
func (s *Store) DiscardWorktree(ctx context.Context, wt types.JobWorktree) error {
	drv, why := resolveDriver(ctx, wt.Source)
	if drv == nil {
		return fmt.Errorf("job: the checkout %s stays, since %s", wt.Root, why)
	}
	return drv.DiscardCheckout(ctx, wt.Source, wt.Root, wt.Base)
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

// BinaryDigest is the content hash of the file at bin, "sha256:<hex>".
func BinaryDigest(bin string) (string, error) {
	f, err := os.Open(bin)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("job: hash %s: %w", bin, err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// digestCacheName is the file beside the job store that keeps each judging
// binary's hash, so every checkout of the repository shares one.
const digestCacheName = "binary-digests.jsonl"

// binaryDigest is one hash of the file at Path. It holds while the file keeps
// the identity, size and modification time it was hashed under. A writer can
// forge all three, which ADR 0045 section 1 accepts.
type binaryDigest struct {
	Path   string `json:"path"`
	Ino    uint64 `json:"ino,omitempty"`
	Size   int64  `json:"size"`
	ModNs  int64  `json:"mod_ns"`
	Digest string `json:"digest"`
}

func (d binaryDigest) describes(fi os.FileInfo) bool {
	ino, _, _ := file.Identity(fi)
	return d.Size == fi.Size() && d.ModNs == fi.ModTime().UnixNano() && d.Ino == ino
}

// cachedDigest is [BinaryDigest] read through the cache beside the store.
// The cache is never the authority. One that does not read is rehashed.
func (s *Store) cachedDigest(bin string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	// Stat before the hash: a write in between changes what the next stat sees.
	fi, err := os.Stat(bin)
	if err != nil {
		return "", err
	}
	cache := digestCache(filepath.Join(filepath.Dir(s.path), digestCacheName))
	entries, _ := cache.Load() // an unreadable cache is rehashed
	key := resolved(bin)
	for _, e := range entries {
		if e.Path == key && e.describes(fi) {
			return e.Digest, nil
		}
	}
	digest, err := BinaryDigest(bin)
	if err != nil {
		return "", err
	}
	ino, _, _ := file.Identity(fi)
	entry := binaryDigest{Path: key, Ino: ino, Size: fi.Size(), ModNs: fi.ModTime().UnixNano(), Digest: digest}
	_ = cache.Update(context.Background(), func([]binaryDigest) []binaryDigest { return []binaryDigest{entry} }) // a miss rehashes
	return digest, nil
}

// digestCache is the JSON Lines store at path, keyed by binary path.
func digestCache(path string) file.JSONL[binaryDigest] {
	return file.JSONL[binaryDigest]{Path: path, Key: func(d binaryDigest) string { return d.Path }}
}

// PinRefusal says why the root's hook may not hand a call to bin, "" when it
// may. It may when a live row's standing checkout records bin as its Binary
// and bin still hashes to that row's BinaryDigest.
func (s *Store) PinRefusal(bin string) (string, error) {
	rows, err := s.List()
	if err != nil {
		return "", err
	}
	pinning := slices.DeleteFunc(rows, func(r types.Job) bool {
		return !r.State.Live() || !r.Worktree.Standing() || !samePath(r.Worktree.Binary, bin)
	})
	if len(pinning) == 0 {
		return "no live job row records it as its checkout's binary", nil
	}
	digest, err := s.cachedDigest(bin)
	if err != nil {
		return "", err
	}
	if slices.ContainsFunc(pinning, func(r types.Job) bool { return r.Worktree.BinaryDigest == digest }) {
		return "", nil
	}
	return fmt.Sprintf("its bytes do not hash to what job %s pins, so neither its fork, its placement nor its integrator wrote it last", pinning[0].ID), nil
}

// PinBinary records the hash ./magus in job id's checkout has now on every
// live row standing at that checkout, and returns those rows. The root pins
// any row, and the integrator its own after it relinks there. Any other bound
// caller gets a [*RefusedError]: a worker that pinned its own build would pick
// its judge.
func (s *Store) PinBinary(ctx context.Context, id string) ([]types.Job, error) {
	rows, err := s.List()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnknownJob, id)
	}
	if actor := s.Actor(); actor.Bound() && (actor.Lease != id || len(rows[i].Integrates) == 0) {
		return nil, refuse(actor, id, "only the root and a row's own integrator pin the binary that judges it, and a worker that pinned its own build would pick its judge")
	}
	wt := rows[i].Worktree
	if !wt.Standing() {
		return nil, fmt.Errorf("job: %s owns no standing checkout, so it has no binary to pin", id)
	}
	bin := filepath.Join(wt.Root, binaryName)
	digest, err := BinaryDigest(bin)
	if err != nil {
		return nil, fmt.Errorf("hashing %s's binary to pin it: %w", id, err)
	}
	var pinned []types.Job
	for _, r := range rows {
		if !r.State.Live() || !r.Worktree.Standing() || !samePath(r.Worktree.Root, wt.Root) {
			continue
		}
		row, err := s.writeWorktree(ctx, r.ID, func(w *types.JobWorktree) { w.Binary, w.BinaryDigest = bin, digest })
		if err != nil {
			return pinned, err
		}
		pinned = append(pinned, row)
	}
	return pinned, nil
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
