package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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
// DENIALS ARE EXEMPT, and so is the reason a denial carries. A refusal explains itself
// every time it refuses; that is the one verdict the caller cannot see past.
//
// Only the kinds enrolled below are held to one firing, and enrolling one is deliberate.
// The rest correct the command in front of the reader - a `cd` before magus, a `time`
// wrapper, a chained run - so a second firing reports a second mistake rather than
// repeating a standing fact.

// advisoryKind names one repeatable notice. It is also a filename component, so the
// values stay lowercase letters and dashes.
type advisoryKind string

const (
	advisoryStaleBinary   advisoryKind = "stale-binary"
	advisoryCodeSearch    advisoryKind = "code-search"
	advisoryDocSearch     advisoryKind = "doc-search"
	advisoryPrecedent     advisoryKind = "precedent-search"
	advisoryStageClassify advisoryKind = "stage-classify"
	advisoryUnleasedWrite advisoryKind = "unleased-write"
	advisorySkillSource   advisoryKind = "skill-source"
	advisoryRegenSource   advisoryKind = "regen-source"
	advisoryGraphStale    advisoryKind = "graph-stale"
)

const (
	// advisoryMarkerDir holds one empty marker file per (session, kind), under the same
	// cache base the activity trail writes to. A file rather than process memory because
	// a hook is a short-lived process: nothing survives between two tool calls except
	// what lands on disk.
	advisoryMarkerDir = "advisories"

	// advisoryAnonWindow bounds a marker that could not be keyed to a session id.
	//
	// A host that reports no session leaves nothing to tell this run from the next, so
	// that marker expires on a clock instead: long enough to cover a working session,
	// short enough that tomorrow's session is told the fact again. A host that DOES
	// report one needs no window - the id is the session, and its return is the same
	// session.
	advisoryAnonWindow = 2 * time.Hour

	// advisoryRetention is how long any marker survives the sweep. Long enough that a
	// session resumed the next morning stays quiet, short enough that a machine running
	// many sessions does not accumulate a marker per session forever.
	advisoryRetention = 7 * 24 * time.Hour
)

// advisoryGate holds each enrolled notice to one firing per session.
type advisoryGate struct {
	base    string
	session string
}

// newAdvisoryGate keys the gate on the cache base the hook already resolved and the
// session id the host reported. An empty base means magus could not locate a workspace,
// and the gate then suppresses nothing.
func newAdvisoryGate(base, session string) advisoryGate {
	return advisoryGate{base: base, session: strings.TrimSpace(session)}
}

// once returns text the first time kind fires in this session, and "" on every repeat.
//
// A silent rule never spends its firing: the text is checked before the state is, so a
// rule that had nothing to say has not used up the one time it may speak.
func (g advisoryGate) once(kind advisoryKind, text string) string {
	return g.onceOrBrief(kind, text, "")
}

// onceOrBrief returns full the first time kind fires in this session and brief on every
// repeat after it.
//
// Degrading beats going silent for a family that carries a command to run. Measured over
// 1,499 sessions: 95% of all advisory bytes were same-session repeats, and conversion
// happens on first contact, so a repeat earns its place only at a size nobody has to read
// around. A one-line repeat still names the command; a suppressed one cannot.
//
// An empty brief goes quiet on the repeat, which is right for a notice reporting a
// condition rather than offering a command.
func (g advisoryGate) onceOrBrief(kind advisoryKind, full, brief string) string {
	// An unenrolled kind speaks every time, which is what an empty kind MEANS. Reading it
	// as a key instead would give every such advisory one shared marker and silence all of
	// them the moment any one of them fired.
	if full == "" || kind == "" || g.base == "" {
		return full
	}
	if g.fireOnce(kind) {
		return brief
	}
	return full
}

// seen reports whether kind has already been reported for this session, WITHOUT marking
// it.
//
// It exists for the one rule whose TEXT costs a directory walk to produce: asking here
// first keeps that walk off every later tool call of the session, instead of paying it
// only to throw the answer away. Every other caller wants fireOnce, which marks.
func (g advisoryGate) seen(kind advisoryKind) bool {
	if kind == "" || g.base == "" {
		return false
	}
	info, err := os.Stat(g.markerPath(kind))
	if err != nil {
		return false
	}
	return g.session != "" || time.Since(info.ModTime()) < advisoryAnonWindow
}

// fireOnce reports whether kind was already reported this session, marking it reported
// when it was not: a check-and-set, despite the bool return.
//
// Every failure returns false, which speaks. State magus cannot write is not a reason to
// go quiet: a notice repeated is a smaller failure than a notice nobody ever gets.
func (g advisoryGate) fireOnce(kind advisoryKind) bool {
	if g.seen(kind) {
		return true
	}
	path := g.markerPath(kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false
	}
	// Rewritten rather than created, so an expired anonymous marker starts its window
	// again instead of staying expired forever.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		return false
	}
	sweepAdvisoryMarkers(filepath.Dir(path))
	return false
}

// markerPath names one marker file.
//
// The session id is HASHED rather than sanitized. It is a host-chosen string that may
// hold anything at all, including separators, and a path assembled from it is a path an
// unvalidated value picked. The kind stays readable so the directory can be read by a
// person wondering why a notice went quiet.
func (g advisoryGate) markerPath(kind advisoryKind) string {
	key := "anon"
	if g.session != "" {
		sum := sha256.Sum256([]byte(g.session))
		key = hex.EncodeToString(sum[:6])
	}
	return filepath.Join(g.base, advisoryMarkerDir, key+"."+string(kind))
}

// sweepAdvisoryMarkers removes markers past the retention window.
//
// Called only where a marker is CREATED, which happens at most once per kind per session,
// so the directory read never lands on the path of an ordinary tool call.
func sweepAdvisoryMarkers(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-advisoryRetention)
	for _, e := range entries {
		info, ierr := e.Info()
		if ierr != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
