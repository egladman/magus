package job

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
)

// ErrNoID reports a lease with no id. The id is what Update upserts on, so a row without
// one could never be updated or referred to again: it is unaddressable, not merely
// incomplete.
var ErrNoID = errors.New("job: a lease needs an id")

// ErrMaxAttempts is a fork of an ended job that would start an attempt past its
// max_attempts. Forking again unchanged meets the same cap.
var ErrMaxAttempts = errors.New("job: max_attempts reached")

// ErrUnknownJob is a write or a read naming a job the store does not hold.
var ErrUnknownJob = errors.New("job: unknown lease")

// ErrSkipWrite, returned by a [Store.Rewrite] fn, ends the rewrite without writing and
// without an error: the fn looked and found nothing to change.
var ErrSkipWrite = errors.New("job: skip this write")

const (
	jobsFileName     = "jobs.jsonl"
	attemptsFileName = "attempts.jsonl"
	// grantsFileName sits beside jobsFileName, so a grant lives and dies with the plan it
	// names.
	grantsFileName = "grants.jsonl"
	// gradesFileName holds the runs the store recorded and the grades it gave from them.
	gradesFileName = "grades.jsonl"
	// documentFileName is the one JSON document an older writer kept the plan in.
	documentFileName = "jobs.json"
)

// Store is a job ledger, one JSON Lines file in the directory [Open] names. Every
// operation reads the file, acts, and writes what changed, so a Store is cheap to open and
// holds no state between calls beyond its options and its lock.
//
// TWO locks, because there are two kinds of concurrent writer and neither lock sees the
// other's:
//
//   - The mutex serializes writers within one process while they share a Store.
//   - An OS file lock beside jobs.jsonl serializes writers across PROCESSES. Every
//     process holds its own Store on the same file. Two read-modify-writes that
//     interleave drop whichever row the loser had not read.
//
// Both are taken for the span of ONE call. A caller that merges fields by reading with
// List and writing back the whole row takes them twice, and two such merges on one id
// then lose whichever field the second one read before the first wrote. [Store.Update] is
// that merge done under a single acquisition.
//
// READS take neither. A write appends whole lines in one write or replaces the file by
// rename, so a reader sees each row's previous line or its next one, waiting [tailWait] out
// an append it caught mid-write.
type Store struct {
	mu sync.Mutex
	storeConfig
	// dryEnded are the rows a dry run ended, which [Store.Unswept] reads as ended so a dry
	// run ending a tree ends its descendants before their ancestors, as a real one.
	dryEnded map[string]Job
}

// storeConfig is everything a Store carries but its mutex and its dry run, so a view
// copies it whole and sets only what differs.
type storeConfig struct {
	path string
	// err is why the store could not be opened, which every operation reports: a Store
	// that cannot name its file must not read as an empty ledger, since absent rows are
	// how a guard decides nothing is leased.
	err      error
	root     string
	pinned   *Actor
	actor    func() Actor
	origin   func(context.Context) Origin
	denials  Denials
	hook     WriteHook
	retain   func(context.Context, *Rows, time.Time) error
	sweep    func(context.Context, Rows) (Rows, error)
	notices  func(Notice)
	clock    func() time.Time
	declared func(ctx context.Context, path, decl string, body []byte) string
	head     func(context.Context) string
	version  int
	migrate  Migrate
	creds    wireEnum[CredentialKind]
	wrapErr  func(error) error
	tree     func(context.Context) (string, error)
	verifier Verifier
	command  CheckCommand
}

// StoreOption sets one of a Store's seams. Each option a caller leaves out has the
// default its documentation names.
type StoreOption func(*storeConfig)

// Denials reads the denials recorded against the holder of job id since the unix second
// since, from the checkout holderCheckout names ("" for the store's own), and whether the
// record reaches back that far. A fork of an ended job folds them into its last attempt.
type Denials func(ctx context.Context, holderCheckout, id string, since int64) ([]Denial, bool, error)

// Notice is something a store did that its caller did not ask about, such as a rewrite
// dropping a line no writer finished.
type Notice struct {
	// Path is the file the notice is about.
	Path string
	// Text says what happened.
	Text string
}

func (n Notice) String() string { return fmt.Sprintf("%s: %s", n.Path, n.Text) }

// WithRoot names the checkout a row's paths are relative to, for the digest a release
// records. Without one every release records [DigestAbsent] and a [Notice] says so.
func WithRoot(root string) StoreOption { return func(c *storeConfig) { c.root = root } }

// WithActor resolves who each write is made by, at every write; an unbound writer returns
// the zero Actor. [Store.As] pins one instead. A store with neither refuses every row
// write and grant with an [*UnconfiguredError] rather than guess who wrote it.
func WithActor(actor func() Actor) StoreOption { return func(c *storeConfig) { c.actor = actor } }

// WithOrigin stamps where a write came from onto what the store records of it: a row's
// registered_by, an entry's and a grant's by. Without it those are the zero Origin.
func WithOrigin(origin func(context.Context) Origin) StoreOption {
	return func(c *storeConfig) { c.origin = origin }
}

// WithDenials reads the denials a fork of an ended job folds into its last attempt.
// Without it they are unknown.
func WithDenials(d Denials) StoreOption { return func(c *storeConfig) { c.denials = d } }

// WithWriteHook runs hook at each [WriteStage] of a row write, inside the write, while
// the store's locks are held: it must not touch the store.
func WithWriteHook(hook WriteHook) StoreOption { return func(c *storeConfig) { c.hook = hook } }

// WithRetention runs retain over the rows of every rewrite, inside the lock and before
// the write, with the store's clock: it may drop rows that are over and stamp
// [Rows.Retained]. Without it every row is kept.
func WithRetention(retain func(ctx context.Context, rows *Rows, now time.Time) error) StoreOption {
	return func(c *storeConfig) { c.retain = retain }
}

// WithSweep runs sweep over the rows [Store.List] reads before it returns them, for a
// caller that ends rows it can prove dead. The sweep may write through the store. Without
// it List returns the rows as stored.
func WithSweep(sweep func(ctx context.Context, rows Rows) (Rows, error)) StoreOption {
	return func(c *storeConfig) { c.sweep = sweep }
}

// WithNotices sends each [Notice] to notice. A store without it refuses every row write
// and grant with an [*UnconfiguredError], since a write is where a store notices.
func WithNotices(notice func(Notice)) StoreOption { return func(c *storeConfig) { c.notices = notice } }

