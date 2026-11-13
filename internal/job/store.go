// Package job persists the job store: the jobs an orchestrating agent declares
// about the plan it is running, kept where a human can read them.
//
// It gates no run and blocks no write to the tree; the one write it refuses is a write to
// a job the caller does not own (see [authorizeRow]). One plan per REPOSITORY, so every
// worktree and clone reads the same jobs.
//
// The INTENT layer of three, flat stores joined by job id at render time rather than a
// hierarchy: intent is this package, actions are internal/trail, and effects are the run
// itself. internal/journal (one invocation's events) and internal/notes (prose for a
// later reader) model no leased work and are not siblings.
package job

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/store/migrate"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/egladman/magus/vcs"
)

// ErrNoID reports a lease with no id. The id is what Update upserts on, so a row without
// one could never be updated or referred to again: it is unaddressable, not merely
// incomplete.
var ErrNoID = errors.New("job: a lease needs an id")

// ErrMaxAttempts is a fork of an ended job that would start an attempt past its
// max_attempts. The error wrapping it is typed not retryable: forking again unchanged meets
// the same cap.
var ErrMaxAttempts = errors.New("job: max_attempts reached")

// Store is the repository's lease ledger, one JSON Lines file in the per-repository state
// directory (see [Store.Path]). Every operation reads the file, acts, and writes what
// changed, so a Store is cheap to construct and holds no state between calls beyond the
// path and its lock.
//
// TWO locks, because there are two kinds of concurrent writer and neither lock sees the
// other's:
//
//   - The mutex serializes writers within one process while they share a Store, which is
//     why the server builds exactly one and hands it to both of its doors (the
//     client MCP tool's magus\job and the console's read route).
//   - An OS file lock beside jobs.jsonl serializes writers across PROCESSES. The CLI, the
//     server, and an MCP client each hold their own Store on the same file, from any
//     worktree or clone of the repository, and workers register and heartbeat against it.
//     Two read-modify-writes that interleave drop whichever row the loser had not read.
//
// Both are taken for the span of ONE call. A caller that merges fields by reading with
// List and writing back the whole row takes them twice, and two such merges on one id
// then lose whichever field the second one read before the first wrote. [Store.Update] is
// that merge done under a single acquisition, and it is what a field-at-a-time writer (the
// client MCP tool's magus\job.put) has to use.
//
// READS take neither. A write appends whole lines in one write or replaces the file by
// rename, so a reader sees each row's previous line or its next one, waiting [tailWait] out
// an append it caught mid-write; blocking List behind
// a writer in another process would buy nothing and would let a wedged writer stall the
// console.
type Store struct {
	mu sync.Mutex
	storeConfig
	// dryEnded are the rows a dry-run [Exit] ended, which [Store.unswept] reads as ended
	// so a dry run ending a tree ends its descendants before their ancestors, as a real one.
	dryEnded map[string]types.Job
}

// storeConfig is everything a Store carries but its mutex, so a view ([Store.As],
// [Store.InWorkspace], [Store.dry]) copies it whole and sets only what differs.
type storeConfig struct {
	path string
	// err is the failure to work out where this ledger lives, kept so NewStore can stay
	// a constructor while every operation still reports it. A Store that cannot name its
	// file must not silently read as an empty ledger: absent rows are how the guard
	// decides nothing is leased.
	err  error
	root string
	// actor pins who this Store writes as; nil resolves the acting party at every write
	// from cacheDir and the environment. The server builds ONE Store at startup and serves
	// every MCP job caller from it, so an actor frozen at construction grades all of
	// them as whoever started the process.
	actor     *Actor
	cacheDir  string
	stateBase string
	// clock, staleAfter and notices are the sweep's seams (see [Store.List]); nil means
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
	landed func(context.Context, types.Job) string
	// grader is Location.Grader.
	grader CheckGrader
	// dryRun is Location.DryRun.
	dryRun bool
	// build is Location.Build.
	build string
	// modelTiers and modelTierMatrix are Location's.
	modelTiers      *agent.ModelTierSet
	modelRows       func() []types.ModelPricing
	modelTierMatrix func(context.Context, types.ModelTierRequest) (*agent.JobModelTier, error)
	spawnRule       func(parent, child agent.ModelTier) error
	owners          func(context.Context, []string) ([]string, error)
	// priceContext is Location.PriceContext.
	priceContext func(calls []types.AgentFacts) (tokens int64, usd float64, ok bool)
	// workspace is what validation reads the record against (see [Store.InWorkspace]);
	// nil names those checks unchecked.
	workspace *Workspace
	// migrations is [jobMigrations] outside a test.
	migrations []migrate.Migration
}

// jobMigrations take a jobs.jsonl from each migration to the next, in order; see
// [Store.migrate]. A migration rewrites stored rows once, so a change to a stored member's
// name lands here and no reader keeps the old name.
var jobMigrations = []migrate.Migration{
	{Name: "model, criteria and fresh_reason move under agent", Up: agentSection},
	{Name: "worker type resume becomes resumed", Up: workerResumed},
	{Name: triesMoved, Up: dropTries},
	{Name: "model tiers economy, standard and principal become routine, bounded and design", Up: fourModelTiers},
	{Name: recordsEnveloped, Up: envelopeRow, Header: envelopeHeader},
}

// recordsEnveloped names the migration [Store.migrate] rewrites
// attempts.jsonl before, with [Store.envelopeAttempts].
const recordsEnveloped = "rows and attempts take the batch.magus/v1alpha1 envelope; holder goes"

// headerEntry reports the header line, the one that is neither a row
// before [recordsEnveloped] nor one after.
func headerEntry(e migrate.Entry) bool {
	_, id := e["id"]
	_, spec := e["spec"]
	return !id && !spec
}

// envelopeRow rewrites a flat row as its [jobRecord]. A server's row is
// told by its registration once holder goes. A member types.Job does not
// declare is kept on the record as it was.
func envelopeRow(e migrate.Entry) (migrate.Entry, error) {
	text, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	var row types.Job
	var flat struct {
		Holder     string           `json:"holder"`
		APIVersion types.APIVersion `json:"api_version"`
	}
	if err := json.Unmarshal(text, &row); err != nil {
		return nil, fmt.Errorf("reading the row: %w", err)
	}
	if err := json.Unmarshal(text, &flat); err != nil {
		return nil, fmt.Errorf("reading holder: %w", err)
	}
	if _, old := e["schema_version"]; old || (flat.APIVersion != "" && flat.APIVersion.Compare(types.JobAPIVersion) < 0) {
		return nil, types.RefuseVersion(types.JobAPIVersion, flat.APIVersion, "job: row "+row.ID, staleStoreFix)
	}
	holder := flat
	if holder.Holder == "server" {
		row.RegisteredBy.EntryPoint = types.EntryPointServer
	}
	r := recordJob(row, nil)
	declared := jsonNames(reflect.TypeFor[types.Job]())
	for k, v := range e {
		if !declared[k] && k != "holder" && k != "api_version" {
			if r.Unknown == nil {
				r.Unknown = map[string]json.RawMessage{}
			}
			r.Unknown[k] = v
		}
	}
	return remarshal(r)
}

// envelopeHeader rewrites the flat header as the JobList line, its
// requires list as the requires annotation.
func envelopeHeader(e migrate.Entry) (migrate.Entry, error) {
	var requires []string
	if raw, ok := e["requires"]; ok {
		if err := json.Unmarshal(raw, &requires); err != nil {
			return nil, fmt.Errorf("reading requires: %w", err)
		}
	}
	features := make([]apiversion.Feature, len(requires))
	for i, r := range requires {
		features[i] = apiversion.Feature(r)
	}
	delete(e, "api_version")
	delete(e, "requires")
	next, err := remarshal(jobsFile{TypeMeta: jobListType, Metadata: apiversion.Metadata{}.WithRequires(recordGroup, features)})
	if err != nil {
		return nil, err
	}
	for k, v := range e {
		next[k] = v
	}
	return next, nil
}

// remarshal is v as an entry.
func remarshal(v any) (migrate.Entry, error) {
	text, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var e migrate.Entry
	if err := json.Unmarshal(text, &e); err != nil {
		return nil, err
	}
	return e, nil
}

// jsonNames are the member names t's fields declare.
func jsonNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		switch {
		case name == "-":
		case name != "":
			out[name] = true
		case f.Anonymous:
			for k := range jsonNames(f.Type) {
				out[k] = true
			}
		default:
			out[f.Name] = true
		}
	}
	return out
}

