// Package job persists the job store: the jobs an orchestrating agent declares
// about the plan it is running, kept where a human can read them.
//
// It gates no run and blocks no write to the tree; the one write it refuses is a write to
// a job the caller does not own (see [job.AuthorizeRow]). One plan per REPOSITORY, so every
// worktree and clone reads the same jobs.
//
// The row store and its write pipeline are libs/safetyscissors/job's: this package is
// magus's layer on top of it, the checkouts, grading, validation, sweep, landings, merges
// and outcomes, wired in as the module store's options (see [NewStore]).
//
// The INTENT layer of three, flat stores joined by job id at render time rather than a
// hierarchy: intent is this package, actions are internal/trail, and effects are the run
// itself. internal/journal (one invocation's events) and internal/notes (prose for a
// later reader) model no leased work and are not siblings.
package job

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// Store is the repository's lease ledger: the module's row store ([job.Store]) in the
// per-repository state directory, with magus's checkouts, grading and sweep wired into it.
// Every operation reads the file, acts, and writes what changed, so a Store is cheap to
// construct and holds no state between calls beyond its seams and its locks.
//
// The server builds exactly one and hands it to both of its doors, since the embedded
// store's mutex serializes writers within one process only while they share it.
type Store struct {
	*job.Store
	storeConfig
}

// storeConfig is everything a Store carries but the module store, so a view
// ([Store.As], [Store.InWorkspace], [Store.dry]) copies it whole, sets only what differs,
// and opens the module store over it again.
type storeConfig struct {
	path string
	// err is the failure to work out where this ledger lives, kept so NewStore can stay
	// a constructor while every operation still reports it.
	err  error
	root string
	// actor pins who this Store writes as; nil resolves the acting party at every write
	// from cacheDir and the environment. The server builds ONE Store at startup and serves
	// every MCP job caller from it, so an actor frozen at construction grades all of
	// them as whoever started the process.
	actor     *job.Actor
	cacheDir  string
	stateBase string
	// clock, staleAfter and notices are the sweep's seams (see [Store.sweep]); nil means
	// time.Now, the workspace's jobs.stale_after, and stderr.
	clock      func() time.Time
	staleAfter *time.Duration
	notices    io.Writer
	// retain is the retention window's seam; nil means the workspace's jobs.retain.
	retain *time.Duration
	// outputs is Location.Outputs, and activityBase Location.ActivityBase.
	outputs      func(root string) AttemptResolver
	activityBase func(checkoutRoot string) (string, error)
	// landed is the sweep's landing probe, nil for [Store.landedOnBase]; see [sweeper.landed].
	landed func(context.Context, job.Job) string
	// grader is Location.Grader.
	grader CheckGrader
	// dryRun is Location.DryRun.
	dryRun bool
	// build is Location.Build.
	build string
	// modelTiers and modelTierMatrix are Location's.
	modelTiers      *tier.Set
	modelRows       func() []model.Row
	modelTierMatrix func(context.Context, types.ModelTierRequest) (*tier.Assignment, error)
	spawnRule       func(parent, child tier.Tier) error
	spawnCeiling    func(context.Context) (builtin.Setting, error)
	owners          func(context.Context, []string) ([]string, error)
	// priceContext is Location.PriceContext.
	priceContext func(calls []types.AgentFacts) (tokens int64, usd float64, ok bool)
	// workspace is what validation reads the record against (see [Store.InWorkspace]);
	// nil names those checks unchecked.
	workspace *Workspace
	// migrations is [jobMigrations] outside a test.
	migrations []migrate.Migration
	// skew is Location.Skew. unfenced says once per Store, views included, that no skew
	// judged a write.
	skew     func(root string) error
	unfenced *sync.Once
}

