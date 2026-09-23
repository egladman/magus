package hint

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The once-per-session rule for the advisory-tier notices that repeat.
//
// An advisory says nothing the second time it arrives, and this page's own standard
// names the cost: a check that is red by default is a check people learn to ignore,
// taking the real failures with it (docs/doctrine.md). Measured over one session: the
// stale-binary notice and the graph-beats-grep hint each fired dozens of times with
// byte-identical text, and the reader stopped seeing either.
//
// DENIALS ARE NEVER SILENCED. A refusal explains itself every time it refuses, since it
// is the one verdict the caller cannot see past; the guard uses this gate only to shorten
// a repeat to one line and a ref (internal/guard/denial.go).
//
// Only the kinds enrolled below are held to one firing, and enrolling one is deliberate.
// The rest correct the command in front of the reader (a `cd` before magus, a `time`
// wrapper, a chained run), so a second firing reports a second mistake rather than
// repeating a standing fact.

// MarkerKind names one repeatable notice. It is also a filename component, so the
// values stay lowercase letters, digits and dashes; advisoryFocusPath is the one
// that appends digits, and it appends a hash rather than anything a caller chose.
type MarkerKind string

const (
	// markerDir holds one empty marker file per (session, kind), under the same
	// cache base the activity trail writes to. A file rather than process memory because
	// a hook is a short-lived process: nothing survives between two tool calls except
	// what lands on disk.
	markerDir = "advisories"

	// anonWindow bounds a marker that could not be keyed to a session id at all.
	//
	// A caller that reports no session and has no terminal to stand in for one leaves
	// nothing to tell this run from the next, so that marker expires on a clock instead:
	// long enough to cover a working session, short enough that tomorrow's is told the
	// fact again. A caller that DOES name a session needs no window: the id is the
	// session, and its return is the same session.
	//
	// It is the LAST resort rather than the terminal default. A caller at a prompt passes
	// no --session, and keying it on a clock made the gate mean "in the last two hours",
	// so the same advisory went quiet on a second deliberate look and came back unbidden
	// the next morning. WindowFromTerminal is what gives it a real key.
	anonWindow = 2 * time.Hour

	// markerRetention is how long any marker survives the sweep. Long enough that a
	// session resumed the next morning stays quiet, short enough that a machine running
	// many sessions does not accumulate a marker per session forever.
	markerRetention = 7 * 24 * time.Hour
)

// Gate holds each enrolled notice to one firing per session.
type Gate struct {
	cacheDir string
	session  string
}

// NewGate keys the gate on the cache dir the caller already resolved and an opaque
// session key: the host's session id, or a key naming the caller within it, as the
// guard's callerKey does. An empty cacheDir means magus could not locate a workspace,
// and the gate then suppresses nothing.
func NewGate(cacheDir, session string) Gate {
	return Gate{cacheDir: cacheDir, session: strings.TrimSpace(session)}
}

// WindowFromTerminal names the terminal window a caller runs in, the key a gate holds
// notices under when no host delivered a session. Empty when there is no terminal to
// read, which is the pipeline and CI case and correctly falls back to anonWindow.
//
// A window is not a session: a session is the host's conversation id, and a caller at a
// terminal has none. Without this key such a caller shared one "anon" bucket with every
// other unattributed run on the machine, held on a two-hour clock, when what it means is
// this terminal, until it is closed.
//
// The terminal is read from the environment a terminal emulator sets, and falls back to
// the parent process id, which is the shell that invoked magus. Neither is a secret and
// neither is trusted: MarkerPath hashes whatever comes back, so a value holding
// separators or anything else cannot pick a path. Getting it WRONG costs an advisory
// shown twice or held once too long, never a wrong verdict.
func WindowFromTerminal(env func(string) string, ppid int, isTerminal bool) string {
	if !isTerminal {
		return ""
	}
	// TERM_SESSION_ID is macOS Terminal and iTerm2; WINDOWID is X11 terminals. Both
	// name the WINDOW, which outlives a shell restart inside it, so they are preferred
	// over the pid: reopening a shell in the same window is the same sitting.
	for _, key := range []string{"TERM_SESSION_ID", "WINDOWID"} {
		if v := strings.TrimSpace(env(key)); v != "" {
			return "tty:" + v
		}
	}
	if ppid > 1 {
		return "ppid:" + strconv.Itoa(ppid)
	}
	return ""
}

// CacheDir returns the cache dir this gate is keyed on.
func (g Gate) CacheDir() string {
	return g.cacheDir
}

// Session returns the session id this gate is keyed on.
func (g Gate) Session() string {
	return g.session
}

// Once returns text the first time kind fires in this session, and "" on every repeat.
//
// A silent rule never spends its firing: the text is checked before the state is, so a
// rule that had nothing to say has not used up the one time it may speak.
func (g Gate) Once(kind MarkerKind, text string) string {
	return g.OnceOrBrief(kind, text, "")
}