// workerResumed respells a stored worker type "resume" as "resumed", the
// value Go and the Buzz enum now share.
func workerResumed(e migrate.Entry) (migrate.Entry, error) {
	raw, ok := e["agent"]
	if !ok {
		return e, nil
	}
	var section map[string]json.RawMessage
	if err := json.Unmarshal(raw, &section); err != nil {
		return nil, fmt.Errorf("reading the agent section: %w", err)
	}
	var worker map[string]json.RawMessage
	if body, ok := section["worker"]; ok {
		if err := json.Unmarshal(body, &worker); err != nil {
			return nil, fmt.Errorf("reading agent.worker: %w", err)
		}
	}
	var typ string
	if body, ok := worker["type"]; ok {
		if err := json.Unmarshal(body, &typ); err != nil {
			return nil, fmt.Errorf("reading agent.worker.type: %w", err)
		}
	}
	if typ != "resume" {
		return e, nil
	}
	var err error
	if worker["type"], err = json.Marshal(agent.WorkerTypeResumed); err == nil {
		if section["worker"], err = json.Marshal(worker); err == nil {
			e["agent"], err = json.Marshal(section)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("writing agent.worker.type: %w", err)
	}
	return e, nil
}

// agentSection moves a row's flat agent members under agent. A model the
// stored tier resolved as a tier name becomes the requested tier, and any
// other model an id. A server's row takes no section: it ran no agent.
func agentSection(e migrate.Entry) (migrate.Entry, error) {
	var old struct {
		Criteria    string `json:"criteria"`
		Model       string `json:"model"`
		FreshReason string `json:"fresh_reason"`
		Holder      string `json:"holder"`
		ModelTier   *struct {
			Source string `json:"source"`
			Model  string `json:"model"`
		} `json:"model_tier"`
	}
	flat := map[string]json.RawMessage{}
	for _, key := range []string{"criteria", "model", "fresh_reason", "holder", "model_tier"} {
		if v, ok := e[key]; ok {
			flat[key] = v
		}
	}
	body, err := json.Marshal(flat)
	if err == nil {
		err = json.Unmarshal(body, &old)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the flat agent members: %w", err)
	}
	delete(e, "criteria")
	delete(e, "model")
	delete(e, "fresh_reason")
	if old.Criteria == "" && old.Model == "" && old.FreshReason == "" || old.Holder == "server" {
		return e, nil
	}
	spec := agent.Spec{Instructions: old.Criteria, Worker: agent.Worker{Type: agent.WorkerTypeResumed}}
	switch {
	case old.Model == "":
	case old.ModelTier != nil && old.ModelTier.Source == string(agent.ModelTierSourceRow) && old.ModelTier.Model == "":
		spec.Model.Tier = old.Model
	default:
		spec.Model.ID = old.Model
	}
	if old.FreshReason != "" {
		spec.Worker = agent.Worker{Type: agent.WorkerTypeNew, Reason: old.FreshReason}
	}
	if e["agent"], err = json.Marshal(spec); err != nil {
		return nil, fmt.Errorf("writing the agent section: %w", err)
	}
	return e, nil
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
	// release is digested (see Update). A Store built with an empty root still records
	// releases; it just cannot say what was in them.
	Root string
	// StateBase is the user state directory the ledger and the checkout records live
	// under: xdg.StateDir for every caller outside a test, resolved by the caller. Empty
	// is an error every Store operation reports, and a checkout with no record.
	StateBase string
	// Actor is who the Store writes as. A pointer because the zero Actor is UNBOUND and
	// is a real value: nil is the only way to say "resolve it at every write, from the
	// environment and CacheDir through [ActingActor]", which is what every door outside a
	// test wants.
	Actor *Actor
	// Outputs resolves an output ref against the output store of the checkout at root.
	// Exit and Wait fall back to it when the caller's resolver finds no run, trying the
	// checkouts the job and its descendants were taken in, so an orchestrator can file a
	// result its worker recorded in another checkout of the repository. Nil tries the
	// caller's resolver alone.
	Outputs func(root string) AttemptResolver
	// TrailBase resolves the activity trail base of the checkout at checkoutRoot, the cache
	// dir the guard records a holder's denials in. A fork of an ended job reads the denials of
	// its last holder there, from whichever checkout the holder took it in. Nil reads only
	// this Store's own CacheDir, and the denials of a holder in another checkout are unknown.
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
	// Build identifies the build whose results the footprint cache holds, so a rebuilt
	// binary under a long-lived daemon never reads the old code's results as its own.
	// Empty is the running build ([buildinfo.Build]); a test names another.
	Build string
	// ModelTiers is the workspace's model tier declaration. With it, a write that creates a
	// row or changes its Model assigns the row's tier (see [resolveModelTier]); nil assigns
	// nothing, as does a Store a dry run reads.
	ModelTiers *agent.ModelTierSet
	// ModelRows is the harness model rows (mgs_listModels), which name a model by id or
	// alias for a tier the declaration's hosts do not map. Nil leaves only the hosts to
	// resolve a model.
	ModelRows func() []types.ModelPricing
	// ModelTierMatrix is the function the workspace registered to choose a tier, or nil.
	// It runs inside the write, while the Store's locks are held, so it must not touch
	// the Store.
	ModelTierMatrix func(context.Context, types.ModelTierRequest) (*agent.JobModelTier, error)
	// SpawnRule is the workspace's magus.WithSpawnRule. It judges a row
	// whose tier and its parent's are both proven. Such a row with no rule
	// is refused.
	SpawnRule func(parent, child agent.ModelTier) error
	// Owners names the projects that own a list of files, the ownership `describe job`
	// reports as a job's projects. It is asked only to fill a matrix's request, so a Store
	// with no matrix never calls it; nil leaves the request's WriteProjects empty.
	Owners func(ctx context.Context, files []string) ([]string, error)
	// PriceContext prices a job's holder's context when the job ends, as the model
	// table's PriceContext does: the tokens and dollars, and false when the price would be
	// a guess. Nil leaves every outcome's tokens and cost unknown.
	PriceContext func(calls []types.AgentFacts) (tokens int64, usd float64, ok bool)
}

// NewStore returns the ledger for loc's repository. The file itself is created by the
// first write.
//
// THE CACHE DIRECTORY IS NOT THE HOME, and that is the whole point of this
// resolution: a cache dir belongs to one CHECKOUT, so an orchestrator's rows in one
// worktree were invisible to a worker in another, and a lease-scoped guard rule could
// not bind across the two. The rows describe a repository's plan, so they key on
// repository identity exactly as internal/sessions does.
//
// A resolution failure is held rather than returned: every operation reports it, so a
// caller cannot mistake an unplaceable ledger for an empty one.
func NewStore(loc Location) *Store {
	s := &Store{storeConfig: storeConfig{root: loc.Root, actor: loc.Actor, cacheDir: loc.CacheDir, stateBase: loc.StateBase, outputs: loc.Outputs, trailBase: loc.TrailBase, grader: loc.Grader, dryRun: loc.DryRun, build: cmp.Or(loc.Build, buildinfo.Build()),
		modelTiers: loc.ModelTiers, modelRows: loc.ModelRows, modelTierMatrix: loc.ModelTierMatrix, spawnRule: loc.SpawnRule, owners: loc.Owners, priceContext: loc.PriceContext,
		migrations: jobMigrations}}
	s.path, s.err = jobsPath(loc)
	return s
}

// view is a Store over s's file with every seam s has and a mutex of its own; the file
// lock still serializes it against s and every other writer.
func (s *Store) view() *Store {
	return &Store{storeConfig: s.storeConfig}
}

// Actor is who this Store writes as RIGHT NOW, for a door that has to refuse before it
// computes anything: `ledger accept` grades a report and must say a worker cannot grade
// its own row before it reads one. Resolved per call unless Location pinned one, so a
// checkout that binds a lease is graded from its next write.
func (s *Store) Actor() Actor {
	if s.actor != nil {
		return *s.actor
	}
	return ActingActor(s.place())
}

// place is the part of s's Location that keys the checkout record.
func (s *Store) place() Location {
	return Location{CacheDir: s.cacheDir, StateBase: s.stateBase}
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

const (
	jobsFileName = "jobs.jsonl"
	// documentFileName is the one JSON document an older magus kept the plan in.
	documentFileName = "jobs.json"
)

// refuseDocument fails when the store's directory still holds the plan an older magus kept
// as one JSON document. Reading past it would show an empty plan while the rows sit beside
// it, and the guard reads an empty plan as nothing leased. The refusal names the move that
// sets the document aside once no older magus writes it; nothing reads it after that.
func (s *Store) refuseDocument() error {
	old := filepath.Join(filepath.Dir(s.path), documentFileName)
	if err := file.RefuseOld(old); err != nil {
		err = fmt.Errorf("cannot open job plan %s: %w", s.path, err)
		return fmt.Errorf("%w: once no older magus writes it, set it aside with `mv %s %s.old`",
			err, shellQuote(old), shellQuote(old))
	}
	return nil
}

// shellQuote is p as one POSIX shell word. A copy: internal/guard's shellWord sits in
// a package that imports this one, and cmd/magus's in package main.
func shellQuote(p string) string {
	if p != "" && strings.Trim(p, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+@:,") == "" {
		return p
	}
	return fmt.Sprintf("'%s'", strings.ReplaceAll(p, "'", `'\''`))
}

// Path is the JSON Lines file this Store reads and writes, for a reader that has to name
// it: a person asking where their plan is kept, or a test planting one. The file itself may
// not exist yet, which is an empty ledger and not an error.
func (s *Store) Path() (string, error) { return s.path, s.err }

// jobsFile is the ledger as read: the header and the rows.
//
// The file is JSON Lines. The header is the line without an id, written first by every
// rewrite, and each other line is one version of one row. A row's LAST line is the row, and
// it keeps the place its first line had, so the rows read in the order they were declared.
//
// Each row line is a [jobRecord]. The header's requires annotation
// refuses the whole file to a reader lacking a feature it names.
type jobsFile struct {
	apiversion.TypeMeta
	Metadata apiversion.Metadata `json:"metadata,omitzero"`
	// Migration is how many of [jobMigrations] the rows have been through;
	// see [Store.migrate]. A file past this build's is refused.
	Migration int `json:"migration,omitzero"`
	// Retained is when the last retention pass ran, in unix seconds; see [Store.retainDue].
	Retained int64                      `json:"retained,omitempty"`
	Unknown  map[string]json.RawMessage `json:",unknown"`
	Jobs     []types.Job                `json:"-"`
	// stored are the rows' records as read, by id, so a rewrite keeps
	// what this build does not declare.
	stored map[string]jobRecord
}

// header is f without its rows.
func (f jobsFile) header() jobsFile {
	return jobsFile{TypeMeta: f.TypeMeta, Metadata: f.Metadata, Migration: f.Migration, Retained: f.Retained, Unknown: f.Unknown}
}

// RecordAPIVersion is the apiVersion of every line of jobs.jsonl and
// attempts.jsonl (ADR 0044).
const RecordAPIVersion apiversion.APIVersion = "batch.magus/v1alpha1"

// recordGroup prefixes the annotations a record carries.
const recordGroup = "batch.magus"

var (
	jobListType = apiversion.TypeMeta{APIVersion: RecordAPIVersion, Kind: "JobList"}
	jobType     = apiversion.TypeMeta{APIVersion: RecordAPIVersion, Kind: "Job"}
	attemptType = apiversion.TypeMeta{APIVersion: RecordAPIVersion, Kind: "Attempt"}
)

// jobRecord is one row of jobs.jsonl, the persistence type of
// [types.Job] (ADR 0030). [recordJob] and [jobRecord.job] are the one
// conversion. Unknown, here and on spec and status, is what a newer
// magus wrote, carried back out unchanged.
type jobRecord struct {
	apiversion.TypeMeta
	Metadata apiversion.Metadata        `json:"metadata"`
	Spec     jobSpec                    `json:"spec"`
	Status   jobStatus                  `json:"status"`
	Unknown  map[string]json.RawMessage `json:",unknown"`
}

// jobSpec is what a declaration writes.
type jobSpec struct {
	Parent       string                     `json:"parent,omitempty"`
	Agent        *agentRecord               `json:"agent,omitempty"`
	Checkpoint   string                     `json:"checkpoint,omitempty"`
	WritePaths   []string                   `json:"writePaths,omitempty"`
	DenyPaths    []string                   `json:"denyPaths,omitempty"`
	SoftPaths    []string                   `json:"softPaths,omitempty"`
	ReadPaths    []string                   `json:"readPaths,omitempty"`
	DependsOn    []string                   `json:"dependsOn,omitempty"`
	Integrates   []string                   `json:"integrates,omitempty"`
	Breaks       []string                   `json:"breaks,omitempty"`
	BriefFormat  string                     `json:"briefFormat,omitempty"`
	Delivery     string                     `json:"delivery,omitempty"`
	ParentHead   string                     `json:"parentHead,omitempty"`
	Follows      string                     `json:"follows,omitempty"`
	MaxAttempts  int                        `json:"maxAttempts,omitzero"`
	MaxClaims    int                        `json:"maxClaims,omitzero"`
	Deferred     []types.JobDeferred        `json:"deferred,omitempty"`
	LeftUnmerged string                     `json:"leftUnmerged,omitempty"`
	Weight       float64                    `json:"weight,omitzero"`
	Check        *leaseCheckRecord          `json:"check,omitempty"`
	Validation   string                     `json:"validation,omitempty"`
	Goals        []goalRecord               `json:"goals,omitempty"`
	ReadOnly     bool                       `json:"readOnly,omitzero"`
	Deviations   []types.Deviation          `json:"deviations,omitempty"`
	Unknown      map[string]json.RawMessage `json:",unknown"`
}

// jobStatus is what the store computes. Its conditions are the row's
// lifecycle; see [lifecycleConditions].
type jobStatus struct {
	Conditions      []apiversion.Condition       `json:"conditions,omitempty"`
	ModelTier       *modelTierRecord             `json:"modelTier,omitempty"`
	ReworkRounds    int                          `json:"reworkRounds,omitzero"`
	Widenings       int                          `json:"widenings,omitzero"`
	Claims          []claimRecord                `json:"claims,omitempty"`
	Landed          bool                         `json:"landed,omitzero"`
	Paused          int64                        `json:"paused,omitzero"`
	PausedBy        originRecord                 `json:"pausedBy,omitzero"`
	PauseReason     string                       `json:"pauseReason,omitempty"`
	PausedUntil     int64                        `json:"pausedUntil,omitzero"`
	Rejection       *rejectionRecord             `json:"rejection,omitempty"`
	Releases        []releaseRecord              `json:"releases,omitempty"`
	Unattributed    []types.JobUnattributedWrite `json:"unattributed,omitempty"`
	Entries         []entryRecord                `json:"entries,omitempty"`
	ReleaseRequests []types.JobReleaseRequest    `json:"releaseRequests,omitempty"`
	WriteProof      string                       `json:"writeProof,omitempty"`
	Validated       int64                        `json:"validated,omitzero"`
	ReportedBase    string                       `json:"reportedBase,omitempty"`
	BaseVerdict     string                       `json:"baseVerdict,omitempty"`
	RegisteredBy    originRecord                 `json:"registeredBy,omitzero"`
	Authority       string                       `json:"authority,omitempty"`
	Registered      int64                        `json:"registered,omitzero"`
	HolderStopped   int64                        `json:"holderStopped,omitzero"`
	CheckoutRoot    string                       `json:"checkoutRoot,omitempty"`
	Worktree        *worktreeRecord              `json:"worktree,omitempty"`
	EndReason       string                       `json:"endReason,omitempty"`
	Created         int64                        `json:"created"`
	Updated         int64                        `json:"updated"`
	Deadline        int64                        `json:"deadline,omitzero"`
	Attempt         *runAttemptRecord            `json:"attempt,omitempty"`
	GateAttempts    []gateAttemptRecord          `json:"gateAttempts,omitempty"`
	LastRun         *runRecord                   `json:"lastRun,omitempty"`
	Integration     *integrationRecord           `json:"integration,omitempty"`
	WaiterEvidence  *integrationRecord           `json:"waiterEvidence,omitempty"`
	Unknown         map[string]json.RawMessage   `json:",unknown"`
}

// recordJob is row as the line jobs.jsonl stores. stored is the row's
// record as read, nil for a new row: what it carries that row cannot say
// is kept.
func recordJob(row types.Job, stored *jobRecord) jobRecord {
	var prev jobRecord
	if stored != nil {
		prev = *stored
	}
	features := make([]apiversion.Feature, len(row.Requires))
	for i, f := range row.Requires {
		features[i] = apiversion.Feature(f)
	}
	meta := prev.Metadata.WithRequires(recordGroup, features)
	meta.Name, meta.Generation = row.ID, int64(row.Generation)
	r := jobRecord{TypeMeta: jobType, Metadata: meta, Unknown: prev.Unknown}
	r.Spec = jobSpec{
		Parent: row.Parent, Agent: recordAgent(row.Agent), Checkpoint: row.Checkpoint,
		WritePaths: row.WritePaths, DenyPaths: row.DenyPaths, SoftPaths: row.SoftPaths, ReadPaths: row.ReadPaths,
		DependsOn: row.DependsOn, Integrates: row.Integrates, Breaks: row.Breaks,
		BriefFormat: wireBriefFormat.spell(row.BriefFormat), Delivery: row.Delivery, ParentHead: row.ParentHead,
		Follows: row.Follows, MaxAttempts: row.MaxAttempts, MaxClaims: row.MaxClaims,
		Deferred: row.Deferred, LeftUnmerged: row.LeftUnmerged, Weight: row.Weight,
		Validation: row.Validation, ReadOnly: row.ReadOnly, Deviations: row.Deviations,
		Unknown: prev.Spec.Unknown,
	}
	if row.Check != nil {
		c := leaseCheckRecord(*row.Check)
		r.Spec.Check = &c
	}
	for _, g := range row.Goals {
		r.Spec.Goals = append(r.Spec.Goals, recordGoal(g))
	}
	r.Status = jobStatus{
		Conditions:   lifecycleConditions(row, prev.Status.Conditions),
		ModelTier:    recordModelTier(row.ModelTier),
		ReworkRounds: row.ReworkRounds, Widenings: row.Widenings, Landed: row.Landed,
		Paused: row.Paused, PausedBy: recordOrigin(row.PausedBy), PauseReason: row.PauseReason, PausedUntil: row.PausedUntil,
		Rejection:    recordRejection(row.Rejection),
		Unattributed: row.Unattributed, ReleaseRequests: row.ReleaseRequests,
		WriteProof: wireWriteProof.spell(row.WriteProof), Validated: row.Validated,
		ReportedBase: row.ReportedBase, BaseVerdict: wireBaseVerdict.spell(row.BaseVerdict),
		RegisteredBy: recordOrigin(row.RegisteredBy), Authority: row.Authority, Registered: row.Registered,
		HolderStopped: row.HolderStopped, CheckoutRoot: row.CheckoutRoot, EndReason: row.EndReason,
		Created: row.Created, Updated: row.Updated, Deadline: row.Deadline,
		Integration: recordIntegration(row.Integration), WaiterEvidence: recordIntegration(row.WaiterEvidence),
		Unknown: prev.Status.Unknown,
	}
	for _, c := range row.Claims {
		r.Status.Claims = append(r.Status.Claims, claimRecord{
			Path: c.Path, Evidence: claimEvidenceRecord{Kind: wireClaimKind.spell(c.Evidence.Kind), Subject: c.Evidence.Subject},
			Proof: c.Proof, At: c.At, By: recordOrigin(c.By),
		})
	}
	for _, rel := range row.Releases {
		r.Status.Releases = append(r.Status.Releases, releaseRecord(rel))
	}
	for _, e := range row.Entries {
		r.Status.Entries = append(r.Status.Entries, entryRecord{Path: e.Path, By: recordOrigin(e.By), At: e.At, Consumed: e.Consumed})
	}
	if row.Worktree != nil {
		w := worktreeRecord(*row.Worktree)
		r.Status.Worktree = &w
	}
	if row.Attempt != nil {
		a := runAttemptRecord(*row.Attempt)
		r.Status.Attempt = &a
	}
	for _, g := range row.GateAttempts {
		r.Status.GateAttempts = append(r.Status.GateAttempts, gateAttemptRecord{GateID: g.GateID, Attempt: runAttemptRecord(g.Attempt)})
	}
	if row.LastRun != nil {
		run := runRecord(*row.LastRun)
		r.Status.LastRun = &run
	}
	return r
}

// job is r as the domain type, refusing a record of another kind or
// version.
func (r jobRecord) job() (types.Job, error) {
	if r.TypeMeta != jobType {
		return types.Job{}, &apiversion.RefusedError{Want: jobType, Got: r.TypeMeta}
	}
	state, err := lifecycleState(r.Status.Conditions)
	if err != nil {
		return types.Job{}, fmt.Errorf("job %s: %w", r.Metadata.Name, err)
	}
	s, st := r.Spec, r.Status
	row := types.Job{
		ID: r.Metadata.Name, Generation: int(r.Metadata.Generation), State: state,
		Parent: s.Parent, Agent: s.Agent.spec(), Checkpoint: s.Checkpoint,
		WritePaths: s.WritePaths, DenyPaths: s.DenyPaths, SoftPaths: s.SoftPaths, ReadPaths: s.ReadPaths,
		DependsOn: s.DependsOn, Integrates: s.Integrates, Breaks: s.Breaks,
		BriefFormat: wireBriefFormat.read(s.BriefFormat), Delivery: s.Delivery, ParentHead: s.ParentHead,
		Follows: s.Follows, MaxAttempts: s.MaxAttempts, MaxClaims: s.MaxClaims,
		Deferred: s.Deferred, LeftUnmerged: s.LeftUnmerged, Weight: s.Weight,
		Validation: s.Validation, ReadOnly: s.ReadOnly, Deviations: s.Deviations,
		ModelTier:    st.ModelTier.tier(),
		ReworkRounds: st.ReworkRounds, Widenings: st.Widenings, Landed: st.Landed,
		Paused: st.Paused, PausedBy: st.PausedBy.origin(), PauseReason: st.PauseReason, PausedUntil: st.PausedUntil,
		Rejection:    st.Rejection.rejection(),
		Unattributed: st.Unattributed, ReleaseRequests: st.ReleaseRequests,
		WriteProof: wireWriteProof.read(st.WriteProof), Validated: st.Validated,
		ReportedBase: st.ReportedBase, BaseVerdict: wireBaseVerdict.read(st.BaseVerdict),
		RegisteredBy: st.RegisteredBy.origin(), Authority: st.Authority, Registered: st.Registered,
		HolderStopped: st.HolderStopped, CheckoutRoot: st.CheckoutRoot, EndReason: st.EndReason,
		Created: st.Created, Updated: st.Updated, Deadline: st.Deadline,
		Integration: st.Integration.integration(), WaiterEvidence: st.WaiterEvidence.integration(),
	}
	for _, f := range r.Metadata.Requires(recordGroup) {
		row.Requires = append(row.Requires, string(f))
	}
	row.Unknown = unknownMembers(r.Unknown, s.Unknown, st.Unknown)
	if s.Check != nil {
		c := types.LeaseCheck(*s.Check)
		row.Check = &c
	}
	for _, g := range s.Goals {
		row.Goals = append(row.Goals, g.goal())
	}
	for _, c := range st.Claims {
		row.Claims = append(row.Claims, types.JobClaim{
			Path: c.Path, Evidence: types.ClaimEvidence{Kind: wireClaimKind.read(c.Evidence.Kind), Subject: c.Evidence.Subject},
			Proof: c.Proof, At: c.At, By: c.By.origin(),
		})
	}
	for _, rel := range st.Releases {
		row.Releases = append(row.Releases, types.JobRelease(rel))
	}
	for _, e := range st.Entries {
		row.Entries = append(row.Entries, types.JobEntry{Path: e.Path, By: e.By.origin(), At: e.At, Consumed: e.Consumed})
	}
	if st.Worktree != nil {
		w := types.JobWorktree(*st.Worktree)
		row.Worktree = &w
	}
	if st.Attempt != nil {
		a := types.JobAttempt(*st.Attempt)
		row.Attempt = &a
	}
	for _, g := range st.GateAttempts {
		row.GateAttempts = append(row.GateAttempts, types.JobGateAttempt{GateID: g.GateID, Attempt: types.JobAttempt(g.Attempt)})
	}
	if st.LastRun != nil {
		run := types.JobRun(*st.LastRun)
		row.LastRun = &run
	}
	return row, nil
}

// unknownMembers names the members of top, spec and status, as
// [types.Job.Unknown] spells them.
func unknownMembers(top, spec, status map[string]json.RawMessage) []string {
	var out []string
	for k := range top {
		out = append(out, k)
	}
	for k := range spec {
		out = append(out, "spec."+k)
	}
	for k := range status {
		out = append(out, "status."+k)
	}
	slices.Sort(out)
	return out
}

// lifecycleCondition is the condition a state is stored as, True.
type lifecycleCondition struct {
	state        types.JobState
	kind, reason string
}

// lifecycle is every state's condition. Failed takes two reasons, as a
// Kubernetes Job's Failed does.
var lifecycle = []lifecycleCondition{
	{types.StateDeclared, "Declared", "Declared"},
	{types.StateRunning, "Running", "Running"},
	{types.StateExited, "Exited", "Exited"},
	{types.StatePass, "Complete", "Passed"},
	{types.StateFail, "Failed", "CriteriaUnmet"},
	{types.StateNoReturn, "Failed", "NoReturn"},
}

// lifecycleConditions are row's conditions: its state's, then every
// condition of stored no state names, kept as stored. A state's condition
// keeps its stored transition time; a new one transitioned at Updated.
func lifecycleConditions(row types.Job, stored []apiversion.Condition) []apiversion.Condition {
	var out []apiversion.Condition
	for _, l := range lifecycle {
		if l.state != row.State {
			continue
		}
		c := apiversion.Condition{
			Type: l.kind, Status: apiversion.ConditionTrue, ObservedGeneration: int64(row.Generation),
			LastTransitionTime: time.Unix(row.Updated, 0).UTC(), Reason: l.reason,
		}
		for _, s := range stored {
			if s.Type == c.Type && s.Reason == c.Reason && s.Status == c.Status {
				c.LastTransitionTime = s.LastTransitionTime
			}
		}
		out = append(out, c)
	}
	for _, s := range stored {
		if !slices.ContainsFunc(lifecycle, func(l lifecycleCondition) bool { return l.kind == s.Type }) {
			out = append(out, s)
		}
	}
	return out
}

// lifecycleState is the state conditions hold True, "" for none. Two
// is a record no magus writes.
func lifecycleState(conditions []apiversion.Condition) (types.JobState, error) {
	var state types.JobState
	for _, c := range conditions {
		if c.Status != apiversion.ConditionTrue {
			continue
		}
		for _, l := range lifecycle {
			if l.kind != c.Type || l.reason != c.Reason {
				continue
			}
			if state != "" {
				return "", fmt.Errorf("the conditions hold both %s and %s", state, l.state)
			}
			state = l.state
		}
	}
	return state, nil
}

// wireEnum spells an enum's lowercase Go values in CamelCase on a record.
// A value it does not list passes through both ways unchanged.
type wireEnum[T ~string] struct {
	wire map[T]string
	gov  map[string]T
}

func newWireEnum[T ~string](values ...T) wireEnum[T] {
	e := wireEnum[T]{wire: map[T]string{}, gov: map[string]T{}}
	for _, v := range values {
		w := camelCase(string(v))
		e.wire[v], e.gov[w] = w, v
	}
	return e
}

func (e wireEnum[T]) spell(v T) string {
	if w, ok := e.wire[v]; ok {
		return w
	}
	return string(v)
}

func (e wireEnum[T]) read(w string) T {
	if v, ok := e.gov[w]; ok {
		return v
	}
	return T(w)
}

// camelCase spells words joined by '_' or '-' as one CamelCase word.
func camelCase(v string) string {
	var b strings.Builder
	for word := range strings.FieldsFuncSeq(v, func(r rune) bool { return r == '_' || r == '-' }) {
		b.WriteString(strings.ToUpper(word[:1]) + word[1:])
	}
	return b.String()
}

var (
	wireWriteProof  = newWireEnum(types.WriteProofAlone, types.WriteProofDisjoint, types.WriteProofOverlapping)
	wireBaseVerdict = newWireEnum(types.BaseMatch, types.BaseRevisionMatch, types.BaseDiverged, types.BaseUnknown)
	wireBriefFormat = newWireEnum(types.BriefFormatMarkdown, types.BriefFormatBuzz)
	wireGoalKind    = newWireEnum(types.GoalKinds()...)
	wireGoalExpect  = newWireEnum(types.GoalExpects()...)
	wireClaimKind   = newWireEnum(types.ClaimRefs, types.ClaimTest, types.ClaimOutput, types.ClaimReleased, types.ClaimDeadHolder)
	wireEntryPoint  = newWireEnum(types.EntryPointCLI, types.EntryPointHook, types.EntryPointMCP, types.EntryPointRPC, types.EntryPointServer)
	wireCredential  = newWireEnum(types.KindOperator, types.KindStored, types.KindShare, types.KindExchange, types.KindStdio, types.KindSocketPeer)
	wireWorkerType  = newWireEnum(agent.WorkerTypeResumed, agent.WorkerTypeNew)
	wireTierSource  = newWireEnum(agent.ModelTierSourceRow, agent.ModelTierSourceMatrix, agent.ModelTierSourceUserMatrix, agent.ModelTierSourceDefault)
	wireSkills      = newWireEnum(agent.SkillVariantShort, agent.SkillVariantFull)
)

// The records below mirror domain types under camelCase keys. A domain
// type whose keys are all one word is stored as it is.

type agentRecord struct {
	Model        agent.Model  `json:"model,omitzero"`
	Instructions string       `json:"instructions,omitempty"`
	Worker       workerRecord `json:"worker"`
}

type workerRecord struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

func recordAgent(s *agent.Spec) *agentRecord {
	if s == nil {
		return nil
	}
	return &agentRecord{Model: s.Model, Instructions: s.Instructions,
		Worker: workerRecord{Type: wireWorkerType.spell(s.Worker.Type), Reason: s.Worker.Reason}}
}

func (r *agentRecord) spec() *agent.Spec {
	if r == nil {
		return nil
	}
	return &agent.Spec{Model: r.Model, Instructions: r.Instructions,
		Worker: agent.Worker{Type: wireWorkerType.read(r.Worker.Type), Reason: r.Worker.Reason}}
}

type leaseCheckRecord struct {
	Target          string   `json:"target"`
	Script          string   `json:"script,omitempty"`
	Project         string   `json:"project,omitempty"`
	Args            []string `json:"args,omitempty"`
	NoDefaultCharms bool     `json:"noDefaultCharms,omitzero"`
	Test            bool     `json:"test,omitzero"`
}

type goalRecord struct {
	ID          string           `json:"id"`
	Description string           `json:"description,omitempty"`
	DependsOn   []string         `json:"dependsOn,omitempty"`
	Kind        string           `json:"kind"`
	Expect      string           `json:"expect,omitempty"`
	Check       leaseCheckRecord `json:"check,omitzero"`
	Paths       []string         `json:"paths,omitempty"`
	Symbols     []string         `json:"symbols,omitempty"`
	Signature   string           `json:"signature,omitempty"`
}

func recordGoal(g types.Goal) goalRecord {
	return goalRecord{ID: g.ID, Description: g.Description, DependsOn: g.DependsOn,
		Kind: wireGoalKind.spell(g.Kind), Expect: wireGoalExpect.spell(g.Expect), Check: leaseCheckRecord(g.Check),
		Paths: g.Paths, Symbols: g.Symbols, Signature: g.Signature}
}

func (r goalRecord) goal() types.Goal {
	return types.Goal{ID: r.ID, Description: r.Description, DependsOn: r.DependsOn,
		Kind: wireGoalKind.read(r.Kind), Expect: wireGoalExpect.read(r.Expect), Check: types.LeaseCheck(r.Check),
		Paths: r.Paths, Symbols: r.Symbols, Signature: r.Signature}
}

type modelTierRecord struct {
	Name   string   `json:"name"`
	Rank   int      `json:"rank"`
	Skills string   `json:"skills"`
	Source string   `json:"source"`
	Reason string   `json:"reason,omitempty"`
	Site   string   `json:"site,omitempty"`
	Model  string   `json:"model,omitempty"`
	Shared []string `json:"shared,omitempty"`
}

func recordModelTier(t *agent.JobModelTier) *modelTierRecord {
	if t == nil {
		return nil
	}
	return &modelTierRecord{Name: t.Name, Rank: t.Rank, Skills: wireSkills.spell(t.Skills), Source: wireTierSource.spell(t.Source),
		Reason: t.Reason, Site: t.Site, Model: t.Model, Shared: t.Shared}
}

func (r *modelTierRecord) tier() *agent.JobModelTier {
	if r == nil {
		return nil
	}
	return &agent.JobModelTier{Name: r.Name, Rank: r.Rank, Skills: wireSkills.read(r.Skills), Source: wireTierSource.read(r.Source),
		Reason: r.Reason, Site: r.Site, Model: r.Model, Shared: r.Shared}
}

type originRecord struct {
	User       string           `json:"user,omitempty"`
	UID        string           `json:"uid,omitempty"`
	EntryPoint string           `json:"entryPoint,omitempty"`
	Host       string           `json:"host,omitempty"`
	Session    string           `json:"session,omitempty"`
	Agent      string           `json:"agent,omitempty"`
	Credential credentialRecord `json:"credential,omitzero"`
}

type credentialRecord struct {
	Kind  string      `json:"class,omitempty"`
	ID    string      `json:"id,omitempty"`
	Name  string      `json:"name,omitempty"`
	Grant types.Grant `json:"grant,omitzero"`
}

func recordOrigin(o types.Origin) originRecord {
	return originRecord{User: o.User, UID: o.UID, EntryPoint: wireEntryPoint.spell(o.EntryPoint), Host: o.Host,
		Session: o.Session, Agent: o.Agent, Credential: credentialRecord{
			Kind: wireCredential.spell(o.Credential.Kind), ID: o.Credential.ID, Name: o.Credential.Name, Grant: o.Credential.Grant,
		}}
}

func (r originRecord) origin() types.Origin {
	return types.Origin{User: r.User, UID: r.UID, EntryPoint: wireEntryPoint.read(r.EntryPoint), Host: r.Host,
		Session: r.Session, Agent: r.Agent, Credential: types.Credential{
			Kind: wireCredential.read(r.Credential.Kind), ID: r.Credential.ID, Name: r.Credential.Name, Grant: r.Credential.Grant,
		}}
}

type claimRecord struct {
	Path     string              `json:"path"`
	Evidence claimEvidenceRecord `json:"evidence"`
	Proof    string              `json:"proof"`
	At       int64               `json:"at"`
	By       originRecord        `json:"by"`
}

type claimEvidenceRecord struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject,omitempty"`
}

type releaseRecord struct {
	Path        string `json:"path"`
	Digest      string `json:"digest"`
	ReleasedAt  int64  `json:"releasedAt"`
	Revoked     bool   `json:"revoked,omitzero"`
	TakenBy     string `json:"takenBy,omitempty"`
	DeadBecause string `json:"deadBecause,omitempty"`
	Narrowed    bool   `json:"narrowed,omitzero"`
}

type entryRecord struct {
	Path     string       `json:"path"`
	By       originRecord `json:"by"`
	At       int64        `json:"at"`
	Consumed int64        `json:"consumed,omitzero"`
}

type verificationRecord struct {
	ID         string   `json:"id"`
	Verified   bool     `json:"verified"`
	OutputRef  string   `json:"outputRef,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	Owed       string   `json:"owed,omitempty"`
	Violations []string `json:"violations,omitempty"`
}

func recordVerifications(in []types.VerificationStatus) []verificationRecord {
	var out []verificationRecord
	for _, v := range in {
		out = append(out, verificationRecord(v))
	}
	return out
}

func verifications(in []verificationRecord) []types.VerificationStatus {
	var out []types.VerificationStatus
	for _, v := range in {
		out = append(out, types.VerificationStatus(v))
	}
	return out
}

type rejectionRecord struct {
	Unmet              []verificationRecord `json:"unmet,omitempty"`
	Violations         []string             `json:"violations,omitempty"`
	At                 int64                `json:"at"`
	ObservedGeneration int                  `json:"observedGeneration,omitzero"`
}

func recordRejection(r *types.JobRejection) *rejectionRecord {
	if r == nil {
		return nil
	}
	return &rejectionRecord{Unmet: recordVerifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
}

func (r *rejectionRecord) rejection() *types.JobRejection {
	if r == nil {
		return nil
	}
	return &types.JobRejection{Unmet: verifications(r.Unmet), Violations: r.Violations, At: r.At, ObservedGeneration: r.ObservedGeneration}
}

type worktreeRecord struct {
	Root         string `json:"root"`
	Source       string `json:"source"`
	Base         string `json:"base"`
	Created      int64  `json:"created"`
	Binary       string `json:"binary,omitempty"`
	BinaryDigest string `json:"binaryDigest,omitempty"`
	Removed      int64  `json:"removed,omitzero"`
	Kept         string `json:"kept,omitempty"`
	Made         bool   `json:"made,omitzero"`
}

type runAttemptRecord struct {
	Found              bool     `json:"found"`
	Ref                string   `json:"ref,omitempty"`
	TimestampMs        int64    `json:"timestampMs,omitzero"`
	Project            string   `json:"project,omitempty"`
	Target             string   `json:"target,omitempty"`
	Spell              string   `json:"spell,omitempty"`
	Failed             bool     `json:"failed,omitzero"`
	AttemptID          string   `json:"attemptId,omitempty"`
	DurationMs         int64    `json:"durationMs,omitzero"`
	Revision           string   `json:"revision,omitempty"`
	Dirty              bool     `json:"dirty,omitzero"`
	Lease              string   `json:"lease,omitempty"`
	ExtraArgs          []string `json:"extraArgs,omitempty"`
	ObservedGeneration int      `json:"observedGeneration,omitzero"`
}

type gateAttemptRecord struct {
	GateID  string           `json:"gateId"`
	Attempt runAttemptRecord `json:"attempt"`
}

type runRecord struct {
	Invocation     string `json:"invocation,omitempty"`
	Ended          int64  `json:"ended,omitzero"`
	DurationMs     int64  `json:"durationMs,omitzero"`
	OK             bool   `json:"ok"`
	Error          string `json:"error,omitempty"`
	ItemsRemoved   int64  `json:"itemsRemoved,omitzero"`
	BytesReclaimed int64  `json:"bytesReclaimed,omitzero"`
}

type integrationRecord struct {
	Checkout           string               `json:"checkout"`
	At                 int64                `json:"at"`
	Verified           bool                 `json:"verified"`
	Gates              []verificationRecord `json:"gates,omitempty"`
	ObservedGeneration int                  `json:"observedGeneration,omitzero"`
}

func recordIntegration(i *types.JobIntegration) *integrationRecord {
	if i == nil {
		return nil
	}
	return &integrationRecord{Checkout: i.Checkout, At: i.At, Verified: i.Verified, Gates: recordVerifications(i.Gates), ObservedGeneration: i.ObservedGeneration}
}

func (r *integrationRecord) integration() *types.JobIntegration {
	if r == nil {
		return nil
	}
	return &types.JobIntegration{Checkout: r.Checkout, At: r.At, Verified: r.Verified, Gates: verifications(r.Gates), ObservedGeneration: r.ObservedGeneration}
}

// jobsLog is the file a write read, so the write appends only the lines that changed.
type jobsLog struct {
	header []byte
	// rows is the last line of each row, by id.
	rows    map[string][]byte
	lines   int
	partial bool
}

// tailWait is how long a reader waits for an append it caught mid-write to finish before
// it reports the file's last line as one no writer finished.
const tailWait = 50 * time.Millisecond

// readOnly refuses a write to row when it requires a feature this magus lacks. The row
// stays listed and graded; only writing it is out of reach, since a write by a reader
// that cannot act on the row's content could corrupt it.
func readOnly(row types.Job) error {
	lacks := row.Unmet()
	if lacks == nil {
		return nil
	}
	return types.DiagnosticErrorf(types.StoreRecordsNewer, "%s requires %s, which this magus (%s) lacks: update magus",
		row.ID, quoteAll(lacks), RecordAPIVersion)
}

// requiredFeatures are the JobSchema features row's content needs a reader to implement,
// sorted: a `file#Declaration` claim, a `signature` goal and an attempt record (a cap, a
// prior attempt, a second attempt) are each read wrongly by a magus older than the version
// that named them, so a row carrying one is read-only to it. Derived
// from the content on every write, so a row that drops its last claim is writable again.
// Any requirement the stored row carried that this build does not know never reaches here:
// readOnly refused that write.
func requiredFeatures(row types.Job) []string {
	var out []string
	if slices.ContainsFunc(slices.Concat(row.WritePaths, row.DenyPaths), types.HasClaim) {
		out = append(out, "claim-declarations")
	}
	if slices.ContainsFunc(row.Goals, func(g types.Goal) bool { return g.Resolve().Expect == types.ExpectSignature }) {
		out = append(out, "signature-goals")
	}
	// Every row has a generation, so a row carrying only that is not stamped: it would make
	// every row read-only to an older magus.
	if row.MaxAttempts > 0 {
		out = append(out, "job-generations")
	}
	slices.Sort(out)
	return out
}

func quoteAll(names []string) string {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = strconv.Quote(n)
	}
	return strings.Join(quoted, ", ")
}

// Update applies apply to the row with this id and writes the result back while holding
// both of the Store's locks ONCE: a merge spread across List and a whole-row write
// releases them in between, so two concurrent writers advancing different fields of one
// row each read it before the other wrote, and the second write reverts the first.
//
// A caller that wants to replace a row whole rather than merge fields passes an apply that
// does `*cur = row`; the timestamps and everything else the store computes are ignored on
// the way in and returned on the way out.
//
// The row is CREATED when absent, so declaring a lease and advancing one are the same
// call, and the id is the key: whatever apply writes into ID is overwritten with it.
//
// Releases are stamped here because they are the store's to say: a write that drops a path
// from WritePaths IS the release announcement, and reading the previous write set and
// writing the next one has to be one step or a concurrent put decides which release
// happened.
//
// apply runs while the lock is held, so it must not touch the store, and it cannot fail:
// anything that could be rejected belongs in the caller, before the call.
//
// ctx reaches the release digests and nothing else. A cancelled call still WRITES the row,
// since the merge is already done, but it stops hashing files rather than reading the disk
// with the store's lock held.
func (s *Store) Update(ctx context.Context, id string, apply func(*types.Job)) (types.Job, error) {
	return s.mutate(ctx, id, asDeclaration, func(cur *types.Job, _ bool, _ int64) error {
		apply(cur)
		return nil
	})
}

// MaxUnattributedWrites bounds how many outside-written paths one row keeps.
//
// A cap rather than a growing list because this is written from the guard, which runs on every
// file write a host makes: without one, a plan left declared over a long editing session would
// accumulate a row entry per save until the ledger was mostly this. The newest are kept, since a
// lease asking what moved is asking about the tree it faces now.
const MaxUnattributedWrites = 32

// RecordUnattributedWrite notes that somebody outside lease id wrote one of its write paths,
// with the content they left behind.
//
// ONE ROW PER PATH, newest wins: a person saves a file a dozen times while an agent works,
// and the twelfth save is the only one that describes the tree.
//
// A missing row is not an error: the lease may have ended between the grading and this call,
// and a write graded against a plan that has since finished is nothing to report to anybody.
func (s *Store) RecordUnattributedWrite(ctx context.Context, id, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	// An OBSERVATION, and the only ungraded write here: the row it lands on is by
	// definition somebody else's, so grading it would throw the notice away precisely when
	// it matters, which is a worker writing a path another lease holds.
	_, err := s.mutate(ctx, id, asObservation, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: no such lease %q: %w", id, errNoSuchJob)
		}
		next := make([]types.JobUnattributedWrite, 0, len(cur.Unattributed)+1)
		for _, w := range cur.Unattributed {
			if w.Path != path {
				next = append(next, w)
			}
		}
		next = append(next, types.JobUnattributedWrite{
			Path: path, Digest: s.fileDigest(ctx, path), At: now,
		})
		if len(next) > MaxUnattributedWrites {
			next = next[len(next)-MaxUnattributedWrites:]
		}
		cur.Unattributed = next
		return nil
	})
	if errors.Is(err, errNoSuchJob) {
		return nil
	}
	return err
}