// Location is where a Store lives: the repository whose rows these are, the state base
// that places the file, and the checkout's cache directory.
//
// A struct rather than positional params because the paths are transposable at every
// call site and nothing downstream would notice: a ledger written into the workspace and
// digested against the state dir reads as an ordinary empty job store.
type Location struct {
	// CacheDir is the workspace's cache directory (magus.CacheDir), where the checkout's
	// binding record names the acting lease (see [ActingActor]) and the guard records its
	// trail. The ledger itself never lives there.
	CacheDir string
	// Root is the checkout this Store was opened from. It answers two questions: which
	// REPOSITORY owns the rows (through vcs.StateDir, which folds every worktree and clone of
	// one repo onto a single directory), and what a row's paths are relative to when a
	// release is digested. A Store built with an empty root still records releases; it just
	// cannot say what was in them.
	Root string
	// StateBase is the user state directory the ledger and the checkout records live
	// under: xdg.StateDir for every caller outside a test, resolved by the caller. Empty
	// is an error every Store operation reports, and a checkout with no record.
	StateBase string
	// Actor is who the Store writes as. A pointer because the zero Actor is UNBOUND and
	// is a real value: nil is the only way to say "resolve it at every write, from the
	// environment and CacheDir through [ActingActor]", which is what every door outside a
	// test wants.
	Actor *job.Actor
	// Outputs resolves an output ref against the output store of the checkout at root.
	// Exit and Wait fall back to it when the caller's resolver finds no run, trying the
	// checkouts the job and its descendants were taken in. Nil tries the caller's
	// resolver alone.
	Outputs func(root string) AttemptResolver
	// ActivityBase resolves where the guard records the denials of a holder
	// in the checkout at checkoutRoot. Nil reads only CacheDir, so a holder
	// in another checkout has unknown denials.
	ActivityBase func(checkoutRoot string) (string, error)
	// Grader judges a declared check the way the guard will judge the row's holder running
	// it, for the fork doors, against the rules of the workspace at Root. Nil grades
	// nothing: the guard lives above this package, so only a door that can reach it
	// supplies one.
	Grader CheckGrader
	// DryRun makes [Store.List]'s sweep write nothing: a row it proves dead reads as
	// ended, and no row, notice, probe history or binding record is written. It governs
	// reads only; a write method still writes.
	DryRun bool
	// Build identifies the build whose results the footprint cache holds. Empty is the
	// running build ([buildinfo.Build]); a test names another.
	Build string
	// Clock is the store's clock for every time it stamps. Nil is time.Now; a fixture
	// holds it still so the rows it seeds share one declaration time.
	Clock func() time.Time
	// ModelTiers is the workspace's model tier declaration. With it, a write that creates a
	// row or changes its Model assigns the row's tier (see [resolveModelTier]); nil assigns
	// nothing, as does a Store a dry run reads.
	ModelTiers *tier.Set
	// ModelRows is the harness model rows (mgs_listModels). Nil leaves only the hosts to
	// resolve a model.
	ModelRows func() []model.Row
	// ModelTierMatrix is the function the workspace registered to choose a tier, or nil.
	// It runs inside the write, while the Store's locks are held, so it must not touch
	// the Store.
	ModelTierMatrix func(context.Context, types.ModelTierRequest) (*tier.Assignment, error)
	// SpawnRule is the workspace's magus.WithSpawnRule. It judges a row whose tier and its
	// parent's are both proven. Such a row with no rule is refused.
	SpawnRule func(parent, child tier.Tier) error
	// SpawnCeiling reads the workspace's spawn-above-parent-tier setting. A row SpawnRule
	// ranks above its parent is refused only under deny. Such a row with no reader is
	// refused.
	SpawnCeiling func(context.Context) (builtin.Setting, error)
	// Owners names the projects that own a list of files. It is asked only to fill a
	// matrix's request; nil leaves the request's WriteProjects empty.
	Owners func(ctx context.Context, files []string) ([]string, error)
	// PriceContext prices a job's holder's context when the job ends. Nil leaves every
	// outcome's tokens and cost unknown.
	PriceContext func(calls []types.AgentFacts) (tokens int64, usd float64, ok bool)
	// Skew refuses a write by this binary to the store of the checkout at root, before
	// anything is written: contract.RefuseSkew, which answers MGS1021 for a binary built
	// from another contract than root carries. It lives above this package, so each door
	// that can reach it fills it in. Nil fences nothing, and the Store's first write says
	// so on its notices.
	Skew func(root string) error
}

// NewStore returns the ledger for loc's repository. The file itself is created by the
// first write.
//
// THE CACHE DIRECTORY IS NOT THE HOME: a cache dir belongs to one CHECKOUT, and the rows
// describe a repository's plan, so they key on repository identity exactly as
// internal/sessions does, under vcs.StateDir.
//
// A resolution failure is held rather than returned: every operation reports it, so a
// caller cannot mistake an unplaceable ledger for an empty one.
func NewStore(loc Location) *Store {
	s := &Store{storeConfig: storeConfig{root: loc.Root, actor: loc.Actor, cacheDir: loc.CacheDir, stateBase: loc.StateBase, outputs: loc.Outputs, activityBase: loc.ActivityBase, grader: loc.Grader, dryRun: loc.DryRun, build: cmp.Or(loc.Build, buildinfo.Build()), clock: loc.Clock,
		modelTiers: loc.ModelTiers, modelRows: loc.ModelRows, modelTierMatrix: loc.ModelTierMatrix, spawnRule: loc.SpawnRule, spawnCeiling: loc.SpawnCeiling, owners: loc.Owners, priceContext: loc.PriceContext,
		migrations: jobMigrations(), skew: loc.Skew, unfenced: new(sync.Once)}}
	s.path, s.err = jobsPath(loc)
	return s.open()
}

