// Package session is the append-only record of what invocations did, kept in a
// directory the caller names.
//
// An INVOCATION is one process's facts, filed under its own id. A SESSION is only ever
// the host's conversation, which an invocation's origin may name, and a caller with no
// host conversation names none: nothing here requires one.
//
// Records are grow-only. Nothing here mutates or rewrites a record, which is what makes
// concurrent producers safe without a lock: on a LOCAL filesystem a POSIX append of one
// short line is atomic, so two processes appending to two invocation files (or the same
// one) cannot interleave a line.
//
// A network mount is outside that guarantee. NFS and SMB implement O_APPEND by seeking
// on the client, so two hosts appending to one file there can interleave mid-line. Such
// an interleave surfaces as a line no reader can decode and lands in [Fold.Skipped],
// never as a record that lies.
//
// A file, however, is not grow-only: [Prune] deletes whole invocation files whose newest
// fact has aged out or that fall past a count cap, and [Open] runs it opportunistically
// so the store bounds itself without a server. A [Tie] the caller supplies keeps records
// that span files from being split, and [Prune] documents why. Because another process
// can prune while this one is folding, every reader here treats a file that disappears
// mid-read as absent rather than damaged.
//
// The package writes nothing to stdout or stderr, reads no environment, and keeps no
// process-wide state; the clock is the caller's through [WithClock].
package session

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/internal/apigroup"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// APIVersion stamps every record this build writes. Readers IGNORE what they cannot
// interpret rather than failing, so an old binary reading a newer store shows less
// instead of refusing. An older record is counted in [Fold.Legacy] and never read, but
// one stamped with [retiredGroup] refuses the store as a [*RetiredGroupError].
const APIVersion apiversion.APIVersion = apigroup.Session

// olderVersion reports whether line is a record stamped older than APIVersion, an
// integer from an older writer included, but for preFoldVersion.
func olderVersion(line []byte) bool {
	var stamp struct {
		V codec.RawMessage `json:"v"`
	}
	if codec.Unmarshal(line, &stamp) != nil || stamp.V == nil || string(stamp.V) == preFoldVersion {
		return false
	}
	var v apiversion.APIVersion
	return codec.Unmarshal(stamp.V, &v) != nil || v.Compare(APIVersion) < 0
}

// preFoldVersion is the integer an older magus stamped APIVersion's records with.
// compat(until: the next release): every worktree appends to one store, and a binary
// built before the rename keeps writing it.
const preFoldVersion = "2"

// compat: see preFoldVersion. An older magus marshaled Record with v first, so the
// prefix is the whole test.
var preFoldPrefix = []byte(`{"v":` + preFoldVersion + `,`)

// readForward is line with a preFoldVersion stamp named APIVersion.
// compat: see preFoldVersion.
func readForward(line []byte) []byte {
	rest, ok := bytes.CutPrefix(line, preFoldPrefix)
	if !ok {
		return line
	}
	return slices.Concat([]byte(`{"v":"`+string(APIVersion)+`",`), rest)
}

// Kinds of fact an invocation records. A reader must tolerate a kind it does not know:
// the set grows, and an invocation written by a newer writer is still readable.
const (
	KindInvocationStart = "invocation_start"
	KindTargetResult    = "target_result"
)

// Outcome values on a [TargetResult].
const (
	OutcomePass = "pass"
	OutcomeFail = "fail"
)

// Record is one fact, and one JSONL line. Payload stays raw so a reader can hand a
// record it does not understand straight through; the field is the schema's escape
// hatch, and decoding it eagerly would turn an unknown kind into a parse error.
type Record struct {
	V          apiversion.APIVersion `json:"v"`
	Invocation string                `json:"invocation"`
	Seq        uint64                `json:"seq"` // monotonic within an invocation, from 1
	Kind       string                `json:"kind"`
	Ts         int64                 `json:"ts"` // unix milliseconds
	Payload    codec.RawMessage      `json:"payload,omitzero"`
}

