package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
)

// TargetCacheReport is the -o json/yaml projection of `describe target --cache`: one
// entry per resolved (project, target) pair with its live key, predicted ref, and
// component-class digests; Against carries the stored-vs-live diff when --against
// named a ref.
type TargetCacheReport struct {
	Project      string        `json:"project"`
	Target       string        `json:"target"`
	Charms       []string      `json:"charms,omitempty"`
	Key          string        `json:"key"`
	KeyVersion   string        `json:"key_version"`
	Ref          string        `json:"ref"`
	ClassDigests []ClassDigest `json:"class_digests"`
	// KeyInputs is the masked line-by-line key, present only with --inputs. The class
	// digests say a class changed; these say WHICH line, and, read as a list, which
	// files a declaration actually resolved to. A `src:` line missing for a file the
	// target declares is the whole bug class this exists to make visible.
	KeyInputs []string            `json:"key_inputs,omitempty"`
	Against   *TargetCacheAgainst `json:"against,omitempty"`
	LastRun   *TargetCacheLastRun `json:"last_run,omitempty"`
}

type TargetCacheAgainst struct {
	Ref     string         `json:"ref"`
	Key     string         `json:"key"` // the stored run's full cache key
	Matches bool           `json:"matches"`
	Diff    []KeyInputDiff `json:"diff,omitempty"`
}

// TargetCacheLastRun is the negative half of the cache lens: the newest entry recorded
// for this target HERE, whether a run now would replay a stored entry, and the first key
// input that moved when it would not. --against answers the same question about a peer
// the caller names; this answers it about the run the caller already made, which is the
// one they have when they ask why the cache missed.
type TargetCacheLastRun struct {
	Recorded bool      `json:"recorded"`
	Ref      string    `json:"ref,omitempty"`
	Key      string    `json:"key,omitempty"`
	At       time.Time `json:"at,omitempty"`
	// KeyMatches is whether the NEWEST entry's key equals the live key. WouldReplay is the
	// question the reader actually asked (would a run now hit at all), and the two come
	// apart: an edit and a revert leave the newest entry keyed to the edited tree while an
	// older entry still sits at the live key and replays. ReplaysRef names the entry a run
	// would reach, and is set whenever WouldReplay is.
	KeyMatches  bool            `json:"key_matches"`
	WouldReplay bool            `json:"would_replay"`
	ReplaysRef  string          `json:"replays_ref,omitempty"`
	Differences int             `json:"differences,omitempty"`
	First       *KeyInputChange `json:"first_difference,omitempty"`
	// Explanation names the mechanism behind the verdict and the next step, so the JSON
	// consumer is not left to reassemble the sentence the text renderer prints.
	Explanation string `json:"explanation"`
}

// NewTargetCacheLastRun turns the last recorded entry for project:target into the miss
// explanation for liveKey. lookupErr wrapping fs.ErrNotExist is the "nothing recorded
// yet" case, which is an answer rather than a failure. liveLines must already be digested
// and redacted, like the lines the store persists.
//
// liveKeyReplays is [RecordedRun.WouldReplay] for liveKey, and it gates the whole
// miss explanation: a true there means a run HITS, whatever the newest entry's key says.
func NewTargetCacheLastRun(project, target string, rec RecordedRun, lookupErr error, liveKey string, liveKeyReplays bool, liveLines []string) TargetCacheLastRun {
	switch {
	case errors.Is(lookupErr, fs.ErrNotExist):
		return TargetCacheLastRun{Explanation: fmt.Sprintf(
			"nothing recorded for this target here, so there is nothing to compare against: run `%s` once and this section will name what moved.",
			hint.Run.With(target, project))}
	case lookupErr != nil:
		return TargetCacheLastRun{Explanation: fmt.Sprintf(
			"the last recorded entry could not be read, so there is nothing to compare against: %v", lookupErr)}
	}
	lr := TargetCacheLastRun{
		Recorded:   true,
		Ref:        PortableRef(rec.Key),
		Key:        rec.Key,
		At:         rec.CreatedAt,
		KeyMatches: rec.Key == liveKey,
	}
	lr.WouldReplay = liveKeyReplays || lr.KeyMatches
	if lr.WouldReplay {
		lr.ReplaysRef = PortableRef(liveKey)
	}
	if lr.KeyMatches {
		lr.Explanation = "the live key hashes to the same value, so a run now replays that entry instead of executing."
		return lr
	}
	if lr.WouldReplay {
		lr.Explanation = fmt.Sprintf(
			"a run now HITS: an OLDER entry here is already stored under the live key, so a run replays %s and never reaches %s. An edit and a revert leave the cache exactly this way. Next: `%s` reads the entry that would replay.",
			lr.ReplaysRef, lr.Ref, hint.QueryOutput.With(lr.ReplaysRef))
		return lr
	}
	if rec.KeyInputs == nil {
		lr.Explanation = fmt.Sprintf(
			"a run now MISSES, but that entry predates key-input persistence, so no input can be named: run `%s` once to record them, then ask again.",
			hint.Run.With(target, project))
		return lr
	}
	changes := FirstKeyInputChange(rec.KeyInputs, liveLines)
	lr.Differences = len(changes)
	if len(changes) > 0 {
		lr.First = &changes[0]
	}
	// A differing key with no differing input means the two sides disagree on
	// multiplicity or order, which the pairing collapses; say so rather than
	// claiming nothing changed while the ref plainly moved.
	if lr.Differences == 0 {
		lr.Explanation = fmt.Sprintf(
			"a run now MISSES, but no single input differs: the keys disagree on the ORDER or repetition of inputs, which this comparison collapses. Next: `%s` to read the key line by line.",
			hint.DescribeTarget.With(target, project, "--cache", "--inputs"))
		return lr
	}
	noun := "inputs"
	if lr.Differences == 1 {
		noun = "input"
	}
	lr.Explanation = fmt.Sprintf(
		"a run now MISSES: the cache key is a hash of these inputs and %d %s moved since. Next: `%s` to see every line.",
		lr.Differences, noun, hint.DescribeTarget.With(target, project, "--cache", "--inputs"))
	return lr
}