// errNoSuchJob is internal to RecordUnattributedWrite: mutate creates a row that is not there, and
// this is how the apply func declines that without inventing a lease nobody declared.
var errNoSuchJob = errors.New("job: no such lease")

// MaxJobEntries is how many entries one job takes. Past it, the job wants its holder
// resumed or its lease ended, not a third visitor.
const MaxJobEntries = 2

// Enter records that the caller will write rel, inside the write paths of live job id,
// once. The guard then lets one write by somebody other than the holder through and
// stamps it consumed; see [Store.ConsumeEntry]. A put carrying `enter` is the same write.
//
// Refused, and nothing is written, when the job is not live, when the caller is its
// holder, when rel is outside its write paths (that wants widening, not entering), when
// an entry for rel is still open, and when the job already took [MaxJobEntries].
func (s *Store) Enter(ctx context.Context, id, rel string) (types.Job, error) {
	rel, err := entryPath(rel)
	if err != nil {
		return types.Job{}, err
	}
	return s.Update(ctx, id, func(u *types.Job) { u.Entries = append(u.Entries, types.JobEntry{Path: rel}) })
}

// cleanPath is a path a version control or a person named, as a slash path.
func cleanPath(p string) string {
	return path.Clean(filepath.ToSlash(strings.TrimSpace(p)))
}