// WithClock is the store's clock for what [Store.Now] reads. Without it, time.Now.
func WithClock(clock func() time.Time) StoreOption { return func(c *storeConfig) { c.clock = clock } }

// WithDeclarationDigest digests one declaration of the file at path, whose content is
// body, for the release of a claim. It answers a sha256 digest of the lines the
// declaration spans, or one of the Digest markers. Without it a released declaration
// records [DigestUnreadable] and a [Notice] says so.
func WithDeclarationDigest(digest func(ctx context.Context, path, decl string, body []byte) string) StoreOption {
	return func(c *storeConfig) { c.declared = digest }
}

// WithHead resolves the committed revision an unbound fork records as a row's
// Authority. Without it such a fork records none.
func WithHead(head func(context.Context) string) StoreOption {
	return func(c *storeConfig) { c.head = head }
}

// WithMigrations is the migration this build writes and how a file at an older one is
// brought to it. Without it the store writes migration zero and refuses a file at any
// other.
func WithMigrations(version int, migrate Migrate) StoreOption {
	return func(c *storeConfig) { c.version, c.migrate = version, migrate }
}

// WithCredentialKinds are the credential kinds an origin the store records may carry. A
// stored kind none of them spells is refused rather than read. Without it an origin
// carrying any kind is refused.
func WithCredentialKinds(kinds ...CredentialKind) StoreOption {
	return func(c *storeConfig) { c.creds = newWireEnum(kinds...) }
}

// WithErrorWrap passes each refusal the store raises of its own, a typed error, through
// wrap, for a caller that adds its own codes or remedy.
func WithErrorWrap(wrap func(error) error) StoreOption {
	return func(c *storeConfig) { c.wrapErr = wrap }
}

// WithTree digests the tree a check reads, the way the caller defines it: a revision plus
// a digest of the uncommitted changes, a hash of a module, anything stable. The store
// compares digests and never interprets one. Without it [Store.Witness], [Store.Attest],
// [Store.ReportRun] and [Store.Grade] refuse with a [*GradingUnconfiguredError], since no
// grade could tell a run of this tree from a run of an older one.
func WithTree(tree func(context.Context) (string, error)) StoreOption {
	return func(c *storeConfig) { c.tree = tree }
}

// WithVerifier lets [Store.Attest] accept the statements v vouches for. Without it Attest
// refuses every statement rather than record one unchecked.
func WithVerifier(v Verifier) StoreOption { return func(c *storeConfig) { c.verifier = v } }

// WithCheckCommand names the command a check goal is run by. A [LeaseCheck] names a target
// or a script, not an argv, so the store derives none itself. Without it [Store.Witness]
// refuses.
func WithCheckCommand(command CheckCommand) StoreOption {
	return func(c *storeConfig) { c.command = command }
}

// Open returns the ledger kept in dir, which holds jobs.jsonl, attempts.jsonl,
// grants.jsonl, grades.jsonl and the lock. The files are created by the first write. dir is the
// caller's: Open resolves nothing about a repository.
func Open(dir string, opts ...StoreOption) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("job: open a store on the directory that holds it, and none was named")
	}
	s := &Store{storeConfig: storeConfig{path: filepath.Join(dir, jobsFileName), creds: newWireEnum[CredentialKind]()}}
	for _, opt := range opts {
		opt(&s.storeConfig)
	}
	return s, nil
}

// Unopened is a store every operation of which fails with err, for a caller that holds
// the reason it could not place a store rather than return it.
func Unopened(err error) *Store {
	return &Store{storeConfig: storeConfig{err: err, creds: newWireEnum[CredentialKind]()}}
}

// As returns a Store over s's file that writes as a, for a door serving a caller other
// than this process. The view takes its own mutex.
func (s *Store) As(a Actor) *Store {
	v := &Store{storeConfig: s.storeConfig}
	v.pinned = &a
	return v
}

// Actor is who this Store writes as RIGHT NOW, for a door that has to refuse before it
// computes anything. Resolved per call unless [Store.As] pinned one.
func (s *Store) Actor() Actor {
	switch {
	case s.pinned != nil:
		return *s.pinned
	case s.actor != nil:
		return s.actor()
	}
	return Actor{}
}

// Path is the JSON Lines file this Store reads and writes. The file itself may not exist
// yet, which is an empty ledger and not an error.
func (s *Store) Path() (string, error) { return s.path, s.err }

// Now is the Store's clock.
func (s *Store) Now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *Store) originOf(ctx context.Context) Origin {
	if s.origin == nil {
		return Origin{}
	}
	return s.origin(ctx)
}

func (s *Store) notice(n Notice) {
	if s.notices != nil {
		s.notices(n)
	}
}

// dropped is the [codec.JSONL] OnDrop of every log beside the rows: a rewrite that dropped
// a line no writer finished is reported as a notice rather than refused.
func (s *Store) dropped(_ context.Context, d codec.Dropped) {
	s.notice(Notice{Path: d.Path, Text: fmt.Sprintf("line %d was one no writer finished; the rewrite dropped it", d.Line)})
}

// wrap is err through the caller's [WithErrorWrap].
func (s *Store) wrap(err error) error {
	if err == nil || s.wrapErr == nil {
		return err
	}
	return s.wrapErr(err)
}

// UnconfiguredError is a write on a store opened without an option every write needs.
type UnconfiguredError struct {
	// Op is the call, such as "Update", and Option the store option it needs, such as
	// "WithActor".
	Op, Option string
}

func (e *UnconfiguredError) Error() string {
	return fmt.Sprintf("job: %s writes through a store opened with %s, and this one was opened without it", e.Op, e.Option)
}

// needWriter refuses write op on a store that cannot say who writes or what it noticed.
func (s *Store) needWriter(op string) error {
	switch {
	case s.err != nil:
		return s.err
	case s.pinned == nil && s.actor == nil:
		return s.wrap(&UnconfiguredError{Op: op, Option: "WithActor"})
	case s.notices == nil:
		return s.wrap(&UnconfiguredError{Op: op, Option: "WithNotices"})
	}
	return nil
}

// refuseDocument fails when the store's directory still holds the plan an older writer
// kept as one JSON document.
func (s *Store) refuseDocument() error {
	old := filepath.Join(filepath.Dir(s.path), documentFileName)
	if err := codec.RefuseOld(old); err != nil {
		return s.wrap(&LegacyDocumentError{Path: s.path, Document: old, Err: err})
	}
	return nil
}