// lastRunLines renders the miss explanation as the text section, indented to match the
// key/components blocks above it.
func lastRunLines(lr TargetCacheLastRun) []string {
	if !lr.Recorded {
		return []string{"  last recorded run: none", "    " + lr.Explanation}
	}
	verdict := "DIFFERS"
	switch {
	case lr.KeyMatches:
		verdict = "MATCHES"
	case lr.WouldReplay:
		verdict = "DIFFERS; the live key HITS an older entry"
	}
	out := []string{fmt.Sprintf("  last recorded run: %s  %s  %s", lr.At.UTC().Format(time.RFC3339), lr.Ref, verdict)}
	if lr.First != nil {
		side := func(value string, absent bool) string {
			if absent {
				return "(absent)"
			}
			if value == "" {
				return "(present)"
			}
			return value
		}
		out = append(out,
			"    first differing input: "+lr.First.Input,
			"      - recorded  "+side(lr.First.Recorded, lr.First.RecordedAbsent),
			"      + live      "+side(lr.First.Live, lr.First.LiveAbsent))
	}
	return append(out, "    "+lr.Explanation)
}

// HashOfKeyInputs digests a key-input set for equality comparison. It is the THIRD
// spelling of "do these two keys agree" in this command (after key equality and the
// identity pairing behind [FirstKeyInputChange]), and the only order-sensitive one:
// it hashes the lines in hash order, so a reordering reads as a difference where the
// pairing collapses it. That is the correct behavior HERE, since it stands in for the key
// itself, and the wrong behavior for naming what a reader should go fix.
//
// compat(until: no store still serves a descriptor written without a key field): a
// descriptor predating OutputDescriptor.Key carries no key for --against to compare, and
// falling back to the lines it does have is what keeps such a ref answerable at all rather
// than reporting every comparison as a mismatch. Observe it is safe to drop by checking
// that every .json descriptor under the cache's outputs/ tree (including any store a ref
// was imported from) carries a non-empty "key".
func HashOfKeyInputs(lines []string) string {
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// WriteTargetCacheReports renders the text form of `describe target --cache`: one block
// per resolved target, the key and its component classes first, then the last recorded
// run and the --against diff when either was asked for.
func WriteTargetCacheReports(w io.Writer, reports []TargetCacheReport) {
	for _, r := range reports {
		fmt.Fprintf(w, "project: %s  target: %s\n", r.Project, r.Target)
		if len(r.Charms) > 0 {
			fmt.Fprintf(w, "  charms: %s\n", strings.Join(r.Charms, ","))
		}
		fmt.Fprintf(w, "  key:    %s (keyVersion %s)\n", r.Key, r.KeyVersion)
		fmt.Fprintf(w, "  ref:    %s (what a run of these exact inputs prints)\n", r.Ref)
		fmt.Fprintln(w, "  components:")
		for _, d := range r.ClassDigests {
			noun := "lines"
			if d.Count == 1 {
				noun = "line"
			}
			fmt.Fprintf(w, "    %-16s %s  %d %s\n", d.Class, d.Digest, d.Count, noun)
		}
		if len(r.KeyInputs) > 0 {
			fmt.Fprintln(w, "  key inputs:")
			for _, line := range r.KeyInputs {
				fmt.Fprintf(w, "    %s\n", line)
			}
		}
		if r.LastRun != nil {
			for _, line := range lastRunLines(*r.LastRun) {
				fmt.Fprintln(w, line)
			}
		}
		if r.Against == nil {
			fmt.Fprintln(w)
			continue
		}
		if r.Against.Matches {
			fmt.Fprintf(w, "  against %s: keys MATCH: a run here mints the same ref\n\n", r.Against.Ref)
			continue
		}
		fmt.Fprintf(w, "  against %s: keys DIFFER\n", r.Against.Ref)
		if len(r.Against.Diff) == 0 {
			fmt.Fprintln(w, "    (no line-level difference to show: the stored run recorded no key inputs)")
		}
		for _, d := range r.Against.Diff {
			fmt.Fprintf(w, "    %s:\n", d.Class)
			for _, l := range d.StoredOnly {
				fmt.Fprintf(w, "      - stored  %s\n", l)
			}
			for _, l := range d.LiveOnly {
				fmt.Fprintf(w, "      + live    %s\n", l)
			}
		}
		fmt.Fprintln(w)
	}
}
