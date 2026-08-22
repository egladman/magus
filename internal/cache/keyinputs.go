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

// keyInputsName is the per-key sidecar holding the step's pre-hash key inputs as a JSON
// array: outputs/<cacheKey>/key-inputs. Deliberately NOT *.out or *.json, so the
// attempt-file scanners (resolveRef, Attempts, ListDescriptors, pruneKey) never
// mistake it for an execution record.
const keyInputsName = "key-inputs"

// envValueDigestLen truncates a masked env value's digest, matching the ref/class
// truncation length used elsewhere.
const envValueDigestLen = 12

// MaskKeyInputs returns lines with every env value replaced by a short digest
// ("env:NAME=abc" -> "env:NAME=sha256:<12hex>"). Env values are the one key-input
// class that routinely carries material a user would not publish (tokens ride env
// vars whether or not a secret provider registered them), so the raw value never
// leaves hashStep: the store persists MASKED lines, and every comparison surface
// masks its live lines the same way - which also keeps the two sides byte-comparable
// (a registry-based redaction would fire on one machine and not the other, turning
// every secret-bearing env line into a false diff). The digest still changes when
// the value changes, so the diff names the exact variable without exposing it.
func MaskKeyInputs(inputs []string) []string {
	out := make([]string, len(inputs))
	for i, line := range inputs {
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

// PersistKeyInputs writes the step's pre-hash key inputs beside its attempts, env
// values masked (MaskKeyInputs) and the result secret-redacted line-by-line as a
// second net for non-env classes. One file per cache key: the lines are a property
// of the KEY, so later attempts of the same step overwrite with identical content.
// Best-effort at the call site: an error just means a later --meta/--against has no
// lines to explain with.
func (s *OutputStore) PersistKeyInputs(ctx context.Context, cacheKey string, inputs []string) error {
	if len(inputs) == 0 {
		return nil
	}
	dir := filepath.Join(s.outputsDir(), cacheKey)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	masked := MaskKeyInputs(inputs)
	// Redact the plain text, not the marshaled JSON: a secret containing a character
	// json escapes would not match its escaped form, and would land on disk raw.
	redacted := strings.Split(string(secret.Redact(ctx, []byte(strings.Join(masked, "\n")))), "\n")
	data, err := json.Marshal(redacted)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, keyInputsName), data, 0o644)
}

// KeyInputsByRef returns the stored pre-hash key inputs behind ref (step ref, unique
// prefix, or any attempt id within the step). fs.ErrNotExist when the step resolves
// but predates key input persistence.
func (s *OutputStore) KeyInputsByRef(ref string) ([]string, error) {
	path, err := s.resolveRef(ref)
	if err != nil {
		return nil, err
	}
	return readKeyInputs(filepath.Dir(path))
}

// KeyInputsForKey returns the stored pre-hash key inputs for an EXACT cache key, the
// shape a manifest records. It does not scan the store the way KeyInputsByRef must: a
// prefix search would be a slower route to the same directory, and it fails outright once
// the key's attempt blobs have been pruned away while this sidecar remains.
func (s *OutputStore) KeyInputsForKey(cacheKey string) ([]string, error) {
	return readKeyInputs(filepath.Join(s.outputsDir(), cacheKey))
}