// Rows is the ledger as read: the header and the rows.
//
// The file is JSON Lines. The header is the line without an id, written first by every
// rewrite, and each other line is one version of one row. A row's LAST line is the row, and
// it keeps the place its first line had, so the rows read in the order they were declared.
//
// The header's requires annotation refuses the whole file to a reader lacking a feature it
// names.
type Rows struct {
	apiversion.TypeMeta
	Metadata apiversion.Metadata `json:"metadata,omitzero"`
	// Migration is how many migrations the rows have been through; see [WithMigrations].
	// A file past this build's is refused.
	Migration int `json:"migration,omitzero"`
	// Retained is when the last retention pass ran, in unix seconds; see [WithRetention].
	Retained int64                       `json:"retained"`
	Unknown  map[string]codec.RawMessage `json:"-" codec:"unknown"`
	Jobs     []Job                       `json:"-"`
	// stored are the rows' records as read, by id, so a rewrite keeps
	// what this build does not declare.
	stored map[string]jobRecord
}

// MarshalJSON writes the header with the members it kept in Unknown.
func (f Rows) MarshalJSON() ([]byte, error) {
	type plain Rows
	return codec.MarshalWithUnknown(plain(f), f.Unknown)
}

// UnmarshalJSON reads the header, keeping the members it does not declare in Unknown.
func (f *Rows) UnmarshalJSON(data []byte) error {
	type plain Rows
	var p plain
	unknown, err := codec.UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*f = Rows(p)
	f.Unknown = unknown
	return nil
}