// open sets s's module store over s's file, its seams reading s. A Store that names no
// file and no reason it has none fails every operation rather than open one beside the
// working directory.
func (s *Store) open() *Store {
	if s.path == "" && s.err == nil {
		s.Store = job.Unopened(errors.New("job: this store names no file: open it with NewStore"))
		return s
	}
	if s.err != nil {
		s.Store = job.Unopened(s.err)
		return s
	}
	opened, err := job.Open(filepath.Dir(s.path),
		job.WithRoot(s.root),
		job.WithActor(s.Actor),
		job.WithOrigin(func(ctx context.Context) job.Origin { return recorder.StampOrigin(ctx, job.Origin{}) }),
		job.WithDenials(s.holderDenials),
		job.WithWriteHook(s.writeHook),
		job.WithRetention(s.beforeWrite),
		job.WithSweep(s.sweep),
		job.WithNotices(func(n job.Notice) { fmt.Fprintf(s.noticeWriter(), "job: %s\n", n) }),
		job.WithClock(s.now),
		job.WithDeclarationDigest(s.declarationDigest),
		job.WithHead(s.committedRevision),
		job.WithMigrations(migrate.Store{Migrations: s.migrations}.Version(), s.migrate),
		job.WithCredentialKinds(credentialKinds()...),
		checkSpec(),
		job.WithErrorWrap(diagnose),
		job.WithTree(s.CheckoutToken),
		job.WithCheckCommand(s.checkCommand),
	)
	if err != nil {
		s.Store = job.Unopened(err)
		return s
	}
	s.Store = opened
	return s
}

// view is a Store over s's file with every seam s has and a mutex of its own; the file
// lock still serializes it against s and every other writer.
func (s *Store) view() *Store {
	return (&Store{storeConfig: s.storeConfig}).open()
}

// withMigrations is s reading and writing through migrations instead of [jobMigrations],
// for a test that plants a store at another migration.
func (s *Store) withMigrations(migrations []migrate.Migration) *Store {
	v := &Store{storeConfig: s.storeConfig}
	v.migrations = migrations
	return v.open()
}

// Actor is who this Store writes as RIGHT NOW, for a door that has to refuse before it
// computes anything: `ledger accept` grades a report and must say a worker cannot grade
// its own row before it reads one. Resolved per call unless Location pinned one, so a
// checkout that binds a lease is graded from its next write.
func (s *Store) Actor() job.Actor {
	if s.actor != nil {
		return *s.actor
	}
	return ActingActor(s.place())
}

// place is the part of s's Location that keys the checkout record.
func (s *Store) place() Location {
	return Location{CacheDir: s.cacheDir, StateBase: s.stateBase}
}

// noticeWriter is where s's notices go: Location's seam, else stderr.
func (s *Store) noticeWriter() io.Writer {
	if s.notices != nil {
		return s.notices
	}
	return os.Stderr
}

// now is the Store's clock.
func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// jobsPath places the jobs file: <XDG state>/magus/jobs/<repo>/jobs.jsonl.
func jobsPath(loc Location) (string, error) {
	if loc.StateBase == "" {
		return "", errors.New("job: no user state dir: the caller passes Location.StateBase")
	}
	dir, err := vcs.StateDir(loc.StateBase, "jobs", loc.Root)
	if err != nil {
		return "", fmt.Errorf("job: %w", err)
	}
	return filepath.Join(dir, jobsFileName), nil
}

// credentialKinds are the kinds of credential an origin magus records may carry.
func credentialKinds() []job.CredentialKind {
	return []job.CredentialKind{auth.KindOperator, auth.KindStored, auth.KindShare, auth.KindExchange, auth.KindStdio, auth.KindSocketPeer}
}

// jobsFileName and attemptsFileName are the module store's files, named here for the
// migrations and the refusals that quote them.
const (
	jobsFileName     = "jobs.jsonl"
	attemptsFileName = "attempts.jsonl"
)

// diagnose is the module store's refusal with magus's code and remedy: the codes a
// person looks up and the commands that get them past it.
func diagnose(err error) error {
	if err == nil {
		return nil
	}
	if coded, ok := errors.AsType[*types.DiagnosticError](err); ok && coded != nil {
		return err
	}
	if refused, ok := errors.AsType[*job.RefusedError](err); ok && refused.Actor.Unstamped {
		refused.Remedy = unstampedRemedy
		return err
	}
	if newer, ok := errors.AsType[*job.NewerWriterError](err); ok {
		if len(newer.Lacks) > 0 {
			return types.WrapDiagnostic(types.JobStoreWriterOlder, err, "%s requires %s, which this magus (%s) lacks, update magus",
				newer.Path, quoteAll(newer.Lacks), job.APIVersion)
		}
		return types.WrapDiagnostic(types.JobStoreWriterOlder, err, "%v: update magus", err)
	}
	if newer, ok := errors.AsType[*job.NewerRecordsError](err); ok {
		if len(newer.Lacks) > 0 {
			return types.WrapDiagnostic(types.StoreRecordsNewer, err, "%s requires %s, which this magus (%s) lacks: update magus",
				newer.Record, quoteAll(newer.Lacks), job.APIVersion)
		}
		return types.WrapDiagnostic(types.StoreRecordsNewer, err, "%s: %s is %s %s, and this magus reads %s %s: update magus, or %s",
			newer.Path, newer.Record, newer.Got.APIVersion, newer.Got.Kind, newer.Want.APIVersion, newer.Want.Kind, staleStoreFix(newer.Path))
	}
	if version, ok := errors.AsType[*job.RecordVersionError](err); ok {
		return refuseRecordVersion(err, version.Path, version.Line, version.Want, version.Got)
	}
	if legacy, ok := errors.AsType[*job.LegacyDocumentError](err); ok {
		return fmt.Errorf("cannot open job plan %s, and once no older magus writes it, set it aside with `mv %s %s.old`: %w",
			legacy.Path, shellQuote(legacy.Document), shellQuote(legacy.Document), file.StoreDiagnostic(legacy.Err))
	}
	if refused, ok := errors.AsType[*job.GrantRefusedError](err); ok {
		return fmt.Errorf("job: a grant is a person's keystroke, and this process acts under lease %q: a person types `%s job grant` at their own terminal", refused.Lease, hint.BinaryName())
	}
	if errors.Is(err, job.ErrUnknownJob) {
		return fmt.Errorf("%w: `%s` lists the declared ids", err, hint.LsJobs)
	}
	return file.StoreDiagnostic(err)
}

