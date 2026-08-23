// Package sessions is the append-only record of what magus sessions did, kept
// where every worktree of a repository can read it.
//
// It is a third store beside the two that already exist, and SCOPE is what tells
// the three apart:
//
//   - internal/journal is the EXECUTION JOURNAL, scoped to a run: every line one
//     invocation emitted (exec, output, result), discarded with that invocation.
//   - internal/trail is the ACTIVITY TRAIL, scoped to a machine: consequential
//     actions taken against the daemon.
//   - this package holds SESSIONS, scoped to a repository: the durable facts of a
//     session, folded across worktrees. It answers "what has been happening in this
//     repo lately", which neither of the others can, because one is per-run and the
//     other is per-daemon.
//
// The name is NOT internal/journal, which the design called for: that import path
// is taken by the execution journal above, and that package documents itself as a
// stdlib-only leaf. This one reads the user state directory, so it could not live
// there even if the name were free.
//
// Records are grow-only. Nothing here mutates or rewrites a record, which is what
// makes concurrent producers in different worktrees safe without a lock: on a LOCAL
// filesystem a POSIX append of one short line is atomic, so two magus processes
// appending to two session files (or the same one) cannot interleave a line.
//
// A network mount is outside that guarantee. NFS and SMB implement O_APPEND by
// seeking on the client, so two hosts appending to one file there can interleave
// mid-line. The store is user state under XDG_STATE_HOME, which is local on the
// setups magus is built for; where it is not, an interleave surfaces as a line no
// reader can decode and lands in [Fold.Skipped], never as a record that lies.
//
// A file, however, is not grow-only: [Prune] deletes whole session files whose
// newest fact has aged out, and [Open] runs it opportunistically so the store bounds
// itself without a daemon. Two rules keep that from destroying history a reader is
// still using - a still-open attention request pins every file naming it, and a
// request's records are deleted as a unit - and [Prune] documents why each is
// necessary. Because another process can prune while this one is folding, every
// reader here treats a file that disappears mid-read as absent rather than damaged.
package sessions

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/config"
	json "github.com/egladman/magus/internal/json"
)

// SchemaVersion stamps every record written by this build. It exists so a reader
// can tell a record it merely does not recognize from one written by a future
// magus: readers IGNORE what they cannot interpret (an unknown kind, an unknown
// payload field, a higher version) rather than failing, so an old binary reading a
// newer store degrades to showing less instead of refusing to read.
const SchemaVersion = 1

// Kinds of fact a session records. A reader must tolerate a kind it does not know:
// the set grows, and a session written by a newer magus is still readable.
const (
	KindSessionStart = "session_start"
	KindTargetResult = "target_result"
)

// Outcome values on a [TargetResult].
const (
	OutcomePass = "pass"
	OutcomeFail = "fail"
)

// Record is one fact, and one JSONL line. Payload stays raw so a reader can hand a
// record it does not understand straight through - the field is the schema's escape
// hatch, and decoding it eagerly would turn an unknown kind into a parse error.
type Record struct {
	V       int             `json:"v"`
	Session string          `json:"session"`
	Seq     uint64          `json:"seq"` // monotonic within a session, from 1
	Kind    string          `json:"kind"`
	Ts      int64           `json:"ts"` // unix milliseconds
	Payload json.RawMessage `json:"payload,omitempty"`
}

// SessionStart is the payload of the first fact a session writes: who is running
// magus, and against what.
//
// Host is the agent host that drove the session ("claude", "cursor", ...) and is
// EMPTY from the CLI today. Nothing in the run path knows it: agent identity
// reaches magus only through the hook payloads internal/trail records, and joining
// the two stores is later work. An empty Host means "not known", never "a human".
//
// Delegation is the delegation the session was launched under, from the MAGUS_DELEGATION
// environment channel (trail.DelegationFromEnv). Attribution is cooperative: an empty
// Delegation means the session claimed none, which is the designed outcome for anything a
// person started by hand, never an error and never a session that belongs to no work.
//
// Its wire key was renamed with the field rather than pinned. A record written before the
// rename carries "unit" and decodes with an empty Delegation, so it reads as unattributed
// instead of wrong, and this store is machine-local and pruned, so those records age out
// rather than needing a reader that understands both spellings.
type SessionStart struct {
	Host       string `json:"host,omitempty"`
	Workspace  string `json:"workspace,omitempty"`
	Command    string `json:"command,omitempty"`
	Version    string `json:"version,omitempty"`
	Delegation string `json:"delegation,omitempty"`
}