// Header is f without its rows.
func (f Rows) Header() Rows {
	return Rows{TypeMeta: f.TypeMeta, Metadata: f.Metadata, Migration: f.Migration, Retained: f.Retained, Unknown: f.Unknown, stored: f.stored}
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

// Unwritable refuses a write to row when it requires a feature this build lacks. The row
// stays listed and graded; only writing it is out of reach, since a write by a reader that
// cannot act on the row's content could corrupt it.
func Unwritable(row Job) error {
	lacks := row.Unmet()
	if lacks == nil {
		return nil
	}
	return &NewerRecordsError{Record: row.ID, Lacks: lacks}
}

// requiredFeatures are the JobSchema features row's content needs a reader to implement,
// sorted: a `file#Declaration` claim, a `signature` goal and an attempt record (a cap, a
// prior attempt, a second attempt) are each read wrongly by a reader older than the version
// that named them, so a row carrying one is read-only to it. Derived from the content on
// every write, so a row that drops its last claim is writable again.
func requiredFeatures(row Job) []string {
	var out []string
	if slices.ContainsFunc(slices.Concat(row.Paths.Write, row.Paths.Deny), HasClaim) {
		out = append(out, "claim-declarations")
	}
	if slices.ContainsFunc(row.Goals, func(g Goal) bool { return g.Resolve().Expect == ExpectSignature }) {
		out = append(out, "signature-goals")
	}
	// Every row has a generation, so a row carrying only that is not stamped: it would make
	// every row read-only to an older reader.
	if row.MaxAttempts > 0 {
		out = append(out, "job-generations")
	}
	slices.Sort(out)
	return out
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
// from the write paths IS the release announcement.
//
// apply runs while the lock is held, so it must not touch the store, and it cannot fail:
// anything that could be rejected belongs in the caller, before the call.
//
// ctx reaches the release digests and the hooks. A cancelled call still WRITES the row,
// since the merge is already done, but it stops hashing files.
func (s *Store) Update(ctx context.Context, id string, apply func(*Job)) (Job, error) {
	return s.Mutate(ctx, id, AsDeclaration, func(cur *Job, _ bool, _ int64) error {
		apply(cur)
		return nil
	})
}

// maxUnattributedWrites bounds how many outside-written paths one row keeps. The newest are
// kept, since a lease asking what moved is asking about the tree it faces now.
const maxUnattributedWrites = 32

// errNoSuchJob is how an observation's apply declines a row that is not there without
// inventing a lease nobody declared.
var errNoSuchJob = errors.New("job: no such lease")

// RecordUnattributedWrite notes that somebody outside lease id wrote one of its write paths,
// with the content they left behind.
//
// ONE ROW PER PATH, newest wins. A missing row is not an error: the lease may have ended
// between the grading and this call.
func (s *Store) RecordUnattributedWrite(ctx context.Context, id, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	// An OBSERVATION, and ungraded: the row it lands on is by definition somebody else's,
	// so grading it would throw the notice away precisely when it matters.
	_, err := s.Mutate(ctx, id, AsObservation, func(cur *Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: no such lease %q: %w", id, errNoSuchJob)
		}
		next := make([]UnattributedWrite, 0, len(cur.Unattributed)+1)
		for _, w := range cur.Unattributed {
			if w.Path != path {
				next = append(next, w)
			}
		}
		next = append(next, UnattributedWrite{Path: path, Digest: s.fileDigest(ctx, path), At: now})
		if len(next) > maxUnattributedWrites {
			next = next[len(next)-maxUnattributedWrites:]
		}
		cur.Unattributed = next
		return nil
	})
	if errors.Is(err, errNoSuchJob) {
		return nil
	}
	return err
}

// MaxJobEntries is how many entries one job takes. Past it, the job wants its holder
// resumed or its lease ended, not a third visitor.
const MaxJobEntries = 2

// Enter records that the caller will write rel, inside the write paths of live job id,
// once. A guard then lets one write by somebody other than the holder through and stamps
// it consumed; see [Store.ConsumeEntry].
//
// Refused, and nothing is written, when the job is not live, when the caller is its
// holder, when rel is outside its write paths (that wants widening, not entering), when
// an entry for rel is still open, and when the job already took [MaxJobEntries].
func (s *Store) Enter(ctx context.Context, id, rel string) (Job, error) {
	rel, err := EntryPath(rel)
	if err != nil {
		return Job{}, err
	}
	return s.Update(ctx, id, func(u *Job) { u.Entries = append(u.Entries, Entry{Path: rel}) })
}

// requestsEntry reports a write whose only change is one unstamped entry appended to
// prev's, which is how Enter reaches Mutate.
func requestsEntry(prev, next Job) bool {
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
func authorizeEntry(actor Actor, id string, prev, next Job, exists bool, rows []Job) error {
	entry := next.Entries[len(next.Entries)-1]
	switch {
	case !exists:
		return fmt.Errorf("%q: there is nothing to enter: %w", id, ErrUnknownJob)
	case len(ChangedFields(prev, next)) > 0:
		return fmt.Errorf("job: an entry declares nothing, and this write also changes %s", strings.Join(ChangedFields(prev, next), ", "))
	case actor.Lease == id:
		return refuse(actor, id, "a holder writes its own paths and never enters them")
	case actor.Bound() && !slices.ContainsFunc(Ancestors(rows, id), func(r Job) bool { return r.ID == actor.Lease }):
		return refuse(actor, id, "a worker enters only a job forked beneath its own")
	case !prev.State.Live():
		return fmt.Errorf("job: %s is %s, so its paths are free to write and there is nothing to enter", id, prev.State)
	}
	if _, ok := Matching(prev.Paths.Write, entry.Path); !ok {
		return fmt.Errorf("job: %s is outside the write paths of %s (%s), a path the job does not own is widened into it, never entered",
			entry.Path, id, strings.Join(prev.Paths.Write, ", "))
	}
	if open, ok := OpenEntry(prev, entry.Path); ok {
		return fmt.Errorf("job: %s already has an entry for %s, recorded at %s and not yet written, write it before entering again",
			id, open.Path, time.Unix(open.At, 0).UTC().Format(time.RFC3339))
	}
	if len(prev.Entries) >= MaxJobEntries {
		return fmt.Errorf("job: %s has taken its %d entries, resume its holder to make the change, or end its lease",
			id, MaxJobEntries)
	}
	return nil
}

// OpenEntry is the first entry on row that covers rel and has not been consumed.
func OpenEntry(row Job, rel string) (Entry, bool) {
	for _, e := range row.Entries {
		if e.Consumed == 0 && Covers(e.Path, rel) {
			return e, true
		}
	}
	return Entry{}, false
}

// ConsumeEntry stamps the open entry on job id covering rel as used. An observation, like
// RecordUnattributedWrite. A job with no open entry for rel is left alone.
func (s *Store) ConsumeEntry(ctx context.Context, id, rel string) error {
	_, err := s.Mutate(ctx, id, AsObservation, func(cur *Job, exists bool, now int64) error {
		if !exists {
			return fmt.Errorf("job: no such lease %q: %w", id, errNoSuchJob)
		}
		for i, e := range cur.Entries {
			if e.Consumed == 0 && Covers(e.Path, rel) {
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

// boundaryFields are the declared fields a widen or narrow changes, in ChangedFields'
// spelling.
func boundaryFields() []string { return []string{"paths.write", "paths.deny", "paths.read"} }

// widensInPlace reports a declaration that changes a live row's boundaries and nothing
// else it declares, while resetting a state its holder already moved past declared: a
// re-fork that only moves paths. Such a write keeps the stored state, since resetting it
// would hand the holder's job out again. A deadline may move with it. A re-fork that
// changes no boundary still resets the state, which is how a rejected job is handed out
// again.
func widensInPlace(prev, next Job) bool {
	if !prev.State.Live() || prev.State == StateDeclared || next.State != StateDeclared {
		return false
	}
	moved := false
	for _, field := range ChangedFields(prev, next) {
		switch {
		case slices.Contains(boundaryFields(), field):
			moved = true
		case field != "state" && field != "deadline":
			return false
		}
	}
	return moved
}

// ReasonIntegratorRegenerates is the reason of a deviation a fork derives for an output
// outside the write paths that the integrator regenerates. A whole-row record that omits
// it keeps it (see keepDerived).
const ReasonIntegratorRegenerates = "the integrator regenerates it"

// keepDerived carries what the fork derived for prev onto a whole-row record that omits
// it, so a projection never hardens a path. That is prev's soft paths, unless row names
// paths.soft for one, and the deviations the integrator regenerates.
func keepDerived(row *Job, prev Job) {
	if !slices.ContainsFunc(row.Paths.Soft, func(p string) bool { return slices.Contains(prev.Paths.Write, p) }) {
		for _, p := range prev.Paths.Soft {
			if slices.Contains(row.Paths.Write, p) && !slices.Contains(row.Paths.Soft, p) {
				row.Paths.Soft = append(row.Paths.Soft, p)
			}
		}
	}
	for _, w := range prev.Deviations {
		if w.Reason == ReasonIntegratorRegenerates && !slices.ContainsFunc(row.Deviations, func(have Deviation) bool {
			return have.Code == w.Code && have.Subject == w.Subject
		}) {
			row.Deviations = append(row.Deviations, w)
		}
	}
}

// AttemptStart is when row's live attempt began, unix seconds: when the last prior attempt
// ended, else when the row was created.
func AttemptStart(row Job, tries Attempts) int64 {
	if prior := tries.Prior(row.ID); len(prior) > 0 {
		return prior[len(prior)-1].Status.Ended
	}
	return row.Created
}

// endedAttemptDenials are the denials of the holder of job id since its last attempt
// began, for the fold a fork of the ended row makes, and whether the record still reaches
// back to that start. Only an ended row is read. A store with no [WithDenials], or one
// whose record cannot be read, gives none and unknown, which the fold records as unknown.
func (s *Store) endedAttemptDenials(ctx context.Context, id string) ([]Denial, bool) {
	if s.denials == nil {
		return nil, false
	}
	f, err := s.Read(ctx)
	if err != nil {
		return nil, false
	}
	i := slices.IndexFunc(f.Jobs, func(r Job) bool { return r.ID == id })
	if i < 0 || !f.Jobs[i].State.Terminal() {
		return nil, false
	}
	row := f.Jobs[i]
	all, err := s.Attempts(ctx)
	if err != nil {
		return nil, false
	}
	denials, known, err := s.denials(ctx, row.CheckoutRoot, id, AttemptStart(row, NewAttempts(all)))
	if err != nil {
		return nil, false
	}
	return denials, known
}

// Reader is s as a [Reader].
func (s *Store) Reader() Reader { return storeReader{s} }

type storeReader struct{ s *Store }

func (r storeReader) List(ctx context.Context) ([]Job, error) { return r.s.List(ctx) }

// List returns every row in the order it was first recorded. The rows are copies, so a
// caller may keep or mutate them without reaching back into the file's next read.
//
// With [WithSweep] every read sweeps first: the sweep may end a row it can prove dead,
// which is the one write a read makes. ctx bounds the lock a migration or the sweep's
// write waits on, and reaches the sweep.
func (s *Store) List(ctx context.Context) ([]Job, error) {
	f, err := s.Read(ctx)
	if err != nil {
		return nil, err
	}
	if s.sweep != nil {
		if f, err = s.sweep(ctx, f); err != nil {
			return nil, err
		}
	}
	return cloneRows(f.Jobs), nil
}

// Unswept is [Store.List] without the sweep, for a verb scoped to the rows it names:
// ending one job must not end, or probe the landed work of, every other row in the plan.
// A row [Store.EndDry] ended reads as ended.
func (s *Store) Unswept(ctx context.Context) ([]Job, error) {
	f, err := s.Read(ctx)
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

// EndDry records row as a dry run ended it: [Store.Unswept] reads it so from now on, and
// nothing is written.
func (s *Store) EndDry(row Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dryEnded == nil {
		s.dryEnded = map[string]Job{}
	}
	s.dryEnded[row.ID] = row.Clone()
}

func cloneRows(rows []Job) []Job {
	out := make([]Job, len(rows))
	for i, row := range rows {
		out[i] = row.Clone()
	}
	return out
}

// Clear drops every row, which is how a fresh plan starts, ARCHIVING what it dropped to a
// timestamped sibling file (jobs-<UTC time>.jsonl) first, and returns how many rows it
// dropped. Clearing an empty or absent ledger is not an error and archives nothing.
//
// The COUNT comes from inside the lock: a preceding List to derive it undercounts a row a
// concurrent registrant appended between the two calls.
//
// A bound worker is refused. ctx bounds the wait for the lock and nothing else.
func (s *Store) Clear(ctx context.Context) (int, error) {
	if err := authorizeClear(s.Actor()); err != nil {
		return 0, err
	}
	var dropped int
	err := s.Rewrite(ctx, func(f *Rows) error {
		if len(f.Jobs) > 0 {
			if err := s.archive(ctx, *f); err != nil {
				return err
			}
		}
		dropped = len(f.Jobs)
		*f = f.Header()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return dropped, nil
}

// archive writes the rows a clear or a delete is about to drop beside the ledger, in the
// ledger's own shape and named for the moment they were dropped, with their attempts. A
// second drop within the same second overwrites the first archive.
func (s *Store) archive(ctx context.Context, f Rows) error {
	f.Migration = s.version
	lines, err := s.encodeJobs(f)
	if err != nil {
		return err
	}
	stamp := s.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Dir(s.path)
	if err := s.archiveAttempts(ctx, filepath.Join(dir, fmt.Sprintf("jobs-%s.attempts.jsonl", stamp)), f.Jobs); err != nil {
		return err
	}
	return codec.RewriteLines(filepath.Join(dir, fmt.Sprintf("jobs-%s.jsonl", stamp)), lines, 0o600)
}

// Rewrite runs fn over the ledger as it stands, holding both of the Store's locks, so a
// read-modify-write cannot interleave with one from this process or another, and writes
// what fn leaves, after the [WithRetention] pass. fn returning an error, [ErrSkipWrite]
// included, writes nothing; ErrSkipWrite is no error.
//
// fn must not touch the store, and must leave each id once: a second row under an id is a
// [*DuplicateRowError] and nothing is written. The lock wait is BOUNDED: a ledger write is a small append,
// so a longer wait means a stuck holder.
func (s *Store) Rewrite(ctx context.Context, fn func(*Rows) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rewrite(ctx, fn)
}

// rewrite is Rewrite for a caller holding s.mu.
func (s *Store) rewrite(ctx context.Context, fn func(*Rows) error) error {
	if err := s.needWriter("Rewrite"); err != nil {
		return err
	}
	// Made here, before the lock and file helpers create it world-readable. Rows carry a
	// repository's plan and its checkout paths.
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("job: create store %s: %w", filepath.Dir(s.path), err)
	}
	return s.locked(ctx, func() error {
		f, log, err := s.readFile(ctx, false)
		if err != nil {
			return err
		}
		gone := make(map[string]bool, len(f.Jobs))
		for _, row := range f.Jobs {
			gone[row.ID] = true
		}
		if err := fn(&f); err != nil {
			if errors.Is(err, ErrSkipWrite) {
				return nil
			}
			return err
		}
		if s.retain != nil {
			if err := s.retain(ctx, &f, s.Now()); err != nil {
				return err
			}
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
// appended lines would make it [codec.CompactDue].
func (s *Store) write(f Rows, log jobsLog) error {
	if err := refuseDuplicateRows(f.Jobs); err != nil {
		return s.wrap(err)
	}
	f.Migration = s.version
	lines, err := s.encodeJobs(f)
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
	if !dropped && sameHeader && !log.partial && !codec.CompactDue(log.lines+len(changed), len(f.Jobs)) {
		return codec.AppendLines(s.path, changed, 0o600)
	}
	if log.partial {
		s.notice(Notice{Path: s.path, Text: "ended in a line no writer finished; the rewrite dropped it"})
	}
	return codec.RewriteLines(s.path, lines, 0o600)
}

// DuplicateRowError is a rewrite leaving two rows under one id. A row is keyed by its id,
// so the file would read back as the last of them and the others would be dropped.
type DuplicateRowError struct{ ID string }

func (e *DuplicateRowError) Error() string {
	return fmt.Sprintf("job: the rewrite leaves two rows named %q, and a row is keyed by its id, so nothing was written", e.ID)
}

func refuseDuplicateRows(rows []Job) error {
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if seen[row.ID] {
			return &DuplicateRowError{ID: row.ID}
		}
		seen[row.ID] = true
	}
	return nil
}

// Read loads the file. An absent file is an empty ledger, not a failure: nothing has
// been recorded yet. ctx bounds the wait for an append in flight and for the lock a
// migration takes.
func (s *Store) Read(ctx context.Context) (Rows, error) {
	f, _, err := s.readFile(ctx, true)
	return f, err
}

// readFile parses the ledger and the log a write compares against.
//
// A reader holding no lock may catch an append mid-write, so with wait it reads again for
// up to [tailWait] while the file ends in an unfinished line, and reports one that stays
// unfinished. A writer holds the lock, so an unfinished line it finds was left by a writer
// that died, and the write drops it.
//
// A row newer than this build is read like any other. The members it does not declare
// ride in its stored record and go back out on the next write. A row requiring a feature
// this build lacks is read too, and only a write to it is refused (see [Unwritable]).
//
// A file at an older migration is migrated first, once, under the lock (see
// [WithMigrations]); without wait the caller already holds it.
func (s *Store) readFile(ctx context.Context, wait bool) (Rows, jobsLog, error) {
	f, log, lines, err := s.parse(ctx, wait)
	if err != nil || f.Migration == s.version || log.lines == 0 {
		return f, log, err
	}
	// The caller's migrations refuse a newer file in their own words; with none, the store
	// refuses any other migration than its own.
	if s.migrate == nil {
		return Rows{}, jobsLog{}, s.wrap(&NewerWriterError{Path: s.path, Found: f.Migration, Known: s.version})
	}
	migrate := func() error { return s.migrate(ctx, s.path, f.Migration, lines) }
	if wait {
		err = s.locked(ctx, migrate)
	} else {
		err = migrate()
	}
	if err != nil {
		return Rows{}, jobsLog{}, err
	}
	f, log, _, err = s.parse(ctx, wait)
	return f, log, err
}

// parse reads the file as it stands; see [Store.readFile]. A file at another migration
// than this build's has no rows decoded: they are returned as lines.
func (s *Store) parse(ctx context.Context, wait bool) (Rows, jobsLog, []codec.Line, error) {
	if s.err != nil {
		return Rows{}, jobsLog{}, nil, s.err
	}
	if err := s.refuseDocument(); err != nil {
		return Rows{}, jobsLog{}, nil, err
	}
	read, err := s.readLines(ctx, wait)
	if err != nil {
		return Rows{}, jobsLog{}, nil, err
	}
	f := Rows{stored: map[string]jobRecord{}}
	log := jobsLog{rows: make(map[string][]byte, len(read.Lines)), lines: len(read.Lines), partial: read.Partial}
	texts, err := s.takeHeader(read.Lines, &f, &log)
	if err != nil {
		return Rows{}, jobsLog{}, nil, err
	}
	if f.Migration != s.version {
		return f, log, texts, nil
	}
	if f.TypeMeta != jobListType() && log.header != nil {
		return Rows{}, jobsLog{}, nil, s.refuseRecord(jobListType(), f.TypeMeta, 1)
	}
	if err := s.decodeRows(texts, &f, &log); err != nil {
		return Rows{}, jobsLog{}, nil, err
	}
	if err := s.refuseUnmetFeatures(f); err != nil {
		return Rows{}, jobsLog{}, nil, err
	}
	return f, log, nil, nil
}

// readLines reads the file's lines. With wait it reads again, a millisecond apart, for up
// to [tailWait] while the file ends in a line no writer finished. It counts tries rather
// than read a clock: a store clock [WithClock] holds still would never reach the deadline.
func (s *Store) readLines(ctx context.Context, wait bool) (codec.Lines, error) {
	for try := time.Duration(0); ; try += time.Millisecond {
		read, err := codec.ReadLines(s.path)
		if err != nil {
			return codec.Lines{}, fmt.Errorf("job: %w", err)
		}
		if !read.Partial || !wait {
			return read, nil
		}
		if try >= tailWait {
			return codec.Lines{}, fmt.Errorf("job: %s ends in a line no writer finished, so the next write to the plan drops it", s.path)
		}
		select {
		case <-ctx.Done():
			return codec.Lines{}, fmt.Errorf("job: waiting out an append in flight to %s: %w", s.path, ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

// takeHeader sets the header line into f and log, and returns the lines of the rows.
func (s *Store) takeHeader(lines []codec.Line, f *Rows, log *jobsLog) ([]codec.Line, error) {
	var texts []codec.Line
	for _, line := range lines {
		var probe struct {
			apiversion.TypeMeta
			ID   codec.RawMessage `json:"id"`
			Spec codec.RawMessage `json:"spec"`
		}
		if err := codec.Unmarshal(line.Text, &probe); err != nil {
			return nil, s.wrap(&codec.LineError{Path: s.path, Line: line.N, Err: err})
		}
		if probe.ID != nil || probe.Spec != nil {
			texts = append(texts, line)
			continue
		}
		header := Rows{}
		if err := codec.Unmarshal(line.Text, &header); err != nil {
			return nil, s.wrap(&codec.LineError{Path: s.path, Line: line.N, Err: err})
		}
		header.stored, log.header = f.stored, line.Text
		*f = header
	}
	return texts, nil
}

// decodeRows decodes the row lines into f and log, a later line of a row replacing the
// earlier one in place.
func (s *Store) decodeRows(texts []codec.Line, f *Rows, log *jobsLog) error {
	at := map[string]int{}
	for _, line := range texts {
		var r jobRecord
		if err := codec.Unmarshal(line.Text, &r); err != nil {
			return s.wrap(&codec.LineError{Path: s.path, Line: line.N, Err: err})
		}
		row, err := r.job(s.creds)
		if refused, ok := errors.AsType[*apiversion.RefusedError](err); ok {
			return s.refuseRecord(refused.Want, refused.Got, line.N)
		}
		if err != nil {
			return s.wrap(&codec.LineError{Path: s.path, Line: line.N, Err: err})
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
	return nil
}

// refuseUnmetFeatures refuses a file requiring a feature this build lacks.
func (s *Store) refuseUnmetFeatures(f Rows) error {
	known := make([]apiversion.Feature, len(features()))
	for i, k := range features() {
		known[i] = apiversion.Feature(k)
	}
	lacks := f.Metadata.Unmet(recordGroup, known)
	if lacks == nil {
		return nil
	}
	names := make([]string, len(lacks))
	for i, l := range lacks {
		names[i] = string(l)
	}
	return s.wrap(&NewerWriterError{Path: s.path, Lacks: names})
}

// encodeJobs renders the ledger as its lines: the header first, then one line per row.
// Every row, touched this call or not, keeps what its stored record carries.
func (s *Store) encodeJobs(f Rows) ([][]byte, error) {
	f.TypeMeta = jobListType()
	header, err := codec.Marshal(f)
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
		line, err := codec.Marshal(recordJob(row, stored, s.creds))
		if err != nil {
			return nil, fmt.Errorf("job: %s: %w", row.ID, err)
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// MarshalRecord is row as the line jobs.jsonl stores, with unknown kept beside its
// declared members and kinds the credential kinds its origins may carry, for a migration
// that writes a row an older layout held.
func MarshalRecord(row Job, unknown map[string]codec.RawMessage, kinds ...CredentialKind) ([]byte, error) {
	r := recordJob(row, nil, newWireEnum(kinds...))
	r.Unknown = unknown
	return codec.Marshal(r)
}

// MarshalHeader is the header line of a file whose rows require features, for a
// migration that writes the header an older layout held.
func MarshalHeader(features []apiversion.Feature) ([]byte, error) {
	return codec.Marshal(Rows{TypeMeta: jobListType(), Metadata: apiversion.Metadata{}.WithRequires(recordGroup, features)})
}

// ReadRecord is the row a line of jobs.jsonl stores, kinds being the credential kinds
// its origins may carry, and false for a line that is not a row, such as the header.
func ReadRecord(text []byte, kinds ...CredentialKind) (Job, bool, error) {
	var r jobRecord
	if err := codec.Unmarshal(text, &r); err != nil {
		return Job{}, false, err
	}
	if r.Kind != jobType().Kind {
		return Job{}, false, nil
	}
	row, err := r.job(newWireEnum(kinds...))
	return row, err == nil, err
}

// Delete removes ONE row and returns it, or reports that no such row exists.
//
// A row that is over is still a row: ending a job moves it to a terminal state, which is
// the RECORD of what happened. Delete is for a row that should never have been written,
// and it is the only way to take one out without Clear taking every other lease's row
// with it.
//
// It refuses a TERMINAL row unless force: deleting the record of a job that actually ran
// destroys the only account of it.
//
// A row owning a checkout is put to the [StageDelete] hook first, which may keep the row
// with its checkout by refusing. The dropped row is archived first, as Clear archives.
func (s *Store) Delete(ctx context.Context, id string, force bool) (Job, error) {
	id = strings.TrimSpace(id)
	if err := authorizeDelete(s.Actor(), id); err != nil {
		return Job{}, err
	}
	var dropped Job
	err := s.Rewrite(ctx, func(f *Rows) error {
		i := slices.IndexFunc(f.Jobs, func(row Job) bool { return row.ID == id })
		if i < 0 {
			return fmt.Errorf("job: there is no job %q", id)
		}
		dropped = f.Jobs[i]
		if err := Unwritable(dropped); err != nil {
			return s.wrap(err)
		}
		if dropped.State.Terminal() && !force {
			return fmt.Errorf("job: %s is %s, and that row is the record of what happened:"+
				" delete it anyway with force, or leave it where a later reader can find it",
				id, dropped.State)
		}
		if s.hook != nil {
			others := slices.DeleteFunc(slices.Clone(f.Jobs), func(row Job) bool { return row.ID == id })
			e := WriteEvent{Stage: StageDelete, ID: id, Actor: s.Actor(), Prev: dropped, Row: &dropped, Exists: true, Rows: others, Now: s.Now().Unix()}
			if err := s.hook(ctx, &e); err != nil {
				return err
			}
		}
		archived := f.Header()
		archived.Jobs = []Job{dropped}
		if err := s.archive(ctx, archived); err != nil {
			return err
		}
		f.Jobs = slices.Delete(f.Jobs, i, i+1)
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return dropped, nil
}

// Releases carries the row's recorded releases forward and adds the paths this write
// gave up, the ones prev owned and next does not.
//
// A path that is owned again drops OUT of the list. A path released twice keeps its
// position and takes the NEWER digest, because the version the next agent inherits is the
// one left behind last. revoked marks what this write gave up as taken from its holder
// rather than given up by it.
func (s *Store) Releases(ctx context.Context, prev, next Job, now int64, revoked bool) []Release {
	refork := prev.State.Terminal() && next.State.Live()
	out := slices.DeleteFunc(slices.Clone(prev.Releases), func(r Release) bool {
		switch {
		// A narrowed release outlives its entry; a re-fork ends it.
		case r.Narrowed && refork:
			return true
		case r.Narrowed && slices.Contains(prev.Paths.Write, r.Path):
			return false
		}
		return slices.Contains(next.Paths.Write, r.Path)
	})
	for _, p := range prev.Paths.Write {
		if slices.Contains(next.Paths.Write, p) || slices.Contains(prev.Paths.Soft, p) {
			continue
		}
		rel := Release{Path: p, Digest: s.Digest(ctx, p), ReleasedAt: now, Revoked: revoked}
		if at := slices.IndexFunc(out, func(r Release) bool { return r.Path == p }); at >= 0 {
			out[at] = rel
			continue
		}
		out = append(out, rel)
	}
	return out
}

// Digest identifies the content at a released path, resolved inside the store's root.
// Computed here rather than passed in: a digest the releaser supplied would describe the
// tree it believed it left rather than the one it did.
//
// Everything that is not a readable file inside the root answers with one of the three
// markers rather than a hash: [DigestAbsent], [DigestDir] and [DigestUnreadable]. A path
// escaping the root is absent by the same rule.
//
// Runs under the store's mutex, which is why every branch below is bounded.
//
// A released declaration (`run.go#executeStages`) digests what [WithDeclarationDigest]
// answers for the file now.
func (s *Store) Digest(ctx context.Context, declared string) string {
	p, decl := SplitClaim(declared)
	if decl != "" {
		return s.declarationDigest(ctx, p, decl)
	}
	return s.fileDigest(ctx, p)
}

// fileDigest is Digest for a path SplitClaim already cut.
func (s *Store) fileDigest(ctx context.Context, declared string) string {
	if s.root == "" {
		s.notice(Notice{Path: declared, Text: "the store names no root (WithRoot), so its release records " + DigestAbsent})
		return DigestAbsent
	}
	if ctx.Err() != nil {
		return DigestUnreadable
	}
	// Symlinks are resolved on BOTH sides before the containment check, so a link inside
	// the root pointing outside it is never hashed, and a root reached through a symlinked
	// parent does not read as an escape from itself.
	full, ok := s.resolve(declared)
	if !ok {
		return DigestAbsent
	}
	info, err := os.Stat(full)
	switch {
	case err != nil:
		return DigestUnreadable
	case info.IsDir():
		return DigestDir
	case !info.Mode().IsRegular():
		// A fifo, socket, or device. os.Open on a released named pipe BLOCKS until
		// somebody writes to it, and it would block holding the store's mutex.
		return DigestUnreadable
	case info.Size() > maxDigestBytes:
		return DigestUnreadable
	}
	fh, err := os.Open(full)
	if err != nil {
		return DigestUnreadable
	}
	defer fh.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, fh); err != nil {
		return DigestUnreadable
	}
	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// resolve is declared inside the root with symlinks resolved, and false when nothing
// resolves there or it escapes the root.
func (s *Store) resolve(declared string) (string, bool) {
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return "", false
	}
	full, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(declared)))
	if err != nil {
		// Nothing resolves at the path: a deleted file, a broken link, or a declared glob.
		return "", false
	}
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return full, true
}

// declarationDigest is Digest for one declaration of the file at p. The file passes every
// check Digest makes of a whole file before a line of it is placed.
func (s *Store) declarationDigest(ctx context.Context, p, decl string) string {
	if whole := s.fileDigest(ctx, p); !strings.HasPrefix(whole, "sha256:") {
		return whole
	}
	if s.declared == nil {
		s.notice(Notice{Path: p, Text: "the store digests no declaration (WithDeclarationDigest), so the release of " + decl + " records " + DigestUnreadable})
		return DigestUnreadable
	}
	full, ok := s.resolve(p)
	if !ok {
		return DigestAbsent
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return DigestUnreadable
	}
	return s.declared(ctx, p, decl, body)
}

// maxDigestBytes bounds one release digest, because the hash is computed while the store
// holds its mutex; a path over the cap records unreadable, which is what it is from the
// reader's side.
const maxDigestBytes = 32 << 20

// attemptsLog is attempts.jsonl beside the rows: an attempt lives and dies with the row
// it is a try of.
func (s *Store) attemptsLog() (codec.JSONL[attemptRecord], error) {
	if s.err != nil {
		return codec.JSONL[attemptRecord]{}, s.err
	}
	return codec.JSONL[attemptRecord]{
		Path:   filepath.Join(filepath.Dir(s.path), attemptsFileName),
		Perm:   0o600,
		Key:    attemptRecord.key,
		Live:   func(r attemptRecord) bool { return !r.Status.Dropped },
		OnDrop: s.dropped,
	}, nil
}

// refuseAttemptVersion refuses a line another writer's version stored, which this one
// cannot tell the meaning of.
func (s *Store) refuseAttemptVersion(r attemptRecord) error {
	if r.TypeMeta == attemptType() {
		return nil
	}
	return s.wrap(&NewerRecordsError{
		Record: fmt.Sprintf("attempt %d of %s", r.Spec.N, r.Spec.Job), Path: attemptsFileName,
		Want: attemptType(), Got: r.TypeMeta,
	})
}

// Attempts is every try the store holds, in the order their last lines were written, as
// stored: [Attempt.Bound] reads one under its job's deadline. An absent file holds none.
func (s *Store) Attempts(ctx context.Context) ([]Attempt, error) {
	log, err := s.attemptsLog()
	if err != nil {
		return nil, err
	}
	recs, err := log.Load()
	if err != nil {
		return nil, s.wrap(err)
	}
	out := make([]Attempt, 0, len(recs))
	for _, r := range recs {
		if err := s.refuseAttemptVersion(r); err != nil {
			return nil, err
		}
		a, err := r.attempt()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", attemptsFileName, err)
		}
		out = append(out, a)
	}
	return out, nil
}

// ChangeLiveAttempt shows change job id's try in progress under the attempts lock, and
// appends it when change reports it changed. It returns the try as change left it.
func (s *Store) ChangeLiveAttempt(ctx context.Context, id string, change func(*Attempt) bool) (Attempt, error) {
	log, err := s.attemptsLog()
	if err != nil {
		return Attempt{}, err
	}
	var live Attempt
	var failed error
	err = log.Update(ctx, func(recs []attemptRecord) []attemptRecord {
		all := make([]Attempt, 0, len(recs))
		for _, r := range recs {
			if failed = s.refuseAttemptVersion(r); failed != nil {
				return nil
			}
			var a Attempt
			if a, failed = r.attempt(); failed != nil {
				return nil
			}
			all = append(all, a)
		}
		live = NewAttempts(all).Live(id)
		if !change(&live) {
			return nil
		}
		return []attemptRecord{recordAttempt(live)}
	})
	if err == nil {
		err = failed
	}
	if err != nil {
		return Attempt{}, fmt.Errorf("job: %s: %w", attemptsFileName, s.wrap(err))
	}
	return live, nil
}

// AppendAttempts appends attempts as their records.
func (s *Store) AppendAttempts(ctx context.Context, attempts ...Attempt) error {
	if len(attempts) == 0 {
		return nil
	}
	log, err := s.attemptsLog()
	if err != nil {
		return err
	}
	return s.wrap(log.Update(ctx, func([]attemptRecord) []attemptRecord {
		out := make([]attemptRecord, len(attempts))
		for i, a := range attempts {
			out[i] = recordAttempt(a)
		}
		return out
	}))
}

// MarshalAttempt is a as the line attempts.jsonl stores, dropped marking a try whose row
// is gone, for a migration that writes a try an older layout held.
func MarshalAttempt(a Attempt, dropped bool) ([]byte, error) {
	r := recordAttempt(a)
	r.Status.Dropped = dropped
	return codec.Marshal(r)
}

// AttemptsPath is the attempts file beside the store's rows.
func (s *Store) AttemptsPath() (string, error) {
	log, err := s.attemptsLog()
	return log.Path, err
}

// dropAttempts ends every try of the jobs gone names, whose rows a write dropped, so an
// id declared again starts on its first try.
func (s *Store) dropAttempts(ctx context.Context, gone map[string]bool) error {
	if len(gone) == 0 {
		return nil
	}
	log, err := s.attemptsLog()
	if err != nil {
		return err
	}
	return s.wrap(log.Update(ctx, func(recs []attemptRecord) []attemptRecord {
		var out []attemptRecord
		for _, r := range recs {
			if gone[r.Spec.Job] {
				r.Status.Dropped = true
				out = append(out, r)
			}
		}
		return out
	}))
}

// archiveAttempts writes the tries of rows to path beside the rows' archive, so a cleared
// or deleted job keeps its results there too.
func (s *Store) archiveAttempts(ctx context.Context, path string, rows []Job) error {
	all, err := s.Attempts(ctx)
	if err != nil {
		return err
	}
	named := map[string]bool{}
	for _, r := range rows {
		named[r.ID] = true
	}
	var lines [][]byte
	for _, a := range all {
		if !named[a.Job] {
			continue
		}
		line, err := codec.Marshal(recordAttempt(a))
		if err != nil {
			return err
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return nil
	}
	return codec.RewriteLines(path, lines, 0o600)
}