// entryPath cleans the path an entry names, refusing one that is not workspace-relative.
func entryPath(rel string) (string, error) {
	clean := cleanPath(rel)
	if clean == "." || path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("job: enter %q: name one workspace-relative path inside the job's write paths", rel)
	}
	return clean, nil
}

// requestsEntry reports a write whose only change is one unstamped entry appended to
// prev's, which is how Enter and a put carrying `enter` reach mutate.
func requestsEntry(prev, next types.Job) bool {
	n := len(prev.Entries)
	if len(next.Entries) != n+1 || next.Entries[n].At != 0 {
		return false
	}
	for k := range n {
		if !reflect.DeepEqual(next.Entries[k], prev.Entries[k]) {
			return false
		}
	}
	return true
}

// authorizeEntry grades an entry: prev is the row before it and next carries the one
// entry appended to it.
func authorizeEntry(actor Actor, id string, prev, next types.Job, exists bool, rows []types.Job) error {
	entry := next.Entries[len(next.Entries)-1]
	switch {
	case !exists:
		return fmt.Errorf("%q: there is nothing to enter: %w", id, ErrUnknownJob)
	case len(changedFields(prev, next)) > 0:
		return fmt.Errorf("job: an entry declares nothing, and this write also changes %s", strings.Join(changedFields(prev, next), ", "))
	case actor.Lease == id:
		return refuse(actor, id, "a holder writes its own paths and never enters them")
	case actor.Bound() && !slices.ContainsFunc(types.JobAncestors(rows, id), func(r types.Job) bool { return r.ID == actor.Lease }):
		return refuse(actor, id, "a worker enters only a job forked beneath its own")
	case !prev.State.Live():
		return fmt.Errorf("job: %s is %s, so its paths are free to write and there is nothing to enter", id, prev.State)
	}
	if _, ok := matching(prev.WritePaths, entry.Path); !ok {
		return fmt.Errorf("job: %s is outside the write paths of %s (%s), a path the job does not own is widened into it, never entered",
			entry.Path, id, strings.Join(prev.WritePaths, ", "))
	}
	if open, ok := OpenEntry(prev, entry.Path); ok {
		return fmt.Errorf("job: %s already has an entry for %s, recorded at %s and not yet written, write it before entering again",
			id, open.Path, time.Unix(open.At, 0).UTC().Format(time.RFC3339))
	}
	if len(prev.Entries) >= MaxJobEntries {
		return fmt.Errorf("job: %s has taken its %d entries, resume its holder to make the change, or end its lease with `magus job exit %s`",
			id, MaxJobEntries, id)
	}
	return nil
}