// refuseRecordVersion refuses a stored line at version got, which this magus does not read:
// a newer version of its own group waits for an update, and any other is foreign.
func refuseRecordVersion(err error, path string, line int, want, got apiversion.APIVersion) error {
	if errors.Is(want.Refuse(got), apiversion.ErrNewer) {
		return types.WrapDiagnostic(types.StoreRecordsNewer, err, "%s:%d is %s, newer than this magus reads (%s): update magus",
			path, line, got, want)
	}
	return types.WrapDiagnostic(types.JobStoreRecordForeign, err,
		"%s:%d is %s, which this magus (%s) does not read and no migration reaches: move %s aside and fork the job again, or start a fresh session",
		path, line, got, want, shellQuote(path))
}

// readOnly refuses a write to row when it requires a feature this magus lacks, with
// magus's code; see [job.Unwritable].
func readOnly(row job.Job) error { return diagnose(job.Unwritable(row)) }

// shellQuote is p as one POSIX shell word. A copy: internal/guard's shellWord sits in
// a package that imports this one, and cmd/magus's in package main.
func shellQuote(p string) string {
	if p != "" && strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+@:,") == "" {
		return p
	}
	return fmt.Sprintf("'%s'", strings.ReplaceAll(p, "'", `'\''`))
}

// holderDenials are the guard's denials of the holder of job id since since, read from
// the trail of the checkout it took the job in. A dry run reads none.
func (s *Store) holderDenials(ctx context.Context, holderCheckout, id string, since int64) ([]job.Denial, bool, error) {
	if s.dryRun {
		return nil, false, nil
	}
	base, err := s.holderTrail(holderCheckout)
	if err != nil {
		return nil, false, err
	}
	return recorder.LeaseDenials(ctx, base, id, since, maxDenialsRead)
}

// holderTrail is the trail base of the checkout a holder took its job in: this Store's own
// cache dir when that is this checkout or none, else what Location.TrailBase resolves. The
// guard records a holder's denials in the trail of the checkout it works in, so neither the
// forker's nor the caller's trail holds them.
func (s *Store) holderTrail(checkoutRoot string) (string, error) {
	if checkoutRoot == "" {
		return s.cacheDir, nil
	}
	if s.root != "" {
		if here, err := filepath.Abs(s.root); err == nil && here == checkoutRoot {
			return s.cacheDir, nil
		}
	}
	if s.activityBase == nil {
		return "", fmt.Errorf("job: nothing resolves the activity log of the checkout at %s", checkoutRoot)
	}
	if _, err := os.Stat(checkoutRoot); err != nil {
		return "", fmt.Errorf("job: the checkout holding the activity log is gone: %w", err)
	}
	return s.activityBase(checkoutRoot)
}

// endsOwnRow reports whether this store's caller holds row, so ending it is its holder's.
func (s *Store) endsOwnRow(row job.Job) bool {
	return s.Actor().Lease == row.ID || (row.CheckoutRoot != "" && row.CheckoutRoot == s.root)
}

// writeHook is magus's part of every row write: the rules only magus knows, the fold of an
// ended row's attempt, the end reasons, the model tier and the outcome a row that just
// ended records. It runs inside the write, the module store's locks held.
func (s *Store) writeHook(ctx context.Context, e *job.WriteEvent) error {
	switch e.Stage {
	case job.StageAuthorize:
		if e.Kind == job.AsClaim {
			return authorizeClaim(e.Actor, e.ID, e.Prev, *e.Row, e.Exists, e.Rows)
		}
		return authorizeGate(e.Actor, e.ID, *e.Row, e.Exists, e.Rows)
	case job.StageRefork:
		if err := refuseAttempt(e.ID, e.Attempts.LiveN(e.ID), *e.Row); err != nil {
			return err
		}
		fold := foldAttempt(e.Prev, e.Attempts.Live(e.ID), e.Attempts.Prior(e.ID), e.Denials, e.DenialsKnown, e.Now)
		e.Fold = &fold
	case job.StageEnd:
		switch {
		case e.Row.State == job.StateNoReturn:
			e.Row.EndReason = endedReason(e.Prev, e.Attempts.Result(e.ID) != nil, s.endsOwnRow(e.Prev))
		case e.Row.State == job.StatePass && gradedByHolder(e.Prev):
			e.Row.EndReason = returnedReason
		}
	case job.StageTier:
		return s.assignTier(ctx, e)
	case job.StagePlaced:
		if !e.Exists || e.Prev.State.Terminal() || !e.Row.State.Terminal() {
			return nil
		}
		var ended endings
		if err := ended.hold(ctx, s, e.Rows, *e.Row); err != nil {
			return err
		}
		e.After = append(e.After, func(ctx context.Context) error { return ended.record(ctx, time.Now().UnixMilli()) })
	case job.StageDelete:
		row := e.Row
		if !row.Worktree.Standing() || !row.Worktree.Made {
			return nil
		}
		here, _ := filepath.Abs(s.root)
		if kept := endCheckout(ctx, e.Rows, here, *row, *row.Worktree, nil); kept != "" {
			return fmt.Errorf("job: %s owns the checkout %s, which stays, so the row stays with it: %s",
				e.ID, row.Worktree.Root, kept)
		}
		*row = row.Clone()
		row.Worktree.Removed = time.Now().Unix()
	}
	return nil
}

