package magus

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/coverage"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/clikit/generate"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// scratch exports revisions of the tree into the workspace store: the store
// holding the copies, and the version control exporting them.
type scratch struct {
	root  string
	vcs   types.VCSDriver
	store string
}

func (m *Magus) newScratch(ctx context.Context) (scratch, error) {
	res, err := vcs.Resolve(ctx, m.ws.Root, "", m.ws.VCSOptions)
	if err != nil {
		return scratch{}, fmt.Errorf("change risk: resolve version control to export the base: %w", err)
	}
	if res.VCS == nil {
		return scratch{}, types.DiagnosticErrorf(types.VCSCapabilityMissing,
			"version control is disabled, so magus cannot export the base to key it")
	}
	store, err := config.StoreDir(m.ws.Root, m.cfg)
	if err != nil {
		return scratch{}, err
	}
	return scratch{root: m.ws.Root, vcs: res.VCS, store: store}, nil
}

func (sc scratch) copy(ctx context.Context, rev string) (string, error) {
	trees := filepath.Join(sc.store, scratchTreesDir)
	return treeCopier{root: sc.root, trees: trees, vcs: sc.vcs, rev: rev}.copy(ctx)
}

// fileNamed is the report's file at name, or an empty one.
func fileNamed(files []coverage.File, name string) coverage.File {
	i := slices.IndexFunc(files, func(f coverage.File) bool { return f.Path == name })
	if i < 0 {
		return coverage.File{Path: name}
	}
	return files[i]
}

// baseProfileVersion keys the kept base profiles; bump it when what a
// profile holds changes meaning.
const baseProfileVersion = "2"

// keptProfilesDir holds the base profiles inside the workspace store, which
// every checkout shares.
const keptProfilesDir = "change-risk"

// errBaseNotCached marks a base whose profile no cache holds. Only main's CI
// writes one, so magus never runs the base to make up for it.
var errBaseNotCached = errors.New("no cache entry")

// baseProfiles reads coverage profiles at one base commit. Each is what
// main's CI cached for its target, keyed by the base's own inputs. A read
// keeps it in the workspace store by commit.
type baseProfiles struct {
	commit string
	// kept is the dir holding the kept profiles.
	kept  string
	parse func(c coverageSource, data []byte) ([]coverage.File, error)
	// copy exports the base tree. replay restores c's cached target into it,
	// returning errBaseNotCached when no cache holds the entry.
	copy   func(ctx context.Context) (string, error)
	replay func(ctx context.Context, dir string, c coverageSource) error
	dir    string
}

func (m *Magus) newBaseProfiles(ctx context.Context, commit string) (*baseProfiles, error) {
	sc, err := m.newScratch(ctx)
	if err != nil {
		return nil, err
	}
	return &baseProfiles{
		commit: commit, kept: filepath.Join(sc.store, keptProfilesDir),
		parse: func(c coverageSource, data []byte) ([]coverage.File, error) {
			return m.parseReport(c.project, c.coverage, data)
		},
		copy:   func(ctx context.Context) (string, error) { return sc.copy(ctx, commit) },
		replay: m.replayAtBase,
	}, nil
}

func (b *baseProfiles) entry(c coverageSource) string {
	h := sha256.New()
	for _, part := range []string{baseProfileVersion, c.project, c.spell, c.target, string(c.coverage.Format), c.coverage.Profile} {
		fmt.Fprintf(h, "%s\x00", part)
	}
	return filepath.Join(b.kept, fmt.Sprintf("%s-%x", b.commit, h.Sum(nil)[:8]))
}

// read is c's profile at the base commit: kept, or replayed from the cache
// entry main's CI wrote for c's target there. No entry is an error naming
// the base and the command that writes the profile.
func (b *baseProfiles) read(ctx context.Context, c coverageSource) (profileRead, error) {
	entry := b.entry(c)
	data, err := os.ReadFile(entry)
	switch {
	case err == nil:
		return b.parsed(c, data), nil
	case !errors.Is(err, fs.ErrNotExist):
		return profileRead{}, fmt.Errorf("change risk: the kept base profile: %w", err)
	}
	if data, err = b.replayed(ctx, c); err != nil {
		return profileRead{}, err
	}
	if err := os.MkdirAll(b.kept, 0o700); err != nil {
		return profileRead{}, fmt.Errorf("change risk: %w", err)
	}
	if err := file.WriteFileAtomic(entry, data, 0o600); err != nil {
		return profileRead{}, fmt.Errorf("change risk: keep the base profile: %w", err)
	}
	return b.parsed(c, data), nil
}

// replayed is the profile the cache entry of c's target at base restores.
func (b *baseProfiles) replayed(ctx context.Context, c coverageSource) ([]byte, error) {
	if b.dir == "" {
		dir, err := b.copy(ctx)
		b.dir = dir
		if err != nil {
			return nil, fmt.Errorf("change risk: export base %s: %w", b.commit, err)
		}
	}
	err := b.replay(ctx, b.dir, c)
	if errors.Is(err, errBaseNotCached) {
		return nil, fmt.Errorf("change risk: no cache holds the coverage profile of base %s, "+
			"which main's CI writes with `%s` at that commit: %w", b.commit, c.command(), err)
	}
	if err != nil {
		return nil, fmt.Errorf("change risk: base %s: %w", b.commit, err)
	}
	data, err := os.ReadFile(filepath.Join(b.dir, filepath.FromSlash(c.project), filepath.FromSlash(c.coverage.Profile)))
	if err != nil {
		return nil, fmt.Errorf("change risk: the cached `%s` at base %s restored no %s: %w", c.command(), b.commit, c.coverage.Profile, err)
	}
	return data, nil
}