// EntryAdvice is what an entry is answered with: what the entrant may now do, and once.
func EntryAdvice(row types.Job, rel string) string {
	return fmt.Sprintf("entered %s on %s (%d of %d entries): your next write to it passes once its holder has been idle for a minute, and is recorded on the job",
		rel, row.ID, len(row.Entries), MaxJobEntries)
}

// Entries is the entries recorded on job id in rows, nil when rows holds no such job.
func Entries(rows []types.Job, id string) []types.JobEntry {
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 {
		return rows[i].Entries
	}
	return nil
}

// OpenEntry is the first entry on row that covers rel and has not been consumed.
func OpenEntry(row types.Job, rel string) (types.JobEntry, bool) {
	for _, e := range row.Entries {
		if e.Consumed == 0 && covers(e.Path, rel) {
			return e, true
		}
	}
	return types.JobEntry{}, false
}

// ConsumeEntry stamps the open entry on job id covering rel as used. An observation, like
// RecordUnattributedWrite: the guard records the write it just let through. A job with no
// open entry for rel is left alone.
func (s *Store) ConsumeEntry(ctx context.Context, id, rel string) error {
	_, err := s.mutate(ctx, id, asObservation, func(cur *types.Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: no such lease %q: %w", id, errNoSuchJob)
		}
		for i, e := range cur.Entries {
			if e.Consumed == 0 && covers(e.Path, rel) {
				cur.Entries[i].Consumed = now
				return nil
			}
		}
		return nil
	})
	if errors.Is(err, errNoSuchJob) {
		return nil
	}
	return err
}