// assignTier gives a row whose requested model changed its tier, and refuses one above
// its parent's.
func (s *Store) assignTier(ctx context.Context, e *job.WriteEvent) error {
	row, prev := e.Row, e.Prev
	if s.modelTiers != nil && (!e.Exists || prev.Agent.RequestedModel() != row.Agent.RequestedModel()) {
		assigned, err := s.assignModelTierWithHistory(ctx, *row, e.Rows)
		if err != nil {
			return err
		}
		row.ModelTier = &assigned
	}
	if !e.Exists || prev.Agent.RequestedModel() != row.Agent.RequestedModel() || prev.Parent != row.Parent {
		return refuseAboveParent(ctx, s.modelTiers, s.spawnRule, s.spawnCeiling, *row, e.Rows)
	}
	return nil
}

// pruneFootprints removes the footprint cache of every checkpoint revision no live row in
// rows declares, riding the retention pass. Best effort, as for the archives it drops.
func (s *Store) pruneFootprints(rows []job.Job) {
	dir := s.footprintCache().dir
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	named := map[string]bool{}
	for _, r := range rows {
		if rev := checkpointRevision(r.Checkpoint); rev != "" && !over(r) {
			named[revisionDir(rev)] = true
		}
	}
	for _, e := range entries {
		if !named[e.Name()] {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// migrate brings the file to the version of the last of s.migrations under the module
// store's lock. A file past them is refused with MGS3051, a headerless file whose rows are
// enveloped as damaged, and a migration from a binary the base does not contain as off
// the base; each refusal names the store's last writer. lines are the file's row lines,
// and the header, which says how far the file has migrated, is read from the file itself.
func (s *Store) migrate(ctx context.Context, storePath string, _ apiversion.APIVersion, lines []codec.Line) error {
	read, err := codec.ReadLines(storePath)
	if err != nil {
		return fmt.Errorf("job: %w", err)
	}
	header, headed := storeHeader(read.Lines)
	last := headerWriter(header)
	writer := runningWriter(ctx)
	st := migrate.Store{
		Name:       "job store",
		Path:       storePath,
		Header:     headerEntry,
		Migrations: s.migrations,
		Stamp:      func(e migrate.Entry) (migrate.Entry, error) { return stampHeader(e, writer) },
	}
	refuse := func(err error) error {
		if newer, ok := errors.AsType[*migrate.NewerError](err); ok {
			return types.DiagnosticErrorf(types.JobStoreWriterOlder, "%s%s", newer.Error(), last.named())
		}
		return namePath(err, storePath)
	}
	found, err := st.At(header)
	if err != nil {
		return refuse(err)
	}
	due := found < len(s.migrations) || st.Numbered(header)
	if due {
		if err := s.fence(); err != nil {
			return err
		}
	}
	if !headed {
		if err := refuseHeaderless(storePath, lines); err != nil {
			return err
		}
	}
	if err := s.refuseOffBase(ctx, storePath, storeAt(st, header, found), due, last); err != nil {
		return err
	}
	if i := slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == triesMoved }); i >= found {
		if err := s.moveTries(ctx); err != nil {
			return fmt.Errorf("moving tries to %s: %w", attemptsFileName, err)
		}
	}
	if i := slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == recordsEnveloped }); i >= found {
		if err := s.envelopeAttempts(ctx); err != nil {
			return fmt.Errorf("enveloping %s: %w", attemptsFileName, err)
		}
	}
	if i := slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == groupsMoved }); i >= found {
		if err := s.regroupAttempts(ctx); err != nil {
			return fmt.Errorf("regrouping %s: %w", attemptsFileName, err)
		}
	}
	if i := slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == keysSnaked }); i >= found {
		if err := s.snakeAttempts(ctx); err != nil {
			return fmt.Errorf("respelling %s in snake_case: %w", attemptsFileName, err)
		}
	}
	res, err := st.Apply()
	if err != nil {
		return refuse(err)
	}
	if res.Migrated() {
		fmt.Fprintf(s.noticeWriter(), "job: %s\n", res)
	}
	return nil
}

// writerMember is the header member naming the build that last wrote the job store.
const writerMember = "writer"

// storeWriter is a build that writes the job store, as the header's writer member
// records it.
type storeWriter struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