// OnceOrBrief returns full the first time kind fires in this session and brief on every
// repeat after it.
//
// Degrading beats going silent for a family that carries a command to run. Measured over
// 1,499 sessions: 95% of all advisory bytes were same-session repeats, and conversion
// happens on first contact, so a repeat earns its place only at a size nobody has to read
// around. A one-line repeat still names the command; a suppressed one cannot.
//
// TODO: the brief repeats without limit, and one 961-command session re-judged the four
// search advisories as firing 426 times with zero measured uptake. A capped tier that
// went silent after N repeats was tried and REVERTED: it cannot tell a reader ignoring
// the tenth reminder from sixteen workers racing the first firing, and
// TestAdvisoryGateRaceCostsBytesNotVerdicts pins that a raced firing must never come back
// silent. Whatever replaces it has to separate repeats over TIME from concurrent
// duplicates, which the marker alone does not record.
//
// An empty brief goes quiet on the repeat, which is right for a notice reporting a
// condition rather than offering a command.
func (g Gate) OnceOrBrief(kind MarkerKind, full, brief string) string {
	// An unenrolled kind speaks every time, which is what an empty kind MEANS. Reading it
	// as a key instead would give every such advisory one shared marker and silence all of
	// them the moment any one of them fired.
	if full == "" || kind == "" || g.cacheDir == "" {
		return full
	}
	if g.MarkFired(kind) {
		return brief
	}
	return full
}

// AlreadyFired reports whether kind has been reported for this session, WITHOUT marking it.
//
// It exists for the one rule whose TEXT costs a directory walk to produce: asking here
// first keeps that walk off every later tool call of the session, instead of paying it
// only to throw the answer away. Every other caller wants MarkFired, which marks.
func (g Gate) AlreadyFired(kind MarkerKind) bool {
	if kind == "" || g.cacheDir == "" {
		return false
	}
	info, err := os.Stat(g.markerPath(kind))
	if err != nil {
		return false
	}
	return g.session != "" || time.Since(info.ModTime()) < anonWindow
}

// MarkFired marks kind reported for this session and returns whether it ALREADY was: a
// check-and-set, and the return is the check half.
//
// The set half is an exclusive create, not a stat followed by a write. A host runs one
// hook process per tool call and an agent issues tool calls in parallel, so the racing
// callers are the normal case rather than the exotic one: four processes that each stat
// a missing marker and then each write it all believe they are the first, and the reader
// gets the notice four times. That is the shape this gate exists to prevent, arriving
// through the gate itself.
//
// Every failure returns false, which speaks. State magus cannot write is not a reason to
// go quiet: a notice repeated is a smaller failure than a notice nobody ever gets.
func (g Gate) MarkFired(kind MarkerKind) bool {
	if kind == "" || g.cacheDir == "" {
		return false
	}
	path := g.markerPath(kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil {
		_ = f.Close()
		sweepMarkers(filepath.Dir(path))
		return false
	}
	if !os.IsExist(err) {
		return false
	}
	// The marker is there, so someone fired. A session-keyed marker settles it; an
	// anonymous one is held only for its window, and past that this caller takes the
	// firing and restarts the window by rewriting the file.
	if g.session != "" {
		return true
	}
	if info, serr := os.Stat(path); serr == nil && time.Since(info.ModTime()) < anonWindow {
		return true
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return false
	}
	return false
}

// Count adds one to kind's tally for this session and returns the new total.
//
// The tally is the marker's size: each call appends one byte, and the offset the append
// lands at is this caller's own, so racing hook processes each see a distinct total
// rather than all reading the same one.
//
// State magus cannot write answers 1, the same way MarkFired's failures speak: every
// call then reads as the first, which errs toward the rule that nudges rather than the
// one that escalates.
func (g Gate) Count(kind MarkerKind) int {
	if kind == "" || g.cacheDir == "" {
		return 1
	}
	path := g.markerPath(kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 1
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 1
	}
	defer f.Close()
	if _, err := f.Write([]byte{'.'}); err != nil {
		return 1
	}
	end, err := f.Seek(0, io.SeekCurrent)
	if err != nil || end < 1 {
		return 1
	}
	if end == 1 {
		sweepMarkers(filepath.Dir(path))
	}
	return int(end)
}

// LastSeen reports when kind was last touched in this session, and false when it never
// was. It marks nothing.
func (g Gate) LastSeen(kind MarkerKind) (time.Time, bool) {
	if kind == "" || g.cacheDir == "" {
		return time.Time{}, false
	}
	info, err := os.Stat(g.markerPath(kind))
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// Touch records now as the last time kind was seen in this session. A failure records
// nothing, which LastSeen then reports as never seen.
func (g Gate) Touch(kind MarkerKind) {
	if kind == "" || g.cacheDir == "" {
		return
	}
	path := g.markerPath(kind)
	now := time.Now()
	if err := os.Chtimes(path, now, now); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, nil, 0o644); err == nil {
		sweepMarkers(filepath.Dir(path))
	}
}

// MarkerPath names one marker file for session and kind under cacheDir.
//
// The session id is HASHED rather than sanitized. It is a host-chosen string that may
// hold anything at all, including separators, and a path assembled from it is a path an
// unvalidated value picked. The kind stays readable so the directory can be read by a
// person wondering why a notice went quiet.
func MarkerPath(cacheDir, session string, kind MarkerKind) string {
	key := "anon"
	if session != "" {
		sum := sha256.Sum256([]byte(session))
		key = hex.EncodeToString(sum[:6])
	}
	return filepath.Join(cacheDir, markerDir, key+"."+string(kind))
}

// markerPath names one marker file for this gate.
func (g Gate) markerPath(kind MarkerKind) string {
	return MarkerPath(g.cacheDir, g.session, kind)
}

// sweepMarkers removes markers past the retention window.
//
// Called only where a marker is CREATED, which happens at most once per kind per session,
// so the directory read never lands on the path of an ordinary tool call.
func sweepMarkers(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-markerRetention)
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