// boundaryFields are the declared fields a widen or narrow changes, in changedFields'
// spelling.
var boundaryFields = []string{"write_paths", "deny_paths", "read_paths"}

// widensInPlace reports a declaration that changes a live row's boundaries and nothing
// else it declares, while resetting a state its holder already moved past declared: a
// re-fork that only moves paths. Such a write keeps the stored state, since resetting it
// would hand the holder's job out again. A deadline may move with it, because a fork
// re-stamps jobs.default_timeout. A re-fork that changes no boundary still resets the
// state, which is how a rejected job is handed out again.
func widensInPlace(prev, next types.Job) bool {
	if !prev.State.Live() || prev.State == types.StateDeclared || next.State != types.StateDeclared {
		return false
	}
	moved := false
	for _, field := range changedFields(prev, next) {
		switch {
		case slices.Contains(boundaryFields, field):
			moved = true
		case field != "state" && field != "deadline":
			return false
		}
	}
	return moved
}

// keepDerived carries what the fork derived for prev onto a whole-row record that omits it,
// so a projection never hardens a path no person decided: prev's soft paths row still
// writes, unless row names soft_paths for a path prev held, and the [IntegratorDeviations].
func keepDerived(row *types.Job, prev types.Job) {
	if !slices.ContainsFunc(row.SoftPaths, func(p string) bool { return slices.Contains(prev.WritePaths, p) }) {
		for _, p := range prev.SoftPaths {
			if slices.Contains(row.WritePaths, p) && !slices.Contains(row.SoftPaths, p) {
				row.SoftPaths = append(row.SoftPaths, p)
			}
		}
	}
	for _, w := range prev.Deviations {
		if w.Reason == integratorRegenerates && !slices.ContainsFunc(row.Deviations, func(have types.Deviation) bool {
			return have.Code == w.Code && have.Subject == w.Subject
		}) {
			row.Deviations = append(row.Deviations, w)
		}
	}
}

// mutate is the locked read-modify-write [Store.Update], [Store.Exec] and [Wait] share, and
// the only place a row is written row-wise. kind says what the write is; see
// [grading].
//
// apply gets what Update does not need: exists, because Update creates a row while Exec
// and Wait refuse one nobody declared, and now, the one clock read the write is stamped from. It
// may fail, which is what lets that refusal be decided under the lock; nothing is written
// when it does.
func (s *Store) mutate(ctx context.Context, id string, kind grading, apply func(cur *types.Job, exists bool, now int64) error) (types.Job, error) {
	return s.mutateSeeing(ctx, id, kind, func(cur *types.Job, exists bool, now int64, _ []types.Job) error {
		return apply(cur, exists, now)
	})
}