func (w storeWriter) String() string {
	return fmt.Sprintf("magus %s (commit %.12s)", cmp.Or(w.Version, buildinfo.UnknownVersion), cmp.Or(w.Commit, "unknown"))
}

// named is the clause a refusal appends to name w, the store's last writer: a header
// with no writer member was last written by a magus that stamped none.
func (w storeWriter) named() string {
	if w == (storeWriter{}) {
		return " (its header names no writer, so a magus from before writers were stamped wrote it last)"
	}
	return fmt.Sprintf(" (%s wrote it last)", w)
}

// runningBuild is this binary's build identity: what ctx carries, else [buildinfo.Self].
// Every door that opens a store reads the binary's own stamp this way, whether or not
// its ctx descends from the one main stamped.
func runningBuild(ctx context.Context) types.MagusBuild {
	b := types.MagusBuildFromContext(ctx)
	if b == (types.MagusBuild{}) {
		b = buildinfo.Self()
	}
	b.Version = cmp.Or(b.Version, buildinfo.UnknownVersion)
	b.Commit = cmp.Or(b.Commit, buildinfo.UnknownVersion)
	return b
}

// runningWriter is this binary as the header records its writer.
func runningWriter(ctx context.Context) storeWriter {
	b := runningBuild(ctx)
	return storeWriter{Version: b.Version, Commit: b.Commit}
}

// stampHeader is the header entry e naming w as its writer.
func stampHeader(e migrate.Entry, w storeWriter) (migrate.Entry, error) {
	raw, err := codec.Marshal(w)
	if err != nil {
		return nil, fmt.Errorf("job: stamping the writer: %w", err)
	}
	e[writerMember] = raw
	return e, nil
}

// storeHeader is the header among lines, and whether there is one.
func storeHeader(lines []codec.Line) (migrate.Entry, bool) {
	for _, line := range lines {
		if e, err := decodeEntry(line.Text); err == nil && headerEntry(e) {
			return e, true
		}
	}
	return nil, false
}

// headerWriter is the writer header names, zero when it names none or its writer member
// does not decode.
func headerWriter(header migrate.Entry) storeWriter {
	var w storeWriter
	if raw, ok := header[writerMember]; ok && codec.Unmarshal(raw, &w) == nil {
		return w
	}
	return storeWriter{}
}

// beforeWrite runs inside every rewrite of the job store before it writes: it refuses a
// binary stale against the checkout, stamps this binary as the store's writer and the
// migration count an older magus reads, then runs the retention pass.
func (s *Store) beforeWrite(ctx context.Context, f *job.Rows, now time.Time) error {
	if err := s.fence(); err != nil {
		return err
	}
	raw, err := codec.Marshal(runningWriter(ctx))
	if err != nil {
		return fmt.Errorf("job: stamping the writer: %w", err)
	}
	*f = f.WithMember(writerMember, raw)
	// compat: see migrate.Store.Count.
	*f = f.WithMember(migrate.Store{Migrations: s.migrations}.Count())
	return s.retainDue(ctx, f, now)
}

// fence refuses a write from a binary that Location.Skew judges stale against s's
// checkout. With no Skew it lets the write through and says so once.
func (s *Store) fence() error {
	if s.skew == nil {
		say := func() {
			fmt.Fprintf(s.noticeWriter(), "job: this magus fills no contract check into its job store, so nothing judged it against the contract %s carries before it wrote %s\n", cmp.Or(s.root, "the working directory"), s.path)
		}
		if s.unfenced == nil {
			say()
		} else {
			s.unfenced.Do(say)
		}
		return nil
	}
	if err := s.skew(s.root); err != nil {
		return &staleWriterError{Path: s.path, Root: s.root, Err: err}
	}
	return nil
}

// staleWriterError refuses a job store write from a binary built from another contract
// than its checkout carries (MGS1021, which Err carries). Every checkout of the
// repository shares the store, so a stale binary would write rows the current one reads
// differently. Nothing was written.
type staleWriterError struct {
	Path, Root string
	Err        error
}

func (e *staleWriterError) Error() string {
	return fmt.Sprintf("job: job store %s was not written, because this magus is stale against %s: %v", e.Path, cmp.Or(e.Root, "the working directory"), e.Err)
}

func (e *staleWriterError) Unwrap() error { return e.Err }

// headerlessStoreError is a job store with no header line whose rows are enveloped. Only
// a store past [recordsEnveloped] holds such rows, and that store's header says so, so
// the header was lost: replaying every migration from 0 would guess at what the rows
// are. Nothing was read or written.
type headerlessStoreError struct {
	Path string
	// Line is the first enveloped row's line.
	Line int
}

func (e *headerlessStoreError) Error() string {
	return fmt.Sprintf("job: job store %s has no header line, and line %d is an enveloped row, which only a store at %s or later holds:"+
		" its header was lost, and replaying every migration from the first over these rows would guess at what wrote them, so nothing was read or written:"+
		" move it aside with `mv %s %s.damaged` and fork the jobs again",
		e.Path, e.Line, envelopedAt(), shellQuote(e.Path), shellQuote(e.Path))
}

