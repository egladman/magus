// Package sessionjournal is the append-only journal of facts one magus SESSION
// produced, kept where every worktree of a repository can read it.
//
// It is a third thing beside the two journals that already exist, and the
// distinction is what the whole feature rests on:
//
//   - internal/journal is the EXECUTION journal: every line one invocation
//     emitted (exec, output, result), scoped to that run and discarded with it.
//   - internal/trail is the ACTIVITY trail: consequential actions taken against
//     the daemon, scoped to a machine.
//   - this package is the SESSION journal: the durable facts of a session, folded
//     across worktrees. It answers "what has been happening in this repo lately",
//     which neither of the others can, because one is per-run and the other is
//     per-daemon.
//
// The name is NOT internal/journal, which the design called for: that import path
// is taken by the execution journal above, and that package documents itself as a
// stdlib-only leaf. This one reads the user state directory, so it could not live
// there even if the name were free.
//
// Records are grow-only. Nothing here mutates or rewrites a record, which is what
// makes concurrent producers in different worktrees safe without a lock: a POSIX
// append of one short line is atomic, so two magus processes appending to two
// session files (or the same one) cannot interleave a line.
package sessionjournal

import (
	"bufio"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
// newer journal degrades to showing less instead of refusing to read.
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
type SessionStart struct {
	Host      string `json:"host,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	Command   string `json:"command,omitempty"`
	Version   string `json:"version,omitempty"`
}

// TargetResult is the payload of one target finishing. Replayed distinguishes a
// cache hit from work that actually ran, which is the difference between a session
// that did something and one that confirmed something.
type TargetResult struct {
	Target   string `json:"target"`
	Project  string `json:"project,omitempty"`
	Outcome  string `json:"outcome"`
	DurMs    int64  `json:"dur_ms,omitempty"`
	Replayed bool   `json:"replayed,omitempty"`
	Ref      string `json:"ref,omitempty"`
}

// sessionRE is the session-id shape, which doubles as the journal file's basename:
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
		return "", fmt.Errorf("sessionjournal: resolve state dir: %w (set XDG_STATE_HOME to a writable absolute path)", err)
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
// produces no facts therefore leaves no journal entry at all, rather than a
// session that only ever says it began.
func Open(dir, session string, start SessionStart) (*Writer, error) {
	if !sessionRE.MatchString(session) {
		return nil, fmt.Errorf("sessionjournal: session id %q must be alphanumeric with - and _ (it names the journal file); mint one with ProcessSession", session)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("sessionjournal: create store %s: %w", dir, err)
	}
	return &Writer{path: filepath.Join(dir, session+fileExt), session: session, start: start}, nil
}

// Session returns the id this writer appends under.
func (w *Writer) Session() string { return w.session }

// Append records one fact. payload is marshaled as the record's payload object; a
// nil payload writes the fact with none.
//
// The first call also writes the session-start record, so seq 1 is always the
// start. Errors are returned rather than swallowed, but callers on the run path
// must not fail a build over one - a journal that can break a build is worse than
// no journal.
func (w *Writer) Append(kind string, payload any) error {
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
			return fmt.Errorf("sessionjournal: encode %s payload: %w", kind, err)
		}
		rec.Payload = raw
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("sessionjournal: encode %s record: %w", kind, err)
	}

	// Opened and closed per append rather than held: a session's facts are
	// low-frequency, and a long-lived handle would need a close nobody on the CLI
	// path is positioned to run. See internal/trail, which made the same trade.
	f, err := os.OpenFile(w.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("sessionjournal: open %s: %w", w.path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("sessionjournal: append to %s: %w", w.path, err)
	}
	w.seq++
	return nil
}

// Fold is the union of every session journal in one store, ordered.
type Fold struct {
	// Records are ordered by (Ts, Session, Seq). Session and Seq break the tie
	// because wall-clock alone cannot: two worktrees appending in the same
	// millisecond would otherwise order differently on each read.
	Records []Record

	// Sessions counts the journal files read, INCLUDING any that contributed no
	// usable record - the difference between "no sessions" and "sessions nobody
	// could parse" is the thing a reader most needs to see.
	Sessions int

	// Skipped counts lines that were not a decodable record. A journal is written
	// by a process that can be killed mid-line, so a truncated tail is expected,
	// not corruption to report as an error.
	Skipped int
}

// Read folds every session journal under dir.
//
// A missing store is an empty Fold and no error: no session has run yet. An
// undecodable line is skipped and counted in Skipped rather than failing the read,
// so one killed process cannot make the whole history unreadable. Only an
// unreadable DIRECTORY is an error.
func Read(dir string) (Fold, error) {
	var fold Fold
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fold, nil
		}
		return fold, fmt.Errorf("sessionjournal: read store %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		fold.Sessions++
		records, skipped := readFile(filepath.Join(dir, e.Name()))
		fold.Records = append(fold.Records, records...)
		fold.Skipped += skipped
	}
	slices.SortStableFunc(fold.Records, func(a, b Record) int {
		if c := cmp.Compare(a.Ts, b.Ts); c != 0 {
			return c
		}
		if c := strings.Compare(a.Session, b.Session); c != 0 {
			return c
		}
		return cmp.Compare(a.Seq, b.Seq)
	})
	return fold, nil
}

// readFile decodes one session journal, returning what it could read and how many
// lines it could not. An unreadable file counts as no records rather than an error:
// see Read on why a partial history beats a refused one.
func readFile(path string) (records []Record, skipped int) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 1
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	// A record carries no bodies (a target ref, not its output), so the default
	// 64KiB line budget is generous; raising it guards against a pathological
	// target name rather than an expected one.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Session == "" || rec.Kind == "" {
			skipped++
			continue
		}
		records = append(records, rec)
	}
	if sc.Err() != nil {
		skipped++
	}
	return records, skipped
}

// Summary is one session as a reader meets it: who, when, and what it ran.
type Summary struct {
	Session   string         `json:"session"`
	Host      string         `json:"host,omitempty"`
	Workspace string         `json:"workspace,omitempty"`
	Command   string         `json:"command,omitempty"`
	StartedMs int64          `json:"started_ms"`
	LastMs    int64          `json:"last_ms"`
	Facts     int            `json:"facts"`
	Targets   []TargetResult `json:"targets,omitempty"`
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
				s.Host, s.Workspace, s.Command = start.Host, start.Workspace, start.Command
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