func (b *baseProfiles) parsed(c coverageSource, data []byte) profileRead {
	files, err := b.parse(c, data)
	if err != nil {
		return profileRead{reason: fmt.Sprintf("the coverage profile of %s at base %s: %s", c.project, b.commit, err)}
	}
	return profileRead{files: files}
}

// remove deletes the base export, if a read made one.
func (b *baseProfiles) remove() {
	if b.dir != "" {
		_ = os.RemoveAll(b.dir)
	}
}

// replayAtBase keys c's target in dir, an export of the base, and replays
// its cache entry there. It never runs the target: a miss is
// errBaseNotCached. A base this magus cannot load is an error. The export
// is no checkout, so it is pointed at this workspace's store, where main's
// entries land locally.
func (m *Magus) replayAtBase(ctx context.Context, dir string, c coverageSource) error {
	store, err := config.StoreDir(m.ws.Root, m.cfg)
	if err != nil {
		return err
	}
	env := append(slices.Clone(m.environ), fmt.Sprintf("%s=%s", generate.EnvName("MAGUS", "cache", "dir"), store))
	base, err := Open(ctx, dir, append(m.processOptions(), WithEnv(env))...)
	if err != nil {
		return fmt.Errorf("this magus cannot load the base tree: %w", err)
	}
	defer func() { _ = base.Close() }()
	p := base.Get(c.project)
	if p == nil {
		return fmt.Errorf("the base holds no project %s", c.project)
	}
	step, err := base.targetStep(project.WithExtraArgs(ctx, nil), p, c.target, nil, nil)
	if err != nil {
		return err
	}
	_, err = base.cache.RunAside(ctx, step, func(context.Context) error { return errBaseNotCached })
	return err
}

// scratchTreesDir holds the base exports inside the workspace store. A
// sandboxed gate may write the store but not the user cache.
const scratchTreesDir = "scratch-trees"

// treeCopier exports rev into a new dir under trees.
type treeCopier struct {
	root  string
	trees string
	vcs   types.VCSDriver
	rev   string
}

func (c treeCopier) copy(ctx context.Context) (string, error) {
	if err := os.MkdirAll(c.trees, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(c.trees, "")
	if err != nil {
		return "", err
	}
	return dir, c.vcs.ExportRevision(ctx, c.root, c.rev, dir)
}

// freshProfile is a profile one run brought current. A magus nested in the
// same run reads it as it stands: its own probe keys the target from another
// process, can miss, and a run there is refused MGS3007.
type freshProfile struct {
	Project string `json:"project"`
	Profile string `json:"profile"`
	Digest  string `json:"digest"`
}

// freshProfileLife bounds the fresh-profile records: one serves only the run
// that wrote it, and no run lasts a day.
const freshProfileLife = 24 * time.Hour

// freshProfiles is what the run ctx belongs to brought current, as JSON Lines
// in the workspace store. file is "" outside a run, which vouches for nothing.
type freshProfiles struct {
	root string
	file string
}

func (m *Magus) freshProfiles(ctx context.Context) freshProfiles {
	run := types.InvocationRoot(ctx)
	store, err := config.StoreDir(m.ws.Root, m.cfg)
	if run == "" || err != nil {
		return freshProfiles{root: m.ws.Root}
	}
	sum := sha256.Sum256([]byte(run))
	return freshProfiles{root: m.ws.Root, file: filepath.Join(store, keptProfilesDir, fmt.Sprintf("fresh-%x.jsonl", sum[:8]))}
}

func (f freshProfiles) digest(c coverageSource) (string, bool) {
	data, err := os.ReadFile(filepath.Join(f.root, filepath.FromSlash(c.project), filepath.FromSlash(c.coverage.Profile)))
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), true
}

// vouches reports whether this run brought c's profile current and the
// profile on disk is still the one it left. The last line per profile wins.
func (f freshProfiles) vouches(c coverageSource) bool {
	if f.file == "" {
		return false
	}
	lines, err := file.ReadLines(f.file)
	if err != nil {
		return false
	}
	recorded := ""
	for _, line := range lines.Lines {
		var e freshProfile
		if json.Unmarshal(line.Text, &e) == nil && e.Project == c.project && e.Profile == c.coverage.Profile {
			recorded = e.Digest
		}
	}
	now, ok := f.digest(c)
	return ok && recorded == now
}

// record appends c's profile as this run left it, after removing the records
// of runs older than freshProfileLife. A profile the target did not write is
// not recorded; reading it says so.
func (f freshProfiles) record(c coverageSource) error {
	now, ok := f.digest(c)
	if f.file == "" || !ok {
		return nil
	}
	if err := pruneFreshProfiles(filepath.Dir(f.file), time.Now().Add(-freshProfileLife)); err != nil {
		return err
	}
	line, err := json.Marshal(freshProfile{Project: c.project, Profile: c.coverage.Profile, Digest: now})
	if err != nil {
		return err
	}
	return file.AppendLines(f.file, [][]byte{line}, 0o600)
}

// pruneFreshProfiles removes each fresh-profile record in dir last written
// before cutoff. An absent dir holds none.
func pruneFreshProfiles(dir string, cutoff time.Time) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !strings.HasPrefix(e.Name(), "fresh-") || !info.ModTime().Before(cutoff) {
			continue
		}
		errs = append(errs, os.Remove(filepath.Join(dir, e.Name())))
	}
	return errors.Join(errs...)
}