// envelopedAt is the version a store's rows are enveloped from.
func envelopedAt() apiversion.APIVersion {
	migrations := jobMigrations()
	return migrations[slices.IndexFunc(migrations, func(m migrate.Migration) bool { return m.Name == recordsEnveloped })].To
}

// refuseHeaderless refuses a file at path with no header whose row lines hold an
// enveloped row; see [headerlessStoreError].
func refuseHeaderless(path string, lines []codec.Line) error {
	for _, line := range lines {
		e, err := decodeEntry(line.Text)
		if err != nil {
			continue
		}
		// An enveloped row carries a spec; a flat one carries its id at the top.
		if _, ok := e["spec"]; ok {
			return &headerlessStoreError{Path: path, Line: line.N}
		}
	}
	return nil
}

// offBaseMigrationError refuses a forward migration of the job store from a binary not
// built on a commit the workspace's base contains. Every checkout and branch of the
// repository shares the store, so once a branch binary migrated it every binary built on
// the base would refuse it as newer. Nothing was read or written.
type offBaseMigrationError struct {
	Path string
	// Found is how far the store has migrated, as [storeAt] names it, and Known the
	// version this build writes.
	Found string
	Known apiversion.APIVersion
	Build types.MagusBuild
	// Base is the base this build was judged against, "" when none resolved.
	Base string
	// Why says how the build fails to be on Base.
	Why string
	// Last is the store's last writer.
	Last storeWriter
	// Foreign means the workspace's history holds no commit that built this binary, so
	// the workspace is not magus's source and the binary's stamp alone judged it.
	Foreign bool
}

func (e *offBaseMigrationError) Error() string {
	base := cmp.Or(e.Base, "the base")
	self := storeWriter{Version: e.Build.Version, Commit: e.Build.Commit}.String()
	if e.Build.Source == types.BuildSourceVCS {
		self += ", read from Go's VCS stamp because it carries no magus version stamp"
	}
	if e.Foreign {
		return fmt.Sprintf("job: job store %s is at %s, and this magus (%s) writes %s, but %s:"+
			" a binary this workspace's history did not build migrates only on a clean magus version stamp, so nothing was read or written%s:"+
			" install a magus release, build magus with `./magus run go-build .` in a clean magus checkout, or run this binary under its own XDG_STATE_HOME",
			e.Path, e.Found, self, e.Known, e.Why, e.Last.named())
	}
	return fmt.Sprintf("job: job store %s is at %s, and this magus (%s) writes %s, but %s:"+
		" every checkout and branch shares the store, and once migrated every magus built on %s refuses it as newer, so nothing was read or written%s:"+
		" rebuild magus from %s, or run this binary under its own XDG_STATE_HOME",
		e.Path, e.Found, self, e.Known, e.Why, base, e.Last.named(), base)
}

// storeAt names how far a store whose header is header, found of st's migrations along,
// has migrated.
func storeAt(st migrate.Store, header migrate.Entry, found int) string {
	switch {
	// compat: see migrate.Store.Count.
	case st.Numbered(header):
		return fmt.Sprintf("migration %d", found)
	case found == 0:
		return "no api_version"
	}
	return string(st.Migrations[found-1].To)
}

// refuseOffBase refuses rewriting the file at path, at found as [storeAt] names it, when
// a migration is due, unless this binary was built on a commit the base of s's
// repository contains. A repository that holds no such commit is not magus's source, so
// no base there can judge the binary: it migrates only on a clean magus version stamp,
// and a notice says so. A root no version control claims has no base: it migrates, and a
// notice says nothing judged it.
func (s *Store) refuseOffBase(ctx context.Context, path, found string, due bool, last storeWriter) error {
	if !due {
		return nil
	}
	build := runningBuild(ctx)
	known := migrate.Store{Migrations: s.migrations}.Version()
	refused := func(base, why string, args ...any) error {
		return &offBaseMigrationError{Path: path, Found: found, Known: known, Build: build, Base: base, Why: fmt.Sprintf(why, args...), Last: last}
	}
	root := s.root
	if root == "" {
		// jobsPath keyed a store with no root on the working directory.
		wd, err := os.Getwd()
		if err != nil {
			return refused("", "with no root and no working directory nothing names a base to judge it against (%v)", err)
		}
		root = wd
	}
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil {
		return refused("", "the version control of %s does not resolve (%v)", root, err)
	}
	if res.VCS == nil || (res.Source == types.VCSSourceDefault && !underVersionControl(ctx, res.VCS, root)) {
		fmt.Fprintf(s.noticeWriter(), "job: no version control holds %s, so no base judged this magus before it migrated job store %s\n", root, path)
		return nil
	}
	if foreign(ctx, res.VCS, root, build) {
		if why, refuse := stampRefusal(root, build); refuse {
			return &offBaseMigrationError{Path: path, Found: found, Known: known, Build: build, Why: why, Last: last, Foreign: true}
		}
		fmt.Fprintf(s.noticeWriter(), "job: %s holds no commit %.12s, so no base judged this magus; its clean stamp %s let it migrate job store %s\n", root, build.Commit, build.Version, path)
		return nil
	}
	switch {
	case res.Base == "":
		return refused("", "%s names no base branch", res.Name)
	case build.Source == types.BuildSourceNone || build.Commit == buildinfo.UnknownVersion:
		return refused(res.Base, "this binary's build identity could not be read: it carries no magus version stamp and no Go VCS stamp, so nothing says %s contains it;"+
			" build magus with `./magus run go-build .` or `./magus buzz hack/dev/bootstrap-checkout.buzz -- --here`", res.Base)
	case build.Dirty && build.Source == types.BuildSourceVCS:
		return refused(res.Base, "this binary carries no magus version stamp, and Go's VCS stamp says it was built from a modified tree;"+
			" that stamp can name another revision and a modified tree in a linked git worktree, so build magus with `./magus run go-build .` to stamp it")
	case build.Dirty:
		return refused(res.Base, "this binary was built from a modified tree (its version %s says so), and no commit holds what it was built from", build.Version)
	}
	base, err := res.VCS.FindCommit(ctx, root, res.Base)
	if err != nil {
		return refused(res.Base, "%s does not resolve in %s (%v)", res.Base, root, err)
	}
	contained, err := res.VCS.IsAncestor(ctx, root, build.Commit, base.ID)
	switch {
	case err != nil:
		return refused(res.Base, "%s cannot say whether %s at %s contains commit %.12s (%v)", res.Name, res.Base, shortRevision(base), build.Commit, err)
	case !contained:
		return refused(res.Base, "%s at %s does not contain commit %.12s, which built it", res.Base, shortRevision(base), build.Commit)
	}
	if build.Source == types.BuildSourceVCS {
		fmt.Fprintf(s.noticeWriter(), "job: this magus carries no magus version stamp, so its build commit %.12s was read from Go's VCS stamp before it migrated job store %s\n", build.Commit, path)
	}
	return nil
}