// InvocationStart is the payload of the first fact an invocation writes: who is running,
// and against what. Its origin's Session is the host's conversation, when a host
// delivered one.
//
// Lease, TraceID, ParentSpanID and Spawner are what the spawning tool CLAIMED, recorded
// verbatim; nothing here corroborates one. Attribution is cooperative, so empty means
// the invocation claimed nothing: it is unattributed, and the origin's User still says
// whose account ran it.
//
// SpanID is the one identity the writer asserts: the invocation mints it. A child that
// reports this value as its ParentSpanID is what makes ancestry readable across
// invocations, so no chain of ancestors is stored anywhere: the relation is derived from
// records, the way a process tree is derived from PPIDs.
type InvocationStart struct {
	job.Origin `json:",inline"`
	Workspace  string `json:"workspace,omitempty"`
	Command    string `json:"command,omitempty"`
	Version    string `json:"version,omitempty"`
	Lease      string `json:"lease,omitempty"`
	// LeaseFrom is which source answered Lease; see job.LeaseSource.
	LeaseFrom    job.LeaseSource `json:"lease_from,omitempty"`
	TraceID      string          `json:"trace_id,omitempty"`
	SpanID       string          `json:"span_id,omitempty"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	Spawner      string          `json:"spawner,omitempty"`
}

// TargetResult is the payload of one target finishing. Replayed distinguishes a cache
// hit from work that actually ran, which is the difference between an invocation that
// did something and one that confirmed something.
//
// Lease repeats the producing invocation's [InvocationStart.Lease] rather than being read
// off the envelope, because a fact is routinely read on its own, without the start record
// that opened the file.
type TargetResult struct {
	Target     string `json:"target"`
	Project    string `json:"project,omitempty"`
	Outcome    string `json:"outcome"`
	DurationMs int64  `json:"duration_ms,omitzero"`
	Replayed   bool   `json:"replayed,omitzero"`
	// Permitted says a deviation ledger let this pass. Outcome stays pass: a push gate
	// and a job's owed check compare on it.
	Permitted bool   `json:"permitted,omitzero"`
	Ref       string `json:"ref,omitempty"`
	// DiagnosticCode is the caller's code for a failure that never ran, such as an
	// admission refusal.
	DiagnosticCode string `json:"diagnostic_code,omitempty"`
	Lease          string `json:"lease,omitempty"`
	// LeaseFrom repeats the start's, for the reason Lease does.
	LeaseFrom job.LeaseSource `json:"lease_from,omitempty"`
	// What the target claimed and cost, each omitted when zero. Sizing names how the
	// claim was sized, empty on a record older than these fields.
	StartedMs   int64  `json:"started_ms,omitzero"`
	Slots       int    `json:"slots,omitzero"`
	MemoryMB    int    `json:"memory_mb,omitzero"`
	Sizing      string `json:"sizing,omitzero"`
	WaitedMs    int64  `json:"waited_ms,omitzero"`
	MaxRSSBytes int64  `json:"max_rss_bytes,omitzero"`
	CPUMs       int64  `json:"cpu_ms,omitzero"`
}

// UnmarshalJSON decodes a result, reading a duration written under either spelling.
//
// compat(until: no session file under the session store still carries "dur_ms"): the
// duration was spelled `dur_ms` before [TargetResult.DurationMs] was. Session files are
// append-only and rotate, so observing that dropping this is safe means finding none left:
//
//	grep -l '"dur_ms"' <store>/*.jsonl
//
// Dropping it early is silent rather than loud: a session summary reports every target it
// recorded before the rename as instant, which reads as a session that replayed everything.
func (r *TargetResult) UnmarshalJSON(b []byte) error {
	type result TargetResult // no method set, so this does not recurse
	var decoded struct {
		result
		LegacyDurationMs int64 `json:"dur_ms"`
	}
	err := codec.Unmarshal(b, &decoded)
	decoded.DurationMs = cmp.Or(decoded.DurationMs, decoded.LegacyDurationMs)
	*r = TargetResult(decoded.result)
	return err
}

// FileExt ends every invocation file's name, and nothing else in a store: a reader
// listing the store for invocations skips any other file.
const FileExt = ".jsonl"

// Bounder is a payload that clamps itself before [Writer.Append] stores it. The bound
// is applied at write time because it protects the store, not the caller: a payload
// carrying text a producer piped in unbounded is a line every later reader pays for,
// and a producer that forgets is the case this exists for.
type Bounder interface {
	Bound() any
}

// Writer appends facts for one invocation. It is safe for concurrent use.
type Writer struct {
	path       string
	invocation string
	now        func() time.Time

	mu    sync.Mutex
	seq   uint64
	start InvocationStart
	begun bool
}

// Open prepares a writer for cfg.Invocation under cfg.Dir, which is created if absent.
//
// It writes NOTHING: the invocation's file appears with the first fact, and cfg.Start is
// emitted as the KindInvocationStart record just ahead of it. A command that produces no
// facts therefore leaves no entry at all, rather than an invocation that only ever says
// it began.
//
// Opening also prunes the store at cfg.Retention with the options' [Tie], which is what
// keeps a grow-only store bounded without a server or a cron: every producer opens, so
// every producer pays a little of the housekeeping. It cannot fail the open: each error
// it meets goes to cfg.PruneErrors; see [Prune]. It runs at most once per
// [pruneInterval], since reading a store of ten thousand invocations cost every run 17ms
// to delete nothing.
//
// An id that already has a file is RESUMED rather than restarted: see [Writer.resume].
//
// It fails with every configuration problem found, joined: an *[OptionError] for each
// refused option, a *[ConfigError] for an empty Dir, a Retention that is not positive and
// a nil PruneErrors, and an [*InvalidIDError] for an Invocation [ValidFileID] refuses. It
// stops early and returns ctx.Err() as is once ctx is done.
func Open(ctx context.Context, cfg Config, opts ...Option) (*Writer, error) {
	c, errs := cfg.checked(opts)
	if !ValidFileID(c.Invocation) {
		errs = append(errs, &InvalidIDError{Field: "invocation id", ID: c.Invocation})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return open(ctx, c)
}

// open is [Open] over a Config [Config.checked] accepted and a valid invocation id.
func open(ctx context.Context, c Config) (*Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return nil, &StoreError{Op: "create store", Path: c.Dir, Err: err}
	}
	stamp := filepath.Join(c.Dir, pruneStamp)
	if fi, err := os.Stat(stamp); err != nil || c.now().Sub(fi.ModTime()) >= pruneInterval {
		claimStalePruneStamp(ctx, stamp, c.Invocation+FileExt, c)
	}
	w := &Writer{path: filepath.Join(c.Dir, c.Invocation+FileExt), invocation: c.Invocation, now: c.now, start: c.Start}
	w.resume()
	return w, nil
}

// resume continues the numbering an earlier writer left in the file.
//
// Two processes reach one file whenever an id is reused: a retried command, a server and
// a CLI sharing an invocation id. Starting every writer at seq 1 makes (Invocation, Seq)
// stop identifying one record: the second process stamps numbers the first already used,
// and the fold's tie-break then interleaves two runs' facts arbitrarily within a
// millisecond.
//
// It seeds the sequence only. The resumed writer still emits its own start record,
// because it is a different process with its own command line, and the store says so
// rather than inheriting the first one's.
//
// A fresh invocation, which is the common case, costs one failed open: the file does not
// exist until the first fact.
func (w *Writer) resume() {
	for _, rec := range decodeFile(w.path).records {
		if rec.Seq > w.seq {
			w.seq = rec.Seq
		}
	}
}

// Append records one fact. payload is marshaled as the record's payload object; a nil
// payload writes the fact with none, and a [Bounder] is stored as it bounds itself.
//
// The first call also writes the start record, so seq 1 is always the start. Errors are
// returned rather than swallowed, but callers on a run path must not fail a build over
// one: a store that can break a build is worse than no store.
//
// It stops early and returns ctx.Err() as is once ctx is done.
func (w *Writer) Append(ctx context.Context, kind string, payload any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if b, ok := payload.(Bounder); ok {
		payload = b.Bound()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.begun {
		if err := w.write(KindInvocationStart, w.start); err != nil {
			return err
		}
		w.begun = true
	}
	return w.write(kind, payload)
}

// write appends one line. The caller holds w.mu, which is what keeps seq in step with
// the order lines land in the file.
func (w *Writer) write(kind string, payload any) error {
	rec := Record{V: APIVersion, Invocation: w.invocation, Seq: w.seq + 1, Kind: kind, Ts: w.now().UnixMilli()}
	if payload != nil {
		raw, err := codec.Marshal(payload)
		if err != nil {
			return &StoreError{Op: "encode " + kind + " payload", Path: w.path, Err: err}
		}
		rec.Payload = raw
	}
	line, err := codec.Marshal(rec)
	if err != nil {
		return &StoreError{Op: "encode " + kind + " record", Path: w.path, Err: err}
	}

	// Opened and closed per append rather than held: an invocation's facts are
	// low-frequency, and a long-lived handle would need a close nobody on a CLI path is
	// positioned to run.
	lines := [][]byte{line}
	if unfinishedTail(w.path) {
		// A writer died mid-line: end that line, so this record is not glued onto it.
		lines = [][]byte{nil, line}
	}
	if err := codec.AppendLines(w.path, lines, 0o600, codec.WithoutSync()); err != nil {
		return &StoreError{Op: "append " + kind + " record", Path: w.path, Err: err}
	}
	w.seq++
	return nil
}

// unfinishedTail reports whether the file at path ends in bytes no newline closes. An
// absent or unreadable file reports false: the append itself then says what is wrong.
func unfinishedTail(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return false
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, info.Size()-1); err != nil {
		return false
	}
	return last[0] != '\n'
}

// Fold is the union of every invocation file in one store, ordered.
type Fold struct {
	// Records are ordered by (Ts, Invocation, Seq). Invocation and Seq break the tie
	// because wall-clock alone cannot: two writers appending in the same millisecond
	// would otherwise order differently on each read.
	Records []Record

	// Invocations counts the files read, INCLUDING any that contributed no usable
	// record: the difference between "no invocations" and "invocations nobody could
	// parse" is the thing a reader most needs to see.
	Invocations int

	// Skipped counts lines that were not a decodable record. A file is written by a
	// process that can be killed mid-line, so a truncated tail is expected, not
	// corruption to report as an error. A line longer than the reader's budget counts
	// here too, and costs only itself: the records after it still load.
	Skipped int

	// Legacy counts schema-1 lines written before a per-process id was called an
	// invocation, keyed `session`. This build does not read them; they are counted apart
	// from Skipped so they are not reported as damage, and [Prune] ages their files by the
	// timestamps those lines still carry.
	Legacy int
}

// ReadAll folds every invocation file under dir.
//
// A missing store is an empty Fold and no error: nothing has run yet. An undecodable
// line is skipped and counted in Skipped rather than failing the read, so one killed
// process cannot make the whole history unreadable. A file listed by the directory but
// gone by the time it is opened was pruned by another process mid-fold; it counts as
// neither an invocation nor damage. An unreadable directory, and a file that cannot be
// opened or read to its end, are a [*StoreError]: a fold missing a file's records is not
// the history. So is a line stamped with the group these records had before the module
// took them, which is a
// [*RetiredGroupError], and a loaded event holding a retired guard decision, which is a
// [*RetiredDecisionError].
//
// It stops early and returns ctx.Err() as is once ctx is done.
func ReadAll(ctx context.Context, dir string) (Fold, error) {
	var fold Fold
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fold, nil
		}
		return fold, &StoreError{Op: "read store", Path: dir, Err: err}
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return Fold{}, err
		}
		if e.IsDir() || !strings.HasSuffix(e.Name(), FileExt) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		read := decodeFile(path)
		if read.vanished {
			continue
		}
		if read.err != nil {
			return Fold{}, &StoreError{Op: "read invocation", Path: path, Err: read.err}
		}
		if err := read.refusal(path); err != nil {
			return Fold{}, err
		}
		fold.Invocations++
		fold.Records = append(fold.Records, read.records...)
		fold.Skipped += read.skipped
		fold.Legacy += read.legacy
	}
	sortRecords(fold.Records)
	return fold, nil
}

// ReadInvocation returns the records one invocation's file holds, in file order, or none
// when it has no file. It reads that file alone: a caller that knows which invocation
// holds its answer need not fold the store. An id [ValidFileID] refuses, which could name
// a file outside dir, is an [*InvalidIDError] before anything is opened.
//
// It refuses the file as [ReadAll] refuses the store: a [*RetiredGroupError] or a
// [*RetiredDecisionError]. A file that cannot be opened or read to its end is a
// [*StoreError]; an undecodable line is skipped, as ReadAll skips it.
//
// It stops early and returns ctx.Err() as is once ctx is done.
func ReadInvocation(ctx context.Context, dir, invocation string) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !ValidFileID(invocation) {
		return nil, &InvalidIDError{Field: "invocation id", ID: invocation}
	}
	path := filepath.Join(dir, invocation+FileExt)
	read := decodeFile(path)
	if read.err != nil {
		return nil, &StoreError{Op: "read invocation", Path: path, Err: read.err}
	}
	if err := read.refusal(path); err != nil {
		return nil, err
	}
	return read.records, nil
}

// StoreError is a store operation that failed on the filesystem or in encoding: Op says
// what was being done, Path where.
type StoreError struct {
	Op   string
	Path string
	Err  error
}

func (e *StoreError) Error() string { return fmt.Sprintf("session: %s %s: %v", e.Op, e.Path, e.Err) }

func (e *StoreError) Unwrap() error { return e.Err }

// retiredGroup is the API group a session record carried before the module took the
// store. A record stamped with it is refused, never read forward.
const retiredGroup = "magus.sessions"

// retiredStamp is line's version when its group is [retiredGroup], and empty otherwise.
func retiredStamp(line []byte) apiversion.APIVersion {
	var stamp struct {
		V apiversion.APIVersion `json:"v"`
	}
	if codec.Unmarshal(line, &stamp) != nil || stamp.V.Group() != retiredGroup {
		return ""
	}
	return stamp.V
}

// RetiredGroupError is an invocation file holding a record stamped with the group session
// records had before they moved to the module, as magus.sessions/v1alpha1. The store is
// refused rather than read around it, since every record before the rename carries it.
type RetiredGroupError struct {
	// Path is the invocation file holding the record.
	Path    string
	Version apiversion.APIVersion
}

func (e *RetiredGroupError) Error() string {
	return fmt.Sprintf("session: %s holds a record stamped %s, a group this build no longer reads (it reads %s)", e.Path, e.Version, APIVersion)
}

// RetiredDecisionError is a loaded event whose verdict is "pass" or "allow", the names
// the guard's defer had before its decisions became defer, advise, ask and deny. The
// store is refused rather than read around it: a skipped event would drop out of every
// count with nothing said.
type RetiredDecisionError struct {
	// Path is the invocation file holding the event.
	Path     string
	Decision string
}

func (e *RetiredDecisionError) Error() string {
	return fmt.Sprintf("session: %s holds the retired guard decision %q, which this build no longer reads", e.Path, e.Decision)
}

// retiredDecisions are the decision names no build after the defer rename writes.
func retiredDecisions() []string { return []string{"pass", "allow"} }

// refuseRetiredDecision returns a [*RetiredDecisionError] for the first loaded event in
// records, read from path, that holds a retired decision.
func refuseRetiredDecision(path string, records []Record) error {
	for _, rec := range records {
		if rec.Kind != KindAgentEvent {
			continue
		}
		var ev AgentEvent
		var unknown *verdict.UnknownDecisionError
		if err := codec.Unmarshal(rec.Payload, &ev); errors.As(err, &unknown) && slices.Contains(retiredDecisions(), unknown.Decision) {
			return &RetiredDecisionError{Path: path, Decision: unknown.Decision}
		}
	}
	return nil
}

// sortRecords puts a fold in the (Ts, Invocation, Seq) order every reader downstream is
// specified against. Anything that assembles a [Fold] outside [ReadAll] has to apply it
// too.
func sortRecords(records []Record) {
	slices.SortStableFunc(records, func(a, b Record) int {
		if c := cmp.Compare(a.Ts, b.Ts); c != 0 {
			return c
		}
		if c := strings.Compare(a.Invocation, b.Invocation); c != 0 {
			return c
		}
		return cmp.Compare(a.Seq, b.Seq)
	})
}

// maxLineBytes is the longest line decodeFile will try to decode. A record carries no
// bodies (a target ref, not its output) and a [Bounder] bounds the free text a producer
// controls, so a line past this was written by something that did not agree with those
// rules: an older writer, or a hand-edited file. It is skipped like any other unusable
// line, and only itself.
const maxLineBytes = 1 << 20

// fileRead is what [decodeFile] made of one invocation file.
type fileRead struct {
	records []Record
	// skipped counts the lines it could not use.
	skipped int
	// legacy counts schema-1 lines keyed `session`, which decode but name no invocation,
	// and lines stamped with [retiredGroup].
	legacy int
	// retired is the first [retiredGroup] stamp, empty when no line carries one.
	retired apiversion.APIVersion
	// vanished says the file was not there at all.
	vanished bool
	// err is the open or read failure that ended the file early, nil when it was read to
	// its end. It is also counted in skipped.
	err error
}

// refusal is the error a reader refuses the file read from path with, nil when it holds
// neither a retired group nor a retired decision.
func (r fileRead) refusal(path string) error {
	if r.retired != "" {
		return &RetiredGroupError{Path: path, Version: r.retired}
	}
	return refuseRetiredDecision(path, r.records)
}

// readFile is [decodeFile]'s result as pruning reads it, without the failure that ended
// the file: a file it cannot read dates nothing, so pruning keeps it.
func readFile(path string) (records []Record, skipped, legacy int, retired apiversion.APIVersion, vanished bool) {
	r := decodeFile(path)
	return r.records, r.skipped, r.legacy, r.retired, r.vanished
}

// decodeFile decodes one invocation file, returning what it could read, how many lines it
// could not, and whether the file was not there at all.
//
// The vanished case is separated from the unreadable one because pruning can delete a
// file between a caller's directory listing and this open. Nothing was lost that the
// caller could have read, so counting it as damage would report corruption every time
// housekeeping ran. Any OTHER open or read failure (a permission, a device error) is
// real: it is err, which every reader of records returns.
func decodeFile(path string) fileRead {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileRead{vanished: true}
		}
		return fileRead{skipped: 1, err: err}
	}
	defer func() { _ = f.Close() }()

	var out fileRead
	br := bufio.NewReader(f)
	for {
		line, over, err := readLine(br, maxLineBytes)
		if over {
			// One oversized line costs one record and nothing else. Stopping the file
			// here (which is what a bufio.Scanner does on ErrTooLong) would hide every
			// LATER record, and a later record is exactly what a reader pairing records
			// looks for.
			out.skipped++
		} else if trimmed := readForward(bytes.TrimSpace(line)); len(trimmed) > 0 {
			var rec Record
			switch {
			case retiredStamp(trimmed) != "":
				out.legacy++
				out.retired = cmp.Or(out.retired, retiredStamp(trimmed))
			case olderVersion(trimmed):
				out.legacy++
			case codec.Unmarshal(trimmed, &rec) != nil || rec.Kind == "":
				out.skipped++
			case rec.Invocation == "":
				out.skipped++
			default:
				out.records = append(out.records, rec)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				out.skipped++
				out.err = err
			}
			return out
		}
	}
}

// readLine reads one newline-terminated line, discarding whatever it holds past limit.
// over reports that the line was cut; err is io.EOF at the end of the file, or the read
// failure that ended it.
//
// Discarding the remainder rather than buffering it is what bounds the reader's memory
// against a line nobody can use, and the file stays positioned at the start of the next
// line either way, so nothing after the oversized line is lost.
func readLine(br *bufio.Reader, limit int) ([]byte, bool, error) {
	var line []byte
	var over bool
	for {
		chunk, err := br.ReadSlice('\n')
		if room := limit - len(line); len(chunk) > room {
			over = true
			line = append(line, chunk[:room]...)
		} else {
			line = append(line, chunk...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, over, err
	}
}

// Summary is one invocation as a reader meets it: who, when, and what it ran. Session is
// the host's conversation it ran in, empty when no host delivered one.
type Summary struct {
	Invocation string `json:"invocation"`
	// Origin is the start record's, whole: the host session it ran in, the OS account, the
	// entry point and the rest, rather than a copy of some of its fields.
	job.Origin   `json:",inline"`
	Lease        string          `json:"lease,omitempty"`
	LeaseFrom    job.LeaseSource `json:"lease_from,omitempty"`
	TraceID      string          `json:"trace_id,omitempty"`
	SpanID       string          `json:"span_id,omitempty"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	Spawner      string          `json:"spawner,omitempty"`
	Workspace    string          `json:"workspace,omitempty"`
	Command      string          `json:"command,omitempty"`
	StartedMs    int64           `json:"started_ms"`
	LastMs       int64           `json:"last_ms"`
	Facts        int             `json:"facts"`
	Events       int             `json:"events"` // of Facts, the ones a load put here
	Targets      []TargetResult  `json:"targets,omitempty"`
	// Model is the [Agent] pair off the NEWEST loaded [AgentEvent] that named one, so a
	// session spanning a model or host switch reports what it ended on rather than what it
	// began with. Zero when no loaded event named either. Its key is not "agent", which the
	// inlined Origin's subagent id already spells.
	Model Agent `json:"model,omitzero"`
	// pairAtMs is the AtMs of the event Model came from, kept only to decide whether a
	// later event's pair should replace it; never serialized.
	pairAtMs int64
}

