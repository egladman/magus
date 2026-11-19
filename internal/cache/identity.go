package cache

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// OutputIdentityRecord is the -o json/yaml projection of `query output <ref> --identity`: the
// stored descriptor, the producing invocation's lineage when its run log survives, and
// the cache key's component-class digests when the run persisted its key inputs. I is
// the caller's invocation record, which this package cannot name.
type OutputIdentityRecord[I any] struct {
	types.OutputDescriptor
	Invocation   *I            `json:"invocation,omitempty"`
	ClassDigests []ClassDigest `json:"class_digests,omitempty"`
}

// PrintOutputIdentityText writes desc's identity to w: invArgs is the producing
// invocation's command line, nil when its run log did not survive, and currentRev reads
// the revision the workspace is on now, called only when desc recorded one.
func PrintOutputIdentityText(w io.Writer, desc types.OutputDescriptor, invArgs []string, digests []ClassDigest, currentRev func() string) {
	fmt.Fprintf(w, "ref:     %s\n", desc.Ref)
	fmt.Fprintf(w, "project: %s\n", desc.Project)
	if desc.Target != "" {
		fmt.Fprintf(w, "target:  %s\n", desc.Target)
	}
	status := "pass"
	if desc.Failed {
		status = "fail"
	}
	fmt.Fprintf(w, "status:  %s (%s) at %s\n", status,
		(time.Duration(desc.DurationMs) * time.Millisecond).Round(time.Millisecond),
		time.UnixMilli(desc.TimestampMs).Format("2006-01-02 15:04:05"))
	if desc.ErrMsg != "" {
		fmt.Fprintf(w, "error:   %s\n", desc.ErrMsg)
	}
	if desc.FailedNeedRef != "" {
		fmt.Fprintf(w, "failed:  ctx.needs %s, read it with %s\n", desc.FailedNeedTarget, hint.QueryOutput.With(desc.FailedNeedRef))
	}
	if desc.Attempt != "" {
		fmt.Fprintf(w, "attempt: %s\n", desc.Attempt)
	}
	if desc.Inv != "" {
		fmt.Fprintf(w, "inv:     %s", desc.Inv)
		if len(invArgs) > 0 {
			fmt.Fprintf(w, "  (magus %s)", strings.Join(invArgs, " "))
		}
		fmt.Fprintln(w)
	}
	if desc.Key != "" {
		fmt.Fprintf(w, "key:     %s (keyVersion %s)\n", desc.Key, desc.KeyVersion)
	}
	if desc.MagusVersion != "" {
		fmt.Fprintf(w, "magus:   %s\n", desc.MagusVersion)
	}
	if desc.Revision != "" {
		fmt.Fprintf(w, "rev:     %s", shortRevision(desc.Revision))
		if desc.Dirty {
			fmt.Fprintf(w, " (dirty: uncommitted changes at capture time; the revision alone may not reproduce it)")
		}
		fmt.Fprintln(w)
		// The cache key pins a tree STATE, never a commit; this is the one place
		// that names one, by comparing the descriptor's revision against HEAD now.
		// PROVENANCE only, never instruction: on a cache HIT, recordOutput never runs,
		// so the descriptor still carries the revision of whichever run FIRST minted
		// this key, which can differ from HEAD even though the current tree
		// reproduces the output perfectly (that reproduction is exactly why it was a
		// hit). Telling the user to check out the recorded commit would therefore be
		// wrong on the most common path that reaches this line: a ref that resolved
		// already has its bytes, and the KEY, not the commit, is what decided that.
		// Only print when the two actually differ: same revision is the common case
		// and needs no callout.
		if cur := currentRev(); cur != "" && cur != desc.Revision {
			fmt.Fprintf(w, "recorded at %s, you are on %s.\n", shortRevision(desc.Revision), shortRevision(cur))
		}
	}
	if len(digests) == 0 {
		fmt.Fprintln(w, "\nkey components: unavailable (run predates key-input persistence; re-run the target to record them)")
		return
	}
	fmt.Fprintln(w, "\nkey components:")
	for _, d := range digests {
		noun := "lines"
		if d.Count == 1 {
			noun = "line"
		}
		fmt.Fprintf(w, "  %-16s %s  %d %s\n", d.Class, d.Digest, d.Count, noun)
	}
}

func shortRevision(id string) string { return id[:min(len(id), shortRevisionLen)] }