// TargetResult is the payload of one target finishing. Replayed distinguishes a
// cache hit from work that actually ran, which is the difference between a session
// that did something and one that confirmed something.
//
// Delegation repeats the producing session's [SessionStart.Delegation] rather than being read off the
// envelope, because a fact is routinely read on its own: the activity drawer joins one target
// result to a delegation without holding the session-start record that opened the file.
type TargetResult struct {
	Target     string `json:"target"`
	Project    string `json:"project,omitempty"`
	Outcome    string `json:"outcome"`
	DurMs      int64  `json:"dur_ms,omitempty"`
	Replayed   bool   `json:"replayed,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Delegation string `json:"delegation,omitempty"`
}

// sessionRE is the session-id shape, which doubles as the session file's basename:
// it must not be able to escape the store directory.
var sessionRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

const fileExt = ".jsonl"

// Dir resolves the per-repository session store,
// <XDG state>/magus/sessions/<repo-basename>-<hash12>.
//
// The hash keys on repository IDENTITY rather than the checkout path, which is the
// whole point: every git worktree of one repo resolves to the same directory, so a
// session started in one worktree is visible from another. This mirrors
// internal/memory.Dir, deliberately - both answer "state that belongs to the repo,
// not to the checkout", and they must not drift into disagreeing about what a repo
// is.
func Dir(root string) (string, error) {
	base, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("sessions: resolve state dir: %w (set XDG_STATE_HOME to a writable absolute path)", err)
	}
	id := repoIdentity(root)
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(base, "magus", "sessions", filepath.Base(id)+"-"+hex.EncodeToString(sum[:])[:12]), nil
}

// repoIdentity returns the path identifying the repository behind root. A linked
// worktree's .git is a FILE holding "gitdir: <main>/.git/worktrees/<n>"; resolving
// it back to <main> is what makes worktrees share one store. Anything else (a plain
// checkout, or no git at all) identifies as root itself.
//
// Duplicated from internal/memory rather than shared: the original is unexported
// there, and lifting it into a common package would move a decision that belongs to
// each store into a place neither owns. If a third copy appears, extract it then.
func repoIdentity(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
	if gitdir == "" {
		return root
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	if i := strings.Index(filepath.ToSlash(gitdir), "/.git/worktrees/"); i >= 0 {
		return filepath.Clean(gitdir[:i])
	}
	return filepath.Clean(gitdir)
}

// Writer appends facts for one session. It is safe for concurrent use.
type Writer struct {
	path    string
	session string

	mu    sync.Mutex
	seq   uint64
	start SessionStart
	begun bool
}

// Open prepares a writer for session id under dir, which is created if absent.
//
// It writes NOTHING: the session file appears with the first fact, and start is
// emitted as the KindSessionStart record just ahead of it. A magus command that
// produces no facts therefore leaves no session entry at all, rather than a
// session that only ever says it began.
//
// Opening also prunes the store at [DefaultRetention], which is what keeps a
// grow-only store bounded without a daemon or a cron: every producer opens, so
// every producer pays a little of the housekeeping. It is best-effort and cannot
// fail the open - see [Prune].
//
// A session id that already has a file is RESUMED rather than restarted: see
// [Writer.resume].
func Open(dir, session string, start SessionStart) (*Writer, error) {
	if !sessionRE.MatchString(session) {
		return nil, fmt.Errorf("sessions: session id %q must be alphanumeric with - and _ (it names the session file); mint one with journal.NewInvocationID", session)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sessions: create store %s: %w", dir, err)
	}
	prune(dir, DefaultRetention, session+fileExt)
	w := &Writer{path: filepath.Join(dir, session+fileExt), session: session, start: start}
	w.resume()
	return w, nil
}

// resume continues the numbering an earlier writer left in the session file.
//
// Two processes reach one file whenever a session id is reused - a retried command, a
// daemon and a CLI sharing an invocation id. Starting every writer at seq 1 makes
// (Session, Seq) stop identifying one record: the second process stamps numbers the
// first already used, and the fold's tie-break then interleaves two runs' facts
// arbitrarily within a millisecond.
//
// It seeds the sequence only. The resumed writer still emits its own session-start,
// because it is a different invocation with its own command line, and the store
// says so rather than inheriting the first one's.
//
// A fresh session, which is the common case, costs one failed open - the file does
// not exist until the first fact.
func (w *Writer) resume() {
	records, _, _ := readFile(w.path)
	for _, rec := range records {
		if rec.Seq > w.seq {
			w.seq = rec.Seq
		}
	}
}

// Append records one fact. payload is marshaled as the record's payload object; a
// nil payload writes the fact with none.
//
// The first call also writes the session-start record, so seq 1 is always the start.
// An [AttentionOpen] payload is stored with its Message clamped to
// [MaxMessageBytes]. Errors are returned rather than swallowed, but callers on the
// run path must not fail a build over one - a store that can break a build is worse
// than no store.
func (w *Writer) Append(kind string, payload any) error {
	// The message bound is applied HERE rather than at the producer because it
	// protects the store, not the caller: a message is whatever a hook piped to
	// `magus notify` on stdin, and one unbounded line is a line every future reader of
	// this repository pays for. A producer that forgets is the case this exists for.
	if open, ok := payload.(AttentionOpen); ok {
		payload = open.bounded()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.begun {
		if err := w.write(KindSessionStart, w.start); err != nil {
			return err
		}
		w.begun = true
	}
	return w.write(kind, payload)
}

// write appends one line. The caller holds w.mu, which is what keeps seq in step
// with the order lines land in the file.
func (w *Writer) write(kind string, payload any) error {
	rec := Record{V: SchemaVersion, Session: w.session, Seq: w.seq + 1, Kind: kind, Ts: time.Now().UnixMilli()}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("sessions: encode %s payload: %w", kind, err)
		}
		rec.Payload = raw
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("sessions: encode %s record: %w", kind, err)
	}

	// Opened and closed per append rather than held: a session's facts are
	// low-frequency, and a long-lived handle would need a close nobody on the CLI
	// path is positioned to run. See internal/trail, which made the same trade.
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("sessions: open %s: %w", w.path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("sessions: append to %s: %w", w.path, err)
	}
	w.seq++
	return nil
}

// Fold is the union of every session file in one store, ordered.
type Fold struct {
	// Records are ordered by (Ts, Session, Seq). Session and Seq break the tie
	// because wall-clock alone cannot: two worktrees appending in the same
	// millisecond would otherwise order differently on each read.
	Records []Record

	// Sessions counts the session files read, INCLUDING any that contributed no
	// usable record - the difference between "no sessions" and "sessions nobody
	// could parse" is the thing a reader most needs to see.
	Sessions int

	// Skipped counts lines that were not a decodable record. A session file is
	// written by a process that can be killed mid-line, so a truncated tail is
	// expected, not corruption to report as an error. A line longer than the reader's
	// budget counts here too, and costs only itself: the records after it still load.
	Skipped int
}

// ReadAll folds every session file under dir.
//
// A missing store is an empty Fold and no error: no session has run yet. An
// undecodable line is skipped and counted in Skipped rather than failing the read,
// so one killed process cannot make the whole history unreadable. A file listed by
// the directory but gone by the time it is opened was pruned by another process
// mid-fold; it counts as neither a session nor damage. Only an unreadable DIRECTORY
// is an error.
func ReadAll(dir string) (Fold, error) {
	var fold Fold
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fold, nil
		}
		return fold, fmt.Errorf("sessions: read store %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		records, skipped, vanished := readFile(filepath.Join(dir, e.Name()))
		if vanished {
			continue
		}
		fold.Sessions++
		fold.Records = append(fold.Records, records...)
		fold.Skipped += skipped
	}
	sortRecords(fold.Records)
	return fold, nil
}

// sortRecords puts a fold in the (Ts, Session, Seq) order every reader downstream is
// specified against - [Attention]'s collapse rules in particular are defined over it.
// Anything that assembles a [Fold] outside [ReadAll] has to apply it too.
func sortRecords(records []Record) {
	slices.SortStableFunc(records, func(a, b Record) int {
		if c := cmp.Compare(a.Ts, b.Ts); c != 0 {
			return c
		}
		if c := strings.Compare(a.Session, b.Session); c != 0 {
			return c
		}
		return cmp.Compare(a.Seq, b.Seq)
	})
}

// maxLineBytes is the longest line readFile will try to decode. A record carries no
// bodies (a target ref, not its output) and [MaxMessageBytes] bounds the one
// free-text field a producer controls, so a line past this was written by something
// that did not agree with those rules - an older magus, or a hand-edited file. It is
// skipped like any other unusable line, and only itself.
const maxLineBytes = 1 << 20

// readFile decodes one session file, returning what it could read, how many lines it
// could not, and whether the file was not there at all.
//
// The vanished case is separated from the unreadable one because pruning can delete a
// file between a caller's directory listing and this open. Nothing was lost that the
// caller could have read, so counting it as damage would report corruption every time
// housekeeping ran. Any OTHER open failure - a permission, a device error - is real
// and counts as a skipped line, on the same reasoning as a corrupt tail: a partial
// history beats a refused one.
func readFile(path string) (records []Record, skipped int, vanished bool) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, true
		}
		return nil, 1, false
	}
	defer func() { _ = f.Close() }()

	br := bufio.NewReader(f)
	for {
		line, over, err := readLine(br, maxLineBytes)
		if over {
			// One oversized line costs one record and nothing else. Stopping the file
			// here - which is what a bufio.Scanner does on ErrTooLong - would hide every
			// LATER record, and a later record is exactly what the attention queue looks
			// for: with the tail invisible, no dispose is ever found and every request in
			// the file re-opens forever.
			skipped++
		} else if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var rec Record
			if json.Unmarshal(trimmed, &rec) != nil || rec.Session == "" || rec.Kind == "" {
				skipped++
			} else {
				records = append(records, rec)
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				skipped++
			}
			return records, skipped, false
		}
	}
}

// readLine reads one newline-terminated line, discarding whatever it holds past
// limit. over reports that the line was cut; err is io.EOF at the end of the file, or
// the read failure that ended it.
//
// Discarding the remainder rather than buffering it is what bounds the reader's
// memory against a line nobody can use, and the file stays positioned at the start of
// the next line either way, so nothing after the oversized line is lost.
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

// Summary is one session as a reader meets it: who, when, and what it ran.
type Summary struct {
	Session    string         `json:"session"`
	Host       string         `json:"host,omitempty"`
	Delegation string         `json:"delegation,omitempty"`
	Workspace  string         `json:"workspace,omitempty"`
	Command    string         `json:"command,omitempty"`
	StartedMs  int64          `json:"started_ms"`
	LastMs     int64          `json:"last_ms"`
	Facts      int            `json:"facts"`
	Targets    []TargetResult `json:"targets,omitempty"`
}

// Summarize groups a fold into one entry per session, most recent activity first.
//
// A record whose kind this build does not know still counts toward Facts and still
// advances LastMs - a session that did something magus cannot yet describe is
// still a session that was active, and hiding it would be a worse lie than showing
// it with an empty target list.
func Summarize(fold Fold) []Summary {
	order := make([]string, 0, fold.Sessions)
	byID := make(map[string]*Summary, fold.Sessions)

	for _, rec := range fold.Records {
		s := byID[rec.Session]
		if s == nil {
			s = &Summary{Session: rec.Session, StartedMs: rec.Ts}
			byID[rec.Session] = s
			order = append(order, rec.Session)
		}
		s.Facts++
		if rec.Ts > s.LastMs {
			s.LastMs = rec.Ts
		}
		switch rec.Kind {
		case KindSessionStart:
			var start SessionStart
			if json.Unmarshal(rec.Payload, &start) == nil {
				s.Host, s.Workspace, s.Command, s.Delegation = start.Host, start.Workspace, start.Command, start.Delegation
			}
		case KindTargetResult:
			var result TargetResult
			if json.Unmarshal(rec.Payload, &result) == nil {
				s.Targets = append(s.Targets, result)
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