// mutateSeeing is mutate whose apply also sees every stored row.
// The rows are read under the same lock; apply must not change them.
func (s *Store) mutateSeeing(ctx context.Context, id string, kind grading, apply func(cur *types.Job, exists bool, now int64, rows []types.Job) error) (types.Job, error) {
	if strings.TrimSpace(id) == "" {
		return types.Job{}, ErrNoID
	}
	// Read before the lock, like Exec's checkpoint: the trail may be another checkout's, and
	// only a declaration of an ended row folds what they say.
	var denials []types.JobDenial
	var denialsKnown bool
	if kind == asDeclaration {
		denials, denialsKnown = s.endedAttemptDenials(id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	actor := s.Actor()
	rootHead := sync.OnceValue(func() string { return s.committedRevision(ctx) })
	var stored types.Job
	var ended endings
	var folded *types.Attempt
	err := s.update(ctx, func(f *jobsFile) error {
		ended, folded = endings{}, nil
		i := slices.IndexFunc(f.Jobs, func(e types.Job) bool { return e.ID == id })
		var prev types.Job
		if i >= 0 {
			prev = f.Jobs[i]
			if err := readOnly(prev); err != nil {
				return err
			}
		}
		row := prev.Clone()
		row.ID = id
		now := time.Now().Unix()
		if aerr := apply(&row, i >= 0, now, f.Jobs); aerr != nil {
			return aerr
		}
		row = row.Clone()
		row.ID = id
		// Every door into the store declares here, so a record that names no state is
		// stored as declared: the guard reads an empty state as ended.
		if kind == asDeclaration && row.State == "" {
			row.State = types.StateDeclared
		}
		if kind == asDeclaration && i >= 0 && widensInPlace(prev, row) {
			row.State = prev.State
		}
		entering := kind == asDeclaration && requestsEntry(prev, row)
		switch {
		case entering:
			if aerr := authorizeEntry(actor, id, prev, row, i >= 0, f.Jobs); aerr != nil {
				return aerr
			}
			e := &row.Entries[len(row.Entries)-1]
			e.By, e.At, e.Consumed = trail.StampOrigin(ctx, types.Origin{}), now, 0
		case kind.graded():
			if aerr := authorizeRow(actor, id, kind, prev, row, i >= 0, f.Jobs); aerr != nil {
				return aerr
			}
		}
		// An ended row declared again is a fork of the job's next attempt.
		refork := kind == asDeclaration && i >= 0 && prev.State.Terminal() && row.State.Live()
		// The store's own record of what HAPPENED is not a caller's to send. On an
		// existing row it is carried forward, so a whole-row write neither forges nor
		// erases a registration; a row a BOUND caller creates carries none, since a child
		// handed a registration it never made writes without ever reporting its base.
		switch {
		case i >= 0:
			if kind != asExec {
				row.ReportedBase, row.BaseVerdict, row.Registered = prev.ReportedBase, prev.BaseVerdict, prev.Registered
				row.CheckoutRoot = prev.CheckoutRoot
			}
			// An ended row declared again is handed out afresh: its old holder gave it up,
			// and a checkout left on it would refuse every new taker and, once that
			// worktree is gone, end the row again on the next sweep.
			if refork {
				all, aerr := s.Attempts()
				if aerr != nil {
					return aerr
				}
				tries := types.NewJobAttempts(all)
				if aerr := refuseAttempt(id, tries.LiveN(id), row); aerr != nil {
					return aerr
				}
				fold := foldAttempt(prev, tries.Live(id), tries.Prior(id), denials, denialsKnown, now)
				folded = &fold
				row.ReportedBase, row.BaseVerdict, row.Registered, row.CheckoutRoot = "", "", 0, ""
			}
			if kind != asObservation {
				row.Unattributed, row.ReleaseRequests = prev.Unattributed, prev.ReleaseRequests
			}
			if refork {
				row.ReleaseRequests = nil
			}
			if kind != asObservation && !entering {
				row.Entries = prev.Entries
			}
		case actor.Bound():
			row.ReportedBase, row.BaseVerdict, row.Registered, row.Unattributed = "", "", 0, nil
			row.CheckoutRoot = ""
		}
		if i < 0 {
			row.Entries, row.ReleaseRequests = nil, nil
		}
		row.ReleaseRequests = heldRequests(row)
		if kind == asDeclaration && i >= 0 {
			keepDerived(&row, prev)
		}
		// Hardening is one-way: a re-declaration cannot soften a path the holder already
		// wrote, and a soft path that is no write path claims nothing.
		row.SoftPaths = slices.DeleteFunc(row.SoftPaths, func(p string) bool {
			return !slices.Contains(row.WritePaths, p) ||
				i >= 0 && slices.Contains(prev.WritePaths, p) && !slices.Contains(prev.SoftPaths, p)
		})
		// The store writes every reason: the sweep's, and the one a write ending a live row
		// as no_return gets from what the row had become, and a pass its holder's result made.
		// A row brought back to life has none.
		row.EndReason = prev.EndReason
		row.Landed = prev.Landed && !row.State.Live()
		switch {
		case row.State.Live():
			row.EndReason = ""
		case i >= 0 && prev.State.Live() && row.State == types.StateNoReturn:
			all, aerr := s.Attempts()
			if aerr != nil {
				return aerr
			}
			row.EndReason = endedReason(prev, types.NewJobAttempts(all).Result(id) != nil, s.endsOwnRow(prev))
		case i >= 0 && prev.State.Live() && row.State == types.StatePass && gradedByHolder(prev):
			row.EndReason = returnedReason
		}
		row.Updated = now
		// A wait that refuses records why and leaves the row where it was, heartbeat
		// included: the waiter is not the holder.
		if (kind == asObservation || kind == asAdvance || entering || (kind == asVerdict && row.State == prev.State)) && i >= 0 {
			row.Updated = prev.Updated
		}
		row.Created = now
		// The record keeps what a newer magus stored; see [jobsFile].
		row.Unknown = prev.Unknown
		row.ModelTier = prev.ModelTier
		// Only RecordWorktree and EndWorktrees write the worktree record, and Exec rebinding
		// an untaken row from an adopted checkout to another. The next attempt keeps a
		// checkout that still stands and forgets one magus removed.
		if kind != asExec || !rebindsAdopted(prev, row) {
			row.Worktree = prev.Clone().Worktree
		}
		if refork && row.Worktree != nil {
			row.Worktree.Kept = ""
			if row.Worktree.Removed != 0 {
				row.Worktree = nil
			}
		}
		if s.modelTiers != nil && (i < 0 || prev.Agent.RequestedModel() != row.Agent.RequestedModel()) {
			assigned, aerr := s.assignModelTierWithHistory(ctx, row, f.Jobs)
			if aerr != nil {
				return aerr
			}
			row.ModelTier = &assigned
		}
		if i < 0 || prev.Agent.RequestedModel() != row.Agent.RequestedModel() || prev.Parent != row.Parent {
			if aerr := refuseAboveParent(s.modelTiers, s.spawnRule, row, f.Jobs); aerr != nil {
				return aerr
			}
		}
		// The counters are the store's: a whole-row write cannot set them, and only a
		// write that adds write paths to a row its holder already took counts a widening.
		row.Widenings = prev.Widenings
		if kind != asClaim {
			row.Claims = prev.Claims
		}
		if kind != asObservation {
			row.ReworkRounds = prev.ReworkRounds
		}
		if kind != asNarrowing && i >= 0 && (prev.State == types.StateRunning || prev.State == types.StateExited) &&
			slices.ContainsFunc(row.WritePaths, func(p string) bool { return !slices.Contains(prev.WritePaths, p) }) {
			row.Widenings++
		}
		// The generation and the evidence are the store's too. Only a wait
		// records a rejection. A fork of the next attempt starts the
		// evidence and the counters over.
		row.Generation = nextGeneration(prev, row, i >= 0, kind)
		if kind != asVerdict {
			row.Rejection = prev.Rejection
		}
		if refork {
			row.Attempt, row.GateAttempts, row.Rejection = nil, nil, nil
			row.Integration, row.WaiterEvidence = nil, nil
			row.ReworkRounds, row.Widenings, row.Claims = 0, 0, nil
		}
		row.Requires = requiredFeatures(row)
		if kind != asNarrowing {
			row.Releases = s.releases(ctx, prev, row, now, prev.Registered != 0 && actor.Lease != id)
		}
		switch {
		case kind == asAdvance:
		case kind == asDeclaration && (i < 0 || refork):
			row.Authority = forkAuthority(actor, f.Jobs, rootHead)
		default:
			row.Authority = prev.Authority
		}
		if i >= 0 {
			row.Created = prev.Created
			row.RegisteredBy = prev.RegisteredBy
			f.Jobs[i] = row
		} else {
			row.RegisteredBy = trail.StampOrigin(ctx, types.Origin{})
			f.Jobs = append(f.Jobs, row)
		}
		if i >= 0 && !prev.State.Terminal() && row.State.Terminal() {
			if err := ended.hold(s, f.Jobs, row); err != nil {
				return err
			}
		}
		stored = row.Clone()
		return nil
	})
	if err != nil {
		return types.Job{}, err
	}
	// After the row, so a refused write folds nothing.
	if folded != nil {
		if err := s.appendAttempts(ctx, *folded); err != nil {
			return stored, fmt.Errorf("%s was forked again, and its last attempt was not folded: %w", id, err)
		}
	}
	if err := ended.record(ctx, time.Now().UnixMilli()); err != nil {
		return stored, err
	}
	return stored, nil
}

// endedAttemptDenials are the guard's denials of the holder of job id since its last
// attempt began, for the fold a fork of the ended row makes, and whether the trail still
// reaches back to that start. Only an ended row is read. A dry run, or a trail that cannot
// be placed or read, gives none and unknown, which the fold records as unknown.
func (s *Store) endedAttemptDenials(id string) ([]types.JobDenial, bool) {
	if s.dryRun {
		return nil, false
	}
	f, err := s.read()
	if err != nil {
		return nil, false
	}
	i := slices.IndexFunc(f.Jobs, func(r types.Job) bool { return r.ID == id })
	if i < 0 || !f.Jobs[i].State.Terminal() {
		return nil, false
	}
	row := f.Jobs[i]
	base, err := s.holderTrail(row.CheckoutRoot)
	if err != nil {
		return nil, false
	}
	all, err := s.Attempts()
	if err != nil {
		return nil, false
	}
	denials, known, err := trail.LeaseDenials(base, id, attemptStart(row, types.NewJobAttempts(all)), maxDenialsRead)
	if err != nil {
		return nil, false
	}
	return denials, known
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

// List returns every row in the order it was first recorded. The rows are copies, so a
// caller may keep or mutate them without reaching back into the file's next read.
//
// Every read SWEEPS first: a live row magus can prove is dead is ended as no_return with
// its [types.Job.EndReason], and one line per ended row goes to stderr. See [sweeper.dead]
// for the rules. The sweep is the one write a read makes, and only when a row is dead, so
// a plan with nothing to end is still read without either lock.
func (s *Store) List() ([]types.Job, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	if f, err = s.sweep(f); err != nil {
		return nil, err
	}
	return cloneRows(f.Jobs), nil
}

// endsOwnRow reports whether this store's caller holds row, so ending it is its holder's.
func (s *Store) endsOwnRow(row types.Job) bool {
	return s.Actor().Lease == row.ID || (row.CheckoutRoot != "" && row.CheckoutRoot == s.root)
}

// unswept is [Store.List] without the sweep, for a verb scoped to the rows it names: ending
// one job must not end, or probe the landed work of, every other row in the plan.
func (s *Store) unswept() ([]types.Job, error) {
	f, err := s.read()
	if err != nil {
		return nil, err
	}
	rows := cloneRows(f.Jobs)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, row := range rows {
		if ended, ok := s.dryEnded[row.ID]; ok {
			rows[i] = ended.Clone()
		}
	}
	return rows, nil
}

func (s *Store) endDry(row types.Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dryEnded == nil {
		s.dryEnded = map[string]types.Job{}
	}
	s.dryEnded[row.ID] = row.Clone()
}

func cloneRows(rows []types.Job) []types.Job {
	out := make([]types.Job, len(rows))
	for i, row := range rows {
		out[i] = row.Clone()
	}
	return out
}

// Clear drops every row, which is how a fresh plan starts, ARCHIVING what it dropped to a
// timestamped sibling file (jobs-<UTC time>.jsonl) first, and returns how many rows it dropped. Clearing an empty
// or absent ledger is not an error and archives nothing: the caller asked for an empty
// ledger and got one.
//
// The COUNT comes from inside the lock. Both doors report it, and a preceding List to
// derive it undercounts a row a concurrent registrant appended between the two calls,
// which is the one row its author most needs to hear about.
//
// The archive is what makes this recoverable rather than merely reported: one clear wipes
// rows the caller did not write, and nothing else would let them read the plan again.
// Nothing reads these files back, which is the point: they are for the person who has to
// work out what the plan was.
//
// A bound worker is refused: see [authorizeClear]. ctx bounds the wait for the lock and
// nothing else.
func (s *Store) Clear(ctx context.Context) (int, error) {
	if err := authorizeClear(s.Actor()); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var dropped int
	err := s.update(ctx, func(f *jobsFile) error {
		if len(f.Jobs) > 0 {
			if err := s.archive(*f); err != nil {
				return err
			}
		}
		dropped = len(f.Jobs)
		*f = f.header()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return dropped, nil
}

// archive writes the rows a clear or a delete is about to drop beside the ledger, in the
// ledger's own shape and named for the moment they were dropped. A second drop within the
// same second overwrites the first archive rather than growing a suffix scheme: two plans
// wiped in one second is one mistake being repeated, and the rows worth keeping are the
// ones there now.
func (s *Store) archive(f jobsFile) error {
	f.Migration = len(s.migrations)
	lines, err := encodeJobs(f)
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Dir(s.path)
	if err := s.archiveAttempts(filepath.Join(dir, fmt.Sprintf("jobs-%s.attempts.jsonl", stamp)), f.Jobs); err != nil {
		return err
	}
	return file.RewriteLines(filepath.Join(dir, fmt.Sprintf("jobs-%s.jsonl", stamp)), lines, 0o600)
}

// update runs fn over the ledger as it stands under the file lock beside jobs.jsonl, so a
// read-modify-write cannot interleave with one from another magus process, and writes what
// fn leaves, with a retention pass when one is due (see [Store.retainDue]). fn returning an
// error, file.ErrSkipWrite included, writes nothing.
//
// The lock wait is BOUNDED, where the workspace project locks (magus/lock.go) wait forever
// with a heartbeat: their holder is a build that may run for hours, and a ledger write is a
// small append, so a longer wait means a stuck holder.
func (s *Store) update(ctx context.Context, fn func(*jobsFile) error) error {
	if s.err != nil {
		return s.err
	}
	// Made here, before the lock and file helpers create it world-readable.
	// Rows carry a repository's plan and its checkout paths.
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("job: create store %s: %w", filepath.Dir(s.path), err)
	}
	return s.locked(ctx, func() error {
		f, log, err := s.readFile(false)
		if err != nil {
			return err
		}
		gone := make(map[string]bool, len(f.Jobs))
		for _, row := range f.Jobs {
			gone[row.ID] = true
		}
		if err := fn(&f); err != nil {
			if errors.Is(err, file.ErrSkipWrite) {
				return nil
			}
			return err
		}
		retained := f.Retained
		if err := s.retainDue(&f, s.now()); err != nil {
			return err
		}
		if retained != 0 && f.Retained != retained {
			s.pruneFootprints(f.Jobs)
		}
		if err := s.write(f, log); err != nil {
			return err
		}
		for _, row := range f.Jobs {
			delete(gone, row.ID)
		}
		return s.dropAttempts(ctx, gone)
	})
}

// write records f against log, the file it was read from: the lines of the rows that
// changed are appended, and the file is rewritten to one line per row instead when a row
// was dropped, the header changed, the file ended in a line no writer finished, or the
// appended lines would make it [file.CompactDue].
func (s *Store) write(f jobsFile, log jobsLog) error {
	f.Migration = len(s.migrations)
	lines, err := encodeJobs(f)
	if err != nil {
		return err
	}
	header, rows := lines[0], lines[1:]
	var changed [][]byte
	kept := 0
	for i, row := range f.Jobs {
		prev, ok := log.rows[row.ID]
		if ok {
			kept++
		}
		if !bytes.Equal(rows[i], prev) {
			changed = append(changed, rows[i])
		}
	}
	dropped := kept < len(log.rows)
	if len(changed) == 0 && !dropped && bytes.Equal(header, log.header) && !log.partial {
		return nil
	}
	sameHeader := bytes.Equal(header, log.header)
	if !dropped && sameHeader && !log.partial && !file.CompactDue(log.lines+len(changed), len(f.Jobs)) {
		return file.AppendLines(s.path, changed, 0o600)
	}
	if log.partial {
		out := s.notices
		if out == nil {
			out = os.Stderr
		}
		fmt.Fprintf(out, "job: %s ended in a line no writer finished; the rewrite dropped it\n", s.path)
	}
	return file.RewriteLines(s.path, lines, 0o600)
}

// pruneFootprints removes the footprint cache of every checkpoint revision no live row in
// rows declares, riding the retention pass. Best effort, as for the archives it drops.
func (s *Store) pruneFootprints(rows []types.Job) {
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

// now is the Store's clock.
func (s *Store) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// read loads the file. An absent file is an empty ledger, not a failure: nothing has
// been recorded yet for this repository.
func (s *Store) read() (jobsFile, error) {
	f, _, err := s.readFile(true)
	return f, err
}

// readFile parses the ledger and the log a write compares against.
//
// A reader holding no lock may catch an append mid-write, so with wait it reads again for
// up to [tailWait] while the file ends in an unfinished line, and reports one that stays
// unfinished. A writer holds the lock, so an unfinished line it finds was left by a writer
// that died, and the write drops it.
//
// A row newer than this magus is read like any other. The members it does
// not declare ride in its stored record and go back out on the next write.
// A row requiring a feature this magus
// lacks is read too, and only a write to it is refused (see readOnly).
//
// A file behind [jobMigrations] is migrated first, once, under the lock (see
// [Store.migrate]); without wait the caller already holds it.
// A job's checkout refuses it instead (see [Store.refuseCheckoutMigration]).
func (s *Store) readFile(wait bool) (jobsFile, jobsLog, error) {
	f, log, lines, err := s.parse(wait)
	if err != nil || f.Migration == len(s.migrations) || log.lines == 0 {
		return f, log, err
	}
	if err := s.refuseCheckoutMigration(f.Migration, lines); err != nil {
		return jobsFile{}, jobsLog{}, err
	}
	if err := s.migrate(f.Migration, !wait); err != nil {
		return jobsFile{}, jobsLog{}, err
	}
	f, log, _, err = s.parse(wait)
	return f, log, err
}

// refuseCheckoutMigration refuses migrating a file at migration found from
// a live job's own checkout. Every checkout shares the file. A row only
// taken here does not count: the root's own checkout is one.
func (s *Store) refuseCheckoutMigration(found int, lines []file.Line) error {
	if s.root == "" || found >= len(s.migrations) {
		return nil
	}
	rows, err := s.unmigratedRows(found, lines)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if !row.State.Live() || !row.Worktree.Standing() || !within(s.root, row.Worktree.Root) {
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
// types.Job.
func (s *Store) unmigratedRows(found int, lines []file.Line) ([]types.Job, error) {
	flat := found <= slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == recordsEnveloped })
	var rows []types.Job
	at := map[string]int{}
	for _, line := range lines {
		var row types.Job
		if flat {
			if err := json.Unmarshal(line.Text, &row); err != nil {
				return nil, types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
			}
		} else {
			var r jobRecord
			err := json.Unmarshal(line.Text, &r)
			if err == nil && r.Kind == jobType.Kind {
				row, err = r.job()
			}
			if err != nil {
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

// migrate brings the file at migration found to the last of s.migrations, taking the lock
// unless held. A file past them is refused with MGS3051 before anything is locked.
func (s *Store) migrate(found int, held bool) error {
	st := migrate.Store{
		Name:       "job store",
		Path:       s.path,
		Header:     headerEntry,
		Migrations: s.migrations,
	}
	refuse := func(err error) error {
		if newer, ok := errors.AsType[*migrate.NewerError](err); ok {
			return types.DiagnosticErrorf(types.JobStoreWriterOlder, "%s", newer.Error())
		}
		return err
	}
	if err := st.Check(found); err != nil {
		return refuse(err)
	}
	apply := func() error {
		if i := slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == triesMoved }); i >= found {
			if err := s.moveTries(context.Background()); err != nil {
				return fmt.Errorf("moving tries to %s: %w", attemptsFileName, err)
			}
		}
		if i := slices.IndexFunc(s.migrations, func(m migrate.Migration) bool { return m.Name == recordsEnveloped }); i >= found {
			if err := s.envelopeAttempts(); err != nil {
				return fmt.Errorf("enveloping %s: %w", attemptsFileName, err)
			}
		}
		res, err := st.Apply()
		if err != nil {
			return refuse(err)
		}
		if res.Migrated() {
			out := s.notices
			if out == nil {
				out = os.Stderr
			}
			fmt.Fprintf(out, "job: %s\n", res)
		}
		return nil
	}
	if held {
		return apply()
	}
	return s.locked(context.Background(), apply)
}

// parse reads the file as it stands; see [Store.readFile]. A file at
// another migration than this build's has no rows decoded: they are
// returned as lines.
func (s *Store) parse(wait bool) (jobsFile, jobsLog, []file.Line, error) {
	if s.err != nil {
		return jobsFile{}, jobsLog{}, nil, s.err
	}
	if err := s.refuseDocument(); err != nil {
		return jobsFile{}, jobsLog{}, nil, err
	}
	deadline := time.Now().Add(tailWait)
	var read file.Lines
	for {
		var err error
		if read, err = file.ReadLines(s.path); err != nil {
			return jobsFile{}, jobsLog{}, nil, fmt.Errorf("job: %w", err)
		}
		if !read.Partial || !wait {
			break
		}
		if time.Now().After(deadline) {
			return jobsFile{}, jobsLog{}, nil, fmt.Errorf("job: %s ends in a line no writer finished, so the next write to the plan drops it", s.path)
		}
		time.Sleep(time.Millisecond)
	}
	f := jobsFile{stored: map[string]jobRecord{}}
	log := jobsLog{rows: make(map[string][]byte, len(read.Lines)), lines: len(read.Lines), partial: read.Partial}
	var texts []file.Line
	for _, line := range read.Lines {
		var probe struct {
			apiversion.TypeMeta
			ID   json.RawMessage `json:"id"`
			Spec json.RawMessage `json:"spec"`
		}
		if err := json.Unmarshal(line.Text, &probe); err != nil {
			return jobsFile{}, jobsLog{}, nil, types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
		}
		if probe.ID == nil && probe.Spec == nil {
			header := jobsFile{}
			if err := json.Unmarshal(line.Text, &header); err != nil {
				return jobsFile{}, jobsLog{}, nil, types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
			}
			header.stored, log.header = f.stored, line.Text
			f = header
			continue
		}
		texts = append(texts, line)
	}
	if f.Migration != len(s.migrations) {
		return f, log, texts, nil
	}
	if f.TypeMeta != jobListType && log.header != nil {
		return jobsFile{}, jobsLog{}, nil, s.refuseRecord(jobListType, f.TypeMeta, 1)
	}
	at := map[string]int{}
	for _, line := range texts {
		var r jobRecord
		if err := json.Unmarshal(line.Text, &r); err != nil {
			return jobsFile{}, jobsLog{}, nil, types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
		}
		row, err := r.job()
		if refused, ok := errors.AsType[*apiversion.RefusedError](err); ok {
			return jobsFile{}, jobsLog{}, nil, s.refuseRecord(refused.Want, refused.Got, line.N)
		}
		if err != nil {
			return jobsFile{}, jobsLog{}, nil, types.WrapDiagnostic(types.StoreLineUndecodable, err, "%s:%d: %v", s.path, line.N, err)
		}
		log.rows[row.ID] = line.Text
		f.stored[row.ID] = r
		if i, ok := at[row.ID]; ok {
			f.Jobs[i] = row
			continue
		}
		at[row.ID] = len(f.Jobs)
		f.Jobs = append(f.Jobs, row)
	}
	known := make([]apiversion.Feature, len(types.JobFeatures))
	for i, k := range types.JobFeatures {
		known[i] = apiversion.Feature(k)
	}
	if lacks := f.Metadata.Unmet(recordGroup, known); lacks != nil {
		names := make([]string, len(lacks))
		for i, l := range lacks {
			names[i] = string(l)
		}
		return jobsFile{}, jobsLog{}, nil, types.DiagnosticErrorf(types.JobStoreWriterOlder, "%s requires %s, which this magus (%s) lacks, update magus",
			s.path, quoteAll(names), RecordAPIVersion)
	}
	return f, log, nil, nil
}

// encodeJobs renders the ledger as its lines: the header first, then one line per row.
// Every row, touched this call or not, keeps what its stored record carries.
func encodeJobs(f jobsFile) ([][]byte, error) {
	f.TypeMeta = jobListType
	header, err := json.Marshal(f)
	if err != nil {
		return nil, err
	}
	lines := make([][]byte, 0, len(f.Jobs)+1)
	lines = append(lines, header)
	for _, row := range f.Jobs {
		var stored *jobRecord
		if r, ok := f.stored[row.ID]; ok {
			stored = &r
		}
		line, err := json.Marshal(recordJob(row, stored))
		if err != nil {
			return nil, fmt.Errorf("job: %s: %w", row.ID, err)
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// Delete removes ONE row and returns it, or reports that no such row exists.
//
// A row that is over is still a row: `job exit` moves a job to a terminal state, which is
// the RECORD of what happened and is what a later reader wants. Delete is for a row that
// should never have been written -- a demo, a typo, a plan abandoned before it began --
// and it is the only way to take one out without Clear taking every other lease's row
// with it.
//
// It refuses a TERMINAL row by default. Deleting the record of a job that actually ran
// destroys the only account of it, and the caller who wants that says so with force. A
// live row is the opposite case: nothing has happened yet, so there is nothing to lose.
//
// A checkout the row owns is ended first, as `job exit` ends it: removed when it is clean
// and every commit in it is on a branch, else the row stays with it and the error says why.
//
// The dropped row is archived first, as Clear archives, so a delete is recoverable from
// the sibling file rather than only from whatever the caller remembers. Archives last
// jobs.retain; see [Store.dropExpiredFiles].
func (s *Store) Delete(ctx context.Context, id string, force bool) (types.Job, error) {
	id = strings.TrimSpace(id)
	if err := authorizeDelete(s.Actor(), id); err != nil {
		return types.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var dropped types.Job
	err := s.update(ctx, func(f *jobsFile) error {
		i := slices.IndexFunc(f.Jobs, func(row types.Job) bool { return row.ID == id })
		if i < 0 {
			return fmt.Errorf("job: there is no job %q", id)
		}
		dropped = f.Jobs[i]
		if err := readOnly(dropped); err != nil {
			return err
		}
		if dropped.State.Terminal() && !force {
			return fmt.Errorf("job: %s is %s, and that row is the record of what happened:"+
				" delete it anyway with --force, or leave it where a later reader can find it",
				id, dropped.State)
		}
		if dropped.Worktree.Standing() && dropped.Worktree.Made {
			here, _ := filepath.Abs(s.root)
			others := slices.DeleteFunc(slices.Clone(f.Jobs), func(row types.Job) bool { return row.ID == id })
			if kept := endCheckout(ctx, others, here, dropped, *dropped.Worktree, nil); kept != "" {
				return fmt.Errorf("job: %s owns the checkout %s, which stays, so the row stays with it: %s",
					id, dropped.Worktree.Root, kept)
			}
			dropped = dropped.Clone()
			dropped.Worktree.Removed = time.Now().Unix()
		}
		archived := f.header()
		archived.Jobs, archived.stored = []types.Job{dropped}, f.stored
		if err := s.archive(archived); err != nil {
			return err
		}
		f.Jobs = slices.Delete(f.Jobs, i, i+1)
		return nil
	})
	if err != nil {
		return types.Job{}, err
	}
	return dropped, nil
}
