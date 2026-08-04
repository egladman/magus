package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
)

// This file is the key-explanation store: the pre-hash key LINES behind a step's
// cache key, persisted once per key beside the step's attempts, plus the derived
// per-class digests and the stored-vs-live diff. The ref alone cannot be inverted
// into key components; these lines are what let `magus query output <ref> --meta`
// name a key's component classes and `describe target --cache --against <ref>`
// answer "WHY does my machine compute a different ref than CI" with the exact
// disagreeing line.

// keyLinesName is the per-key sidecar holding the step's pre-hash key lines as a JSON
// array: outputs/<cacheKey>/keylines. Deliberately NOT *.out or *.json, so the
// attempt-file scanners (resolveRef, Attempts, ListDescriptors, pruneKey) never
// mistake it for an execution record.
const keyLinesName = "keylines"

// envValueDigestLen truncates a masked env value's digest, matching the ref/class
// truncation length used elsewhere.
const envValueDigestLen = 12

// MaskKeyLines returns lines with every env value replaced by a short digest
// ("env:NAME=abc" -> "env:NAME=sha256:<12hex>"). Env values are the one key-line
// class that routinely carries material a user would not publish (tokens ride env
// vars whether or not a secret provider registered them), so the raw value never
// leaves hashStep: the store persists MASKED lines, and every comparison surface
// masks its live lines the same way - which also keeps the two sides byte-comparable
// (a registry-based redaction would fire on one machine and not the other, turning
// every secret-bearing env line into a false diff). The digest still changes when
// the value changes, so the diff names the exact variable without exposing it.
func MaskKeyLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, "env:")
		if !ok {
			out[i] = line
			continue
		}
		name, value, hasValue := strings.Cut(rest, "=")
		if !hasValue {
			out[i] = line // "env:NAME:unset" carries no value
			continue
		}
		sum := sha256.Sum256([]byte(value))
		out[i] = "env:" + name + "=sha256:" + hex.EncodeToString(sum[:])[:envValueDigestLen]
	}
	return out
}

// PersistKeyLines writes the step's pre-hash key lines beside its attempts, env
// values masked (MaskKeyLines) and the result secret-redacted line-by-line as a
// second net for non-env classes. One file per cache key: the lines are a property
// of the KEY, so later attempts of the same step overwrite with identical content.
// Best-effort at the call site: an error just means a later --meta/--against has no
// lines to explain with.
func (s *OutputStore) PersistKeyLines(ctx context.Context, cacheKey string, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	dir := filepath.Join(s.outputsDir(), cacheKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	masked := MaskKeyLines(lines)
	// Redact the plain text, not the marshaled JSON: a secret containing a character
	// json escapes would not match its escaped form, and would land on disk raw.
	redacted := strings.Split(string(secret.Redact(ctx, []byte(strings.Join(masked, "\n")))), "\n")
	data, err := json.Marshal(redacted)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, keyLinesName), data, 0o644)
}

// KeyLinesByRef returns the stored pre-hash key lines behind ref (step ref, unique
// prefix, or any attempt id within the step). fs.ErrNotExist when the step resolves
// but predates keyline persistence.
func (s *OutputStore) KeyLinesByRef(ref string) ([]string, error) {
	path, err := s.resolveRef(ref)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), keyLinesName))
	if err != nil {
		return nil, err
	}
	var lines []string
	if err := json.Unmarshal(data, &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

// ClassDigest summarizes one component class of a cache key: every key line shares a
// label prefix ("src", "env", "tool", ...), and the class digest hashes the class's
// lines in key order. Two machines comparing digests learn WHICH CLASS disagrees
// without shipping the full lines - small enough for a URL fragment - while the full
// lines (CLI side) name the exact file or variable.
type ClassDigest struct {
	Class  string `json:"class"`
	Digest string `json:"digest"` // sha256 over the class's lines, truncated to 12 hex
	Count  int    `json:"count"`  // how many key lines the class contributes
}

// classDigestHexLen matches the ref truncation: enough to compare, short enough to
// ride a URL fragment many times over.
const classDigestHexLen = 12

// ClassDigests folds key lines into one digest per component class, preserving first-
// appearance order (the hash order of the key itself, so output is stable).
func ClassDigests(lines []string) []ClassDigest {
	type acc struct {
		h     []byte
		count int
	}
	var order []string
	byClass := map[string]*acc{}
	for _, line := range lines {
		class, _, _ := strings.Cut(line, ":")
		a, ok := byClass[class]
		if !ok {
			a = &acc{}
			byClass[class] = a
			order = append(order, class)
		}
		a.h = append(a.h, line...)
		a.h = append(a.h, '\n')
		a.count++
	}
	out := make([]ClassDigest, 0, len(order))
	for _, class := range order {
		a := byClass[class]
		sum := sha256.Sum256(a.h)
		out = append(out, ClassDigest{Class: class, Digest: hex.EncodeToString(sum[:])[:classDigestHexLen], Count: a.count})
	}
	return out
}

// KeyLineDiff is one component class's stored-vs-live disagreement: lines only the
// stored key has and lines only the live key has. A class absent from the slice
// matched exactly.
type KeyLineDiff struct {
	Class      string   `json:"class"`
	StoredOnly []string `json:"stored_only,omitempty"`
	LiveOnly   []string `json:"live_only,omitempty"`
}

// DiffKeyLines compares two key-line sets and returns the disagreeing classes in
// stored-key order (then any live-only classes in live order). Line identity is the
// whole line: a source file whose hash changed appears once under StoredOnly (the old
// hash) and once under LiveOnly (the new), which is exactly the shape a reader needs
// to see what drifted.
func DiffKeyLines(stored, live []string) []KeyLineDiff {
	storedSet := make(map[string]struct{}, len(stored))
	for _, l := range stored {
		storedSet[l] = struct{}{}
	}
	liveSet := make(map[string]struct{}, len(live))
	for _, l := range live {
		liveSet[l] = struct{}{}
	}
	var order []string
	byClass := map[string]*KeyLineDiff{}
	classOf := func(line string) *KeyLineDiff {
		class, _, _ := strings.Cut(line, ":")
		d, ok := byClass[class]
		if !ok {
			d = &KeyLineDiff{Class: class}
			byClass[class] = d
			order = append(order, class)
		}
		return d
	}
	for _, l := range stored {
		if _, ok := liveSet[l]; !ok {
			d := classOf(l)
			d.StoredOnly = append(d.StoredOnly, l)
		}
	}
	for _, l := range live {
		if _, ok := storedSet[l]; !ok {
			d := classOf(l)
			d.LiveOnly = append(d.LiveOnly, l)
		}
	}
	out := make([]KeyLineDiff, 0, len(order))
	for _, class := range order {
		out = append(out, *byClass[class])
	}
	return out
}
