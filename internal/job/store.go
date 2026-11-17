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
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/internal/trail"
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
	// outputs is Location.Outputs, and trailBase Location.TrailBase.
	outputs   func(root string) AttemptResolver
	trailBase func(checkoutRoot string) (string, error)
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
	// TrailBase resolves the activity trail base of the checkout at checkoutRoot, the cache
	// dir the guard records a holder's denials in. Nil reads only this Store's own
	// CacheDir, and the denials of a holder in another checkout are unknown.
	TrailBase func(checkoutRoot string) (string, error)
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
	s := &Store{storeConfig: storeConfig{root: loc.Root, actor: loc.Actor, cacheDir: loc.CacheDir, stateBase: loc.StateBase, outputs: loc.Outputs, trailBase: loc.TrailBase, grader: loc.Grader, dryRun: loc.DryRun, build: cmp.Or(loc.Build, buildinfo.Build()),
		modelTiers: loc.ModelTiers, modelRows: loc.ModelRows, modelTierMatrix: loc.ModelTierMatrix, spawnRule: loc.SpawnRule, spawnCeiling: loc.SpawnCeiling, owners: loc.Owners, priceContext: loc.PriceContext,
		migrations: jobMigrations()}}
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
		job.WithOrigin(func(ctx context.Context) job.Origin { return trail.StampOrigin(ctx, job.Origin{}) }),
		job.WithDenials(s.holderDenials),
		job.WithWriteHook(s.writeHook),
		job.WithRetention(s.retainDue),
		job.WithSweep(s.sweep),
		job.WithNotices(func(n job.Notice) { fmt.Fprintf(s.noticeWriter(), "job: %s\n", n) }),
		job.WithClock(s.now),
		job.WithDeclarationDigest(s.declarationDigest),
		job.WithHead(s.committedRevision),
		job.WithMigrations(len(s.migrations), s.migrate),
		job.WithCredentialKinds(credentialKinds()...),
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
		return types.RefuseVersion(types.APIVersion(version.Want), types.APIVersion(version.Got),
			fmt.Sprintf("job: %s:%d", version.Path, version.Line), staleStoreFix(version.Path))
	}
	if legacy, ok := errors.AsType[*job.LegacyDocumentError](err); ok {
		return fmt.Errorf("cannot open job plan %s: %w: once no older magus writes it, set it aside with `mv %s %s.old`",
			legacy.Path, file.StoreDiagnostic(legacy.Err), shellQuote(legacy.Document), shellQuote(legacy.Document))
	}
	if refused, ok := errors.AsType[*job.GrantRefusedError](err); ok {
		return fmt.Errorf("job: a grant is a person's keystroke, and this process acts under lease %q: a person types `%s job grant` at their own terminal", refused.Lease, hint.BinaryName())
	}
	if errors.Is(err, job.ErrUnknownJob) {
		return fmt.Errorf("%w, `%s` lists the declared ids", err, hint.LsJobs)
	}
	return file.StoreDiagnostic(err)
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
	return trail.LeaseDenials(ctx, base, id, since, maxDenialsRead)
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
	if s.trailBase == nil {
		return "", fmt.Errorf("job: nothing resolves the trail of the checkout at %s", checkoutRoot)
	}
	if _, err := os.Stat(checkoutRoot); err != nil {
		return "", fmt.Errorf("job: the checkout holding the trail is gone: %w", err)
	}
	return s.trailBase(checkoutRoot)
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

// migrate brings the file at migration found to the last of s.migrations under the
// module store's lock, refusing a job's checkout. A file past them is refused with MGS3051.
func (s *Store) migrate(ctx context.Context, storePath string, found int, lines []codec.Line) error {
	if err := s.refuseCheckoutMigration(found, lines); err != nil {
		return err
	}
	st := migrate.Store{
		Name:       "job store",
		Path:       storePath,
		Header:     headerEntry,
		Migrations: s.migrations,
	}
	refuse := func(err error) error {
		if newer, ok := errors.AsType[*migrate.NewerError](err); ok {
			return types.DiagnosticErrorf(types.JobStoreWriterOlder, "%s", newer.Error())
		}
		return namePath(err, storePath)
	}
	if err := st.Check(found); err != nil {
		return refuse(err)
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
	res, err := st.Apply()
	if err != nil {
		return refuse(err)
	}
	if res.Migrated() {
		fmt.Fprintf(s.noticeWriter(), "job: %s\n", res)
	}
	return nil
}

// refuseCheckoutMigration refuses migrating a file at migration found from
// a live job's own checkout. Every checkout shares the file. A row only
// taken here does not count: the root's own checkout is one.
func (s *Store) refuseCheckoutMigration(found int, lines []codec.Line) error {
	if found >= len(s.migrations) {
		return nil
	}
	checkout := s.root
	if checkout == "" {
		// jobsPath keyed a store with no root on the working directory.
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("job: job store %s is behind this magus, and with no root and no working directory nothing says this is not a job's checkout, so it was not migrated: %w", s.path, err)
		}
		checkout = wd
	}
	rows, err := s.unmigratedRows(found, lines)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.State.Live() || !row.Worktree.Standing() || !within(checkout, row.Worktree.Root) {
			continue
		}
		return fmt.Errorf("job: job store %s is at migration %d, and this magus writes migration %d from %s, the checkout of live job %s:"+
			" a job's checkout never migrates the store every checkout shares, so nothing was read or written,"+
			" and the root integrates this change and migrates the store once, from its own checkout",
			s.path, found, len(s.migrations), row.Worktree.Root, row.ID)
	}
	return nil
}

// unmigratedRows reads the rows of a file at migration found as far as a
// refusal needs them. Before [recordsEnveloped] a row's line was a
// job.Job.
func (s *Store) unmigratedRows(found int, lines []codec.Line) ([]job.Job, error) {
	flat := found <= slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == recordsEnveloped })
	var rows []job.Job
	at := map[string]int{}
	for _, line := range lines {
		var row job.Job
		if flat {
			if err := codec.Unmarshal(line.Text, &row); err != nil {
				return nil, types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
			}
		} else {
			var err error
			if row, _, err = job.ReadRecord(line.Text, credentialKinds()...); err != nil {
				return nil, types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
			}
		}
		switch i, seen := at[row.ID]; {
		case row.ID == "":
		case seen:
			rows[i] = row
		default:
			at[row.ID] = len(rows)
			rows = append(rows, row)
		}
	}
	return rows, nil
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

// checkCommand is the argv a witnessed run of goal's check carries, run in this store's
// checkout: [checkArgv].
func (s *Store) checkCommand(_ job.Job, goal job.Goal) ([]string, string, error) {
	if s.root == "" {
		return nil, "", errors.New("this job store has no checkout to run a check in")
	}
	return checkArgv(goal.Check), s.root, nil
}

// quoteAll is names each quoted, joined by commas.
func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}
