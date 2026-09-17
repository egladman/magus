package hint

import (
	"bytes"
	"cmp"
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"

	json "github.com/egladman/magus/internal/json"
)

// The served-next journal: one line per breadcrumb actually put in front of a
// reader, written where the advisory markers already live.
//
// Two readers, one file. The guard treats a command it served within the last few
// calls as already vetted, which needs recency rather than history; `session load`
// joins the same lines onto a host's transcript so uptake per id becomes a query
// instead of a guess at what a result carried. Nothing here is a decision, so a
// write that fails is dropped rather than reported: a journal is not worth failing
// a query over.
const (
	servedNextDir  = "advisories"
	servedNextFile = "served-next.jsonl"

	// servedNextKept bounds the file on rotate. It is a recency window for the
	// guard, not an archive; the store is where a served id lives long enough to be
	// counted.
	servedNextKept = 200

	// servedNextLockWait bounds how long a writer waits for the journal. Past it the
	// holder is stuck rather than busy, and a command answering a reader must not
	// block on its own accounting.
	servedNextLockWait  = 2 * time.Second
	servedNextLockRetry = 5 * time.Millisecond
)

// servedNextMu serializes this process's writers. The OS lock below serializes
// processes and the daemon serves several tools at once, so both are needed.
var servedNextMu sync.Mutex

// ServedNextEntry is one journal line: when a breadcrumb was served, which id, and
// the exact argv the reader was handed.
type ServedNextEntry struct {
	AtMs int64    `json:"ts"`
	ID   string   `json:"id"`
	Argv []string `json:"argv"`
}

// ServedNextPath names the journal under cacheDir. One file per checkout: a served
// breadcrumb clears the same command for every session working in it, which is the
// scope the guard reads it at.
//
// Empty when cacheDir is, which is the caller's signal that there is nowhere to
// write.
func ServedNextPath(cacheDir string) string {
	if cacheDir == "" {
		return ""
	}
	return filepath.Join(cacheDir, servedNextDir, servedNextFile)
}

// AppendServedNext records that next was served, one line each. Best effort
// throughout: every failure returns silently, and an entry that will not marshal is
// skipped rather than losing the rest of the batch.
//
// No ctx, unlike trail.Append: the argv here is magus-minted from templates, so
// there is nothing in it to redact.
func AppendServedNext(cacheDir string, next []Next) {
	journal := ServedNextPath(cacheDir)
	if journal == "" || len(next) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		return
	}
	at := time.Now().UnixMilli()
	lines := make([][]byte, 0, len(next))
	for _, n := range next {
		line, err := json.Marshal(ServedNextEntry{AtMs: at, ID: n.ID, Argv: n.Argv})
		if err != nil {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return
	}

	servedNextMu.Lock()
	defer servedNextMu.Unlock()
	lock := flock.New(journal + ".lock")
	got, err := lock.TryLock()
	if err != nil {
		return
	}
	if !got {
		// A bounded wait rather than a drop: a lost line is a command magus served and
		// then refuses, and the holder is only ever appending a few hundred bytes.
		wait, cancel := context.WithTimeout(context.Background(), servedNextLockWait)
		defer cancel()
		if got, err = lock.TryLockContext(wait, servedNextLockRetry); err != nil || !got {
			return
		}
	}
	defer func() { _ = lock.Unlock() }()

	f, err := os.OpenFile(journal, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	for _, line := range lines {
		// One line per Write, so a short write cannot leave half of the NEXT entry
		// behind the tail of this one.
		if _, err := f.Write(append(line, '\n')); err != nil {
			break
		}
	}
	if err := f.Close(); err != nil {
		return
	}
	rotateServedNext(journal)
}

// rotateServedNext trims the journal to its newest servedNextKept lines. The caller
// holds the lock; the rewrite goes through a sibling and a rename so a reader never
// meets a truncated file.
func rotateServedNext(journal string) {
	raw, err := os.ReadFile(journal)
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) <= servedNextKept {
		return
	}
	kept := append(bytes.Join(lines[len(lines)-servedNextKept:], []byte("\n")), '\n')
	tmp := journal + ".tmp"
	if err := os.WriteFile(tmp, kept, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, journal); err != nil {
		_ = os.Remove(tmp)
	}
}

// ReadServedNext returns every journal entry under cacheDir, oldest first. A line
// that will not decode, names no template, or carries no argv is skipped: the file is
// appended to from several processes, so a torn tail is expected rather than
// exceptional, and an entry nothing can be attributed to is one nobody can count.
func ReadServedNext(cacheDir string) []ServedNextEntry {
	journal := ServedNextPath(cacheDir)
	if journal == "" {
		return nil
	}
	raw, err := os.ReadFile(journal)
	if err != nil {
		return nil
	}
	var out []ServedNextEntry
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry ServedNextEntry
		if err := json.Unmarshal(line, &entry); err != nil || entry.ID == "" || len(entry.Argv) == 0 {
			continue
		}
		out = append(out, entry)
	}
	slices.SortStableFunc(out, func(a, b ServedNextEntry) int { return cmp.Compare(a.AtMs, b.AtMs) })
	return out
}

// NormalizeServedArgv reduces an argv to the form the serving and running sides can
// agree on: the binary by its base name, everything after it verbatim.
//
// The spelling of the binary is the one thing that legitimately differs between what
// was printed and what was run (`./magus` in a worktree, `magus` on PATH, an absolute
// path from a hook template), and it is the one part of the line that changes nothing
// about what the command does.
func NormalizeServedArgv(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	out := slices.Clone(argv)
	out[0] = path.Base(strings.ReplaceAll(out[0], "\\", "/"))
	return out
}