// foreign reports whether driver finds root's current commit but not the one build
// names: the workspace answers, and its history did not build this binary. A build with
// no commit to look up is never foreign; the base judges it, and refuses it.
func foreign(ctx context.Context, driver types.VCSDriver, root string, build types.MagusBuild) bool {
	if build.Source == types.BuildSourceNone || build.Commit == buildinfo.UnknownVersion || !underVersionControl(ctx, driver, root) {
		return false
	}
	_, err := driver.FindCommit(ctx, root, build.Commit)
	return err != nil
}

// stampRefusal says why build may not migrate the job store of a workspace at root that
// holds none of its history, and false when go-build stamped it from a clean tree, as a
// release is.
func stampRefusal(root string, build types.MagusBuild) (string, bool) {
	held := fmt.Sprintf("%s holds no commit %.12s, so it is not magus's source and no base there can judge this binary", root, build.Commit)
	switch {
	case build.Dirty:
		return held + ", and it was built from a modified tree", true
	case build.Source != types.BuildSourceStamp:
		return held + ", and it carries no magus version stamp: Go's VCS stamp can name another revision", true
	}
	return "", false
}

// underVersionControl reports whether driver finds a commit at root, for a root no
// version control claimed, which driver only defaults to.
func underVersionControl(ctx context.Context, driver types.VCSDriver, root string) bool {
	_, err := driver.FindCommit(ctx, root, "")
	return err == nil
}

// declarationDigest digests the lines one declaration of the file at p spans in body, as
// the footprint places them, so the next holder inherits that body rather than a file
// other claims on it keep changing. A declaration no line is placed in any more is absent.
func (s *Store) declarationDigest(ctx context.Context, p, decl string, body []byte) string {
	res, err := vcs.Resolve(ctx, s.root, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return job.DigestUnreadable
	}
	regions, err := res.VCS.RegionsBetween(ctx, s.root, path.Clean(p), nil, body)
	if err != nil {
		return job.DigestUnreadable
	}
	var spans [][2]int
	for _, r := range regions {
		if job.NamesDeclaration(decl, r.Declaration) {
			spans = append(spans, [2]int{r.Lines[0], r.Lines[1]})
		}
	}
	if spans == nil {
		return job.DigestAbsent
	}
	return digestSpans(body, spans)
}

// digestSpans is the digest of the lines of body that spans name, 1-based and inclusive,
// in order.
func digestSpans(body []byte, spans [][2]int) string {
	lines := strings.SplitAfter(string(body), "\n")
	var spanned strings.Builder
	for _, r := range spans {
		for n := r[0]; n <= r[1] && n-1 < len(lines); n++ {
			spanned.WriteString(lines[n-1])
		}
	}
	sum := sha256.Sum256([]byte(spanned.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// checkCommand is the argv a witnessed run of postcondition's check carries, run in this store's
// checkout: [checkArgv].
func (s *Store) checkCommand(_ job.Job, postcondition job.Postcondition) ([]string, string, error) {
	if s.root == "" {
		return nil, "", errors.New("this job store has no checkout to run a check in")
	}
	c, err := PostconditionCheck(postcondition)
	if err != nil {
		return nil, "", err
	}
	return checkArgv(c), s.root, nil
}

// quoteAll is names each quoted, joined by commas.
func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}