// Summarize groups a fold into one entry per invocation, most recent activity first.
//
// A record whose kind this build does not know still counts toward Facts and still
// advances LastMs: an invocation that did something this build cannot yet describe is
// still one that was active, and hiding it would be a worse lie than showing it with an
// empty target list.
func Summarize(fold Fold) []Summary {
	order := make([]string, 0, fold.Invocations)
	byID := make(map[string]*Summary, fold.Invocations)

	for _, rec := range fold.Records {
		s := byID[rec.Invocation]
		if s == nil {
			s = &Summary{Invocation: rec.Invocation, StartedMs: rec.Ts}
			byID[rec.Invocation] = s
			order = append(order, rec.Invocation)
		}
		s.Facts++
		if rec.Ts > s.LastMs {
			s.LastMs = rec.Ts
		}
		switch rec.Kind {
		case KindInvocationStart:
			var start InvocationStart
			if codec.Unmarshal(rec.Payload, &start) == nil {
				s.Origin, s.Workspace, s.Command, s.Lease, s.LeaseFrom = start.Origin, start.Workspace, start.Command, start.Lease, start.LeaseFrom
				s.TraceID, s.SpanID, s.ParentSpanID, s.Spawner = start.TraceID, start.SpanID, start.ParentSpanID, start.Spawner
			}
		case KindTargetResult:
			var result TargetResult
			if codec.Unmarshal(rec.Payload, &result) == nil {
				s.Targets = append(s.Targets, result)
			}
		case KindAgentEvent:
			s.Events++
			var ev AgentEvent
			if codec.Unmarshal(rec.Payload, &ev) == nil && ev.Agent != (Agent{}) && ev.AtMs >= s.pairAtMs {
				s.Model, s.pairAtMs = ev.Agent, ev.AtMs
			}
		}
	}

	out := make([]Summary, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	slices.SortStableFunc(out, func(a, b Summary) int { return cmp.Compare(b.LastMs, a.LastMs) })
	return out
}

// The second producer of a store, beside a run path: a host transcript, normalized by a
// loader and stored through [LoadEvents]. A loaded file is keyed by the HOST's session
// id, so its invocation id is that id, and its start carries the host's own label and
// session.
//
// Only the storage side lives here. Extraction is a per-host adapter, and re-judging a
// command belongs beside the rules that judge it, so both stay with the caller.

// KindAgentEvent is one normalized event from a host's own record of a session.
const KindAgentEvent = "agent_event"

// The events a load accepts. They are this package's vocabulary rather than any host's:
// a loader maps its host's tool names onto these.
const (
	EventShellCommand = "shell.command"
	EventFileRead     = "file.read"
	EventFileWrite    = "file.write"
	EventSkillLoad    = "skill.load"
	EventHookOutput   = "hook.output"
	EventSpawn        = "spawn"
	EventMagusCall    = "magus.call"
)

// EventKinds is the accepted set, in the order a diagnostic lists it, as a fresh slice
// the caller may keep.
func EventKinds() []string {
	return []string{
		EventShellCommand, EventFileRead, EventFileWrite,
		EventSkillLoad, EventHookOutput, EventSpawn, EventMagusCall,
	}
}

// ValidEventKind reports whether kind is one a store keeps.
func ValidEventKind(kind string) bool { return slices.Contains(EventKinds(), kind) }

// Agent is which model produced an event and which release of its host ran it: one fact
// ("what produced this"), not two, so it is one value rather than a pair of loose fields
// that happen to travel together. Both are opaque strings, compared for change only;
// nothing here interprets either.
type Agent struct {
	// Model is the model that produced this event, as the host's own record names it.
	// Empty when the host's record does not carry one.
	Model string `json:"model,omitempty"`
	// HostVersion is the agent host's own release at the moment of this event. Distinct
	// from AgentEvent.Host, which names the PROGRAM; this is the version OF it.
	HostVersion string `json:"host_version,omitempty"`
}

// AgentEvent is the payload of a [KindAgentEvent] record: one thing a host observed its
// session do.
//
// A shell command's own text is NEVER stored, whatever the loader was handed: a load
// ingests a whole session's history at once, unattended, which is where storing a
// secret would cost most. Program, Verdict, Rule and Digest are what survives, and Ref
// plus Transcript are how a reader opens the original where the host already put it.
//
// Every other kind stores Text as given: a path, a skill name, or a hook's own output.
type AgentEvent struct {
	Host string `json:"host"`
	// Agent is the model and host-version pair, zero when the host's record carries
	// neither.
	Agent Agent `json:"agent,omitzero"`
	// Kind is one of [EventKinds]. The wire name stays "event" so the payloads already in
	// a store keep decoding.
	Kind string `json:"event"`
	// Ref is the host's id for the thing this event is about, and the dedup key together
	// with Host, the session and Kind. Kind is part of the key because a hook.output
	// shares its ref with the shell.command it judged: the loader emits both from one host
	// record. It is what makes a re-run of a load idempotent.
	Ref string `json:"ref"`
	// AtMs is when the HOST saw the event, in unix milliseconds. The record's own Ts is
	// when the load ran, which is a different fact and the wrong one to age against.
	AtMs       int64  `json:"at"`
	Text       string `json:"text,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	// Program is the command's resolved program name, arguments dropped.
	Program string `json:"program,omitempty"`
	// Verdict is what today's rules say about the command: defer, advise or deny. It is
	// re-judged at load time rather than read off the transcript, so it answers whether
	// the CURRENT rules would have caught a past command.
	Verdict verdict.Decision `json:"verdict,omitzero"`
	// Rule names the deny rule or the advisory kind behind Verdict. The rule's own
	// argument is deliberately absent: it renders the resolved argv, which is the content
	// Text is barred from carrying.
	Rule string `json:"rule,omitempty"`
	// Digest is the sha256 of the command text, so two events can be compared for
	// sameness without either being readable.
	Digest string `json:"digest,omitempty"`
	// Exit is the command's status where the host records one; hosts that do not report
	// exit codes leave it zero, which is why Denied and Interrupted are separate fields
	// rather than inferred from it.
	Exit        int  `json:"exit"`
	Denied      bool `json:"denied"`
	Interrupted bool `json:"interrupted"`
	// NextServed are the hint ids this call's RESULT served, and NextFollowed the ids this
	// call's COMMAND took up from a result served before it. The loader joins both;
	// nothing a host reports fills either.
	NextServed   []string `json:"next_served,omitempty"`
	NextFollowed []string `json:"next_followed,omitempty"`
}

// LoadEvent is one event with the session it belongs to, as [LoadEvents] takes it.
type LoadEvent struct {
	Session string
	Event   AgentEvent
}

// LoadResult reports what one load did. ByKind counts what was WRITTEN rather than what
// arrived, so a re-run that dedups everything reports nothing loaded and no kinds,
// instead of a breakdown of work it did not do.
type LoadResult struct {
	Loaded  int            `json:"loaded"`
	Deduped int            `json:"deduped"`
	ByKind  map[string]int `json:"by_kind,omitempty"`
}

// LoadEvents appends every event dir does not already hold, keyed on (session, host,
// kind, ref), and reports how many it wrote and how many it skipped.
//
// Loading the same stream twice is therefore a no-op, which is the property loaders are
// built on: a loader re-reads whole transcript files rather than tracking where it
// stopped, and the store is what makes that cheap instead of duplicative.
//
// The store is cfg.Dir, and every writer the load opens is configured as [Open] would be
// from cfg. Each session's events are filed under that session's id, so cfg.Invocation
// is ignored. cfg.Start supplies the fields a start record carries beyond the events
// themselves; Host and Session are overwritten per session from the events, since that
// is the join this store exists to record. Events are written in the order given, so a
// caller that wants them ordered orders them first. ctx bounds the wait for another
// loader's lock and nothing else.
//
// A configuration [Open] refuses fails the load before it reads anything, with the same
// errors, and a session id [ValidFileID] refuses fails it at that session with an
// [*InvalidIDError].
func LoadEvents(ctx context.Context, cfg Config, events []LoadEvent, opts ...Option) (LoadResult, error) {
	c, errs := cfg.checked(opts)
	if err := errors.Join(errs...); err != nil {
		return LoadResult{}, err
	}
	// The one operation in this package that is NOT append-only, and therefore the one
	// that needs a lock. Everything else appends a line, which POSIX makes atomic on a
	// local filesystem; this reads the whole store to build a dedup set and then appends
	// against it, so two loaders that both read before either writes each believe the
	// other's events are new and store them twice, permanently, because the dedup set is
	// a snapshot.
	//
	// The store is made first, owner-only as Open makes it, because the lock would create
	// it world-searchable.
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return LoadResult{}, &StoreError{Op: "create store", Path: c.Dir, Err: err}
	}
	var result LoadResult
	err := codec.WithLock(ctx, filepath.Join(c.Dir, loadLockName), loadLockTimeout, func() error {
		var err error
		result, err = loadEvents(ctx, c, events)
		return err
	})
	return result, err
}

// loadEvents is LoadEvents' body, run under the store's load lock with a Config
// [Config.checked] accepted.
func loadEvents(ctx context.Context, c Config, events []LoadEvent) (LoadResult, error) {
	fold, err := ReadAll(ctx, c.Dir)
	if err != nil {
		return LoadResult{}, err
	}
	seen := loadedKeys(fold)

	result := LoadResult{ByKind: map[string]int{}}
	writers := map[string]*Writer{}
	for _, ev := range events {
		key := eventKey(ev.Session, ev.Event)
		if stored, ok := seen[key]; ok && !addsNext(stored, ev.Event) {
			result.Deduped++
			continue
		}
		w := writers[ev.Session]
		if w == nil {
			if !ValidFileID(ev.Session) {
				return result, &InvalidIDError{Field: "invocation id", ID: ev.Session}
			}
			sc := c
			sc.Invocation = ev.Session
			sc.Start.Host, sc.Start.Session = ev.Event.Host, ev.Session
			if w, err = open(ctx, sc); err != nil {
				return result, err
			}
			writers[ev.Session] = w
		}
		if err := w.Append(ctx, KindAgentEvent, ev.Event); err != nil {
			return result, err
		}
		seen[key] = ev.Event
		result.Loaded++
		result.ByKind[ev.Event.Kind]++
	}
	return result, nil
}

// loadedKeys is the dedup set: every event the store already holds, by eventKey.
func loadedKeys(fold Fold) map[string]AgentEvent {
	seen := make(map[string]AgentEvent)
	for session, ev := range EachAgentEvent(fold) {
		seen[eventKey(session, ev)] = ev
	}
	return seen
}

// addsNext reports whether next carries a hint id stored does not, which is the one
// reason to re-append an event the store already holds.
//
// A transcript loaded before its hint ids were known stores the call with no ids on it.
// Without this the stamp is one-shot and unrecoverable: every later id about that call
// would be permanently uncountable.
func addsNext(stored, next AgentEvent) bool {
	return !isSubset(next.NextServed, stored.NextServed) || !isSubset(next.NextFollowed, stored.NextFollowed)
}

func isSubset(ids, of []string) bool {
	for _, id := range ids {
		if !slices.Contains(of, id) {
			return false
		}
	}
	return true
}

// eventKey is the identity a load dedups on: (session, host, kind, ref).
func eventKey(session string, ev AgentEvent) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", session, ev.Host, ev.Kind, ev.Ref)
}

// EachAgentEvent yields every loaded event in the fold with its session, in fold order.
// A payload this build cannot decode is skipped, the tolerance every reader of the store
// applies to a record it does not understand.
//
// One yield per eventKey, at its FIRST position, carrying the hint ids every record of it
// names. The store is append-only, so a re-load that recovered ids a transcript was
// loaded too early to carry writes a second record of the same call; folding them here
// is what keeps a repaired store from counting that call twice.
func EachAgentEvent(fold Fold) iter.Seq2[string, AgentEvent] {
	return func(yield func(string, AgentEvent) bool) {
		merged := map[string]LoadEvent{}
		var order []string
		for _, rec := range fold.Records {
			if rec.Kind != KindAgentEvent {
				continue
			}
			var ev AgentEvent
			if codec.Unmarshal(rec.Payload, &ev) != nil {
				continue
			}
			// A load files each host session under its own id, so the invocation IS the session.
			key := eventKey(rec.Invocation, ev)
			prev, dup := merged[key]
			if !dup {
				merged[key], order = LoadEvent{Session: rec.Invocation, Event: ev}, append(order, key)
				continue
			}
			prev.Event.NextServed = mergeIDs(prev.Event.NextServed, ev.NextServed)
			prev.Event.NextFollowed = mergeIDs(prev.Event.NextFollowed, ev.NextFollowed)
			merged[key] = prev
		}
		for _, key := range order {
			if !yield(merged[key].Session, merged[key].Event) {
				return
			}
		}
	}
}

// mergeIDs is the union of two hint id lists, order preserved.
func mergeIDs(into, from []string) []string {
	for _, id := range from {
		if !slices.Contains(into, id) {
			into = append(into, id)
		}
	}
	return into
}

// AgentEvents returns the loaded events for one session, in the order the fold holds
// them. An unknown session is an empty slice rather than an error: a session with no
// loaded events and one that never existed look the same to every other reader of this
// store.
func AgentEvents(fold Fold, session string) []AgentEvent {
	var out []AgentEvent
	for s, ev := range EachAgentEvent(fold) {
		if s == session {
			out = append(out, ev)
		}
	}
	return out
}

// NewestEventMs is the newest host timestamp any loaded event carries, or zero when
// nothing has been loaded. It reads [AgentEvent.AtMs] rather than the record's Ts, so a
// store loaded today from a month-old transcript reports the month-old event, which is
// the age a staleness check is asking about.
func NewestEventMs(fold Fold) int64 {
	var newest int64
	for _, ev := range EachAgentEvent(fold) {
		newest = max(newest, ev.AtMs)
	}
	return newest
}