func readKeyInputs(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, keyInputsName))
	if err != nil {
		return nil, err
	}
	var lines []string
	if err := json.Unmarshal(data, &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

// ClassDigest summarizes one component class of a cache key: every key input shares a
// label prefix ("src", "env", "tool", ...), and the class digest hashes the class's
// lines in key order. Two machines comparing digests learn WHICH CLASS disagrees
// without shipping the full lines - small enough for a URL fragment - while the full
// lines (CLI side) name the exact file or variable.
type ClassDigest struct {
	Class  string `json:"class"`
	Digest string `json:"digest"` // sha256 over the class's lines, truncated to 12 hex
	Count  int    `json:"count"`  // how many key inputs the class contributes
}

// classDigestHexLen matches the ref truncation: enough to compare, short enough to
// ride a URL fragment many times over.
const classDigestHexLen = 12

// ClassDigests folds key inputs into one digest per component class, preserving first-
// appearance order (the hash order of the key itself, so output is stable).
func ClassDigests(inputs []string) []ClassDigest {
	type acc struct {
		h     []byte
		count int
	}
	var order []string
	byClass := map[string]*acc{}
	for _, line := range inputs {
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

// splitKeyInput separates a key-input line into the input's stable IDENTITY and the
// VALUE keyed under it. A source file whose content moved has to read as ONE input
// that changed, not as a line removed and an unrelated line added, and only the
// per-class layouts written by hashStepInputs say where that boundary falls. A class
// with no value slot (dep, charm, arg, ...) is its own identity, so it can only be
// present or absent.
func splitKeyInput(line string) (identity, value string) {
	class, rest, ok := strings.Cut(line, ":")
	if !ok {
		return line, ""
	}
	switch class {
	case "src": // src:<rel>:<contentHash>:<execBit>; the rel path may itself contain ':'
		if i := strings.LastIndexByte(rest, ':'); i > 0 {
			if j := strings.LastIndexByte(rest[:i], ':'); j > 0 {
				return "src:" + rest[:j], rest[j+1:]
			}
		}
	case "env": // env:NAME=<masked> or env:NAME:unset - both key under the variable
		if name, v, ok := strings.Cut(rest, "="); ok {
			return "env:" + name, v
		}
		if name, ok := strings.CutSuffix(rest, ":unset"); ok {
			return "env:" + name, "unset"
		}
	case "tool": // tool:<name>:version=<v>
		if name, v, ok := strings.Cut(rest, "="); ok {
			return "tool:" + name, v
		}
	}
	return line, ""
}

// KeyInputChange is one key input that does not agree between a recorded run's key and
// the live one.
type KeyInputChange struct {
	Class string `json:"class"` // the component class: "src", "env", "tool", ...
	Input string `json:"input"` // the input's identity: the file, variable, or tool
	// Recorded and Live are the values the two keys hashed under Input. The *Absent
	// flags separate "that key had no such input" from "it hashed an empty value":
	// classes like dep and charm carry no value slot, so appearing and disappearing is
	// the only difference they can express.
	Recorded       string `json:"recorded,omitempty"`
	RecordedAbsent bool   `json:"recorded_absent,omitempty"`
	Live           string `json:"live,omitempty"`
	LiveAbsent     bool   `json:"live_absent,omitempty"`
}

// KeyInputComparison is the negative lens on a cache key: how many inputs moved since a
// recorded run, and which one moved first. First is nil exactly when Differences is 0.
type KeyInputComparison struct {
	Differences int             `json:"differences"`
	First       *KeyInputChange `json:"first,omitempty"`
}

// CompareKeyInputs reports which inputs disagree between a recorded run's key inputs and
// the live ones, in LIVE key order (inputs only the recorded key had trail behind, in
// recorded order). Live order is the order hashStepInputs writes, so "first" means the
// earliest component class - the target's own definition before its sources, sources
// before env, env before tools - which is the order a reader wants to be told about: a
// changed target definition explains a moved source hash, never the reverse.
//
// Both sides must already be masked (MaskKeyInputs); the store persists masked lines and
// an unmasked live line would read as a difference on every env var. Identity, not the
// whole line, is what pairs the two sides, so a source file whose hash moved counts once.
// Multiplicity collapses: a line repeated within one side is compared once.
func CompareKeyInputs(recorded, live []string) KeyInputComparison {
	recordedVals := make(map[string]string, len(recorded))
	for _, l := range recorded {
		id, v := splitKeyInput(l)
		recordedVals[id] = v
	}
	liveVals := make(map[string]string, len(live))
	for _, l := range live {
		id, v := splitKeyInput(l)
		liveVals[id] = v
	}
	var changes []KeyInputChange
	seen := make(map[string]struct{}, len(live))
	add := func(c KeyInputChange) {
		if _, dup := seen[c.Input]; dup {
			return
		}
		seen[c.Input] = struct{}{}
		c.Class, _, _ = strings.Cut(c.Input, ":")
		changes = append(changes, c)
	}
	for _, l := range live {
		id, v := splitKeyInput(l)
		rv, ok := recordedVals[id]
		if ok && rv == v {
			continue
		}
		add(KeyInputChange{Input: id, Recorded: rv, RecordedAbsent: !ok, Live: v})
	}
	for _, l := range recorded {
		id, v := splitKeyInput(l)
		if _, ok := liveVals[id]; !ok {
			add(KeyInputChange{Input: id, Recorded: v, LiveAbsent: true})
		}
	}
	out := KeyInputComparison{Differences: len(changes)}
	if len(changes) > 0 {
		out.First = &changes[0]
	}
	return out
}

// KeyInputDiff is one component class's stored-vs-live disagreement: lines only the
// stored key has and lines only the live key has. A class absent from the slice
// matched exactly.
type KeyInputDiff struct {
	Class      string   `json:"class"`
	StoredOnly []string `json:"stored_only,omitempty"`
	LiveOnly   []string `json:"live_only,omitempty"`
}

// DiffKeyInputs compares two key-input sets and returns the disagreeing classes in
// stored-key order (then any live-only classes in live order). Line identity is the
// whole line: a source file whose hash changed appears once under StoredOnly (the old
// hash) and once under LiveOnly (the new), which is exactly the shape a reader needs
// to see what drifted.
func DiffKeyInputs(stored, live []string) []KeyInputDiff {
	storedSet := make(map[string]struct{}, len(stored))
	for _, l := range stored {
		storedSet[l] = struct{}{}
	}
	liveSet := make(map[string]struct{}, len(live))
	for _, l := range live {
		liveSet[l] = struct{}{}
	}
	var order []string
	byClass := map[string]*KeyInputDiff{}
	classOf := func(line string) *KeyInputDiff {
		class, _, _ := strings.Cut(line, ":")
		d, ok := byClass[class]
		if !ok {
			d = &KeyInputDiff{Class: class}
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
	out := make([]KeyInputDiff, 0, len(order))
	for _, class := range order {
		out = append(out, *byClass[class])
	}
	return out
}
