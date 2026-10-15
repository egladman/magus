package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// The compat-marker rule notices a leased worker adding a marker that labels code kept for
// older data or callers. Whether such code stays is a judgment about who still consumes the
// old behavior, which a worker cannot see from its write paths, so the rule advises and
// files one attention row per lease. It never refuses: the write is not wrong, the decision
// is just not the worker's.
const denyRuleCompatMarker denyRuleName = "compat-marker"

// defaultCompatMarkers is the rule's own list, for a setting that was never resolved.
var defaultCompatMarkers = sync.OnceValue(func() []string {
	all, err := builtin.Resolve(nil)
	if err != nil {
		return nil
	}
	return all[string(denyRuleCompatMarker)].Markers
})

// gradeCompatMarker advises a leased write its boundary allowed that adds a marker, and
// files the lease's attention row the first time one does. A dry run is advised and files
// nothing. Silent for every uncertainty: a file it cannot read, a rule it cannot resolve.
func gradeCompatMarker(ctx context.Context, deps Dependencies, at location, who hookAttribution, lease, writePath string, fields writeFields) writeGrade {
	if lease == "" {
		return writeGrade{}
	}
	s, ok := deps.setting(string(denyRuleCompatMarker))
	if !ok || s.Decision == builtin.Off {
		return writeGrade{}
	}
	markers := s.Markers
	if len(markers) == 0 {
		markers = defaultCompatMarkers()
	}
	if fields.Content != "" {
		// A whole-file write replaces what is on disk, so that is the text it adds to.
		old, err := os.ReadFile(writePath)
		switch {
		case err == nil:
			fields.OldText = string(old)
		case !errors.Is(err, fs.ErrNotExist):
			return writeGrade{}
		}
	}
	added := addedMarkers(fields, markers)
	if len(added) == 0 {
		return writeGrade{}
	}
	shown := writePath
	if rel, inside := workspaceRelative(leaseCheckout(ctx, at, lease), writePath); inside {
		shown = rel
	}
	if !isDryRun(ctx) && !stallGate(at.cacheDir, lease).MarkFired(hint.MarkerKind(denyRuleCompatMarker)) {
		fileCompatRow(ctx, at, who, lease, shown, added)
	}
	say := fmt.Sprintf("magus workspace: this write adds %s to %s: leave it out and name it in unresolved_risks, since the root decides whether code stays for an old caller.",
		quotedList(added), shown)
	return writeGrade{Decision: decisionAdvise, Kind: hint.MarkerKind(denyRuleCompatMarker),
		Key:     hint.MarkerKind(string(denyRuleCompatMarker) + "-" + lineDigest(say)),
		Context: say,
		Why: fmt.Sprintf("A marker labels code kept for older data, artifacts or callers, and keeping it is a judgment about who still consumes the old behavior. "+
			"Lease %s cannot see those consumers from its write paths; the root can, and an attention row tells it this lease added one. The write itself is never refused.", lease)}
}

// addedMarkers are the markers a write leaves more of than it found: counted in the text
// it writes (Content, NewText and each edit's) against the text it replaces (OldText and
// each edit's).
func addedMarkers(fields writeFields, markers []string) []string {
	added := append(make([]string, 0, 2+len(fields.Edits)), fields.Content, fields.NewText)
	removed := append(make([]string, 0, 1+len(fields.Edits)), fields.OldText)
	for _, e := range fields.Edits {
		added, removed = append(added, e.NewText), append(removed, e.OldText)
	}
	// Joined on NUL so a marker cannot straddle two pieces of text.
	newText, oldText := strings.Join(added, "\x00"), strings.Join(removed, "\x00")
	var out []string
	for _, m := range markers {
		if m != "" && strings.Count(newText, m) > strings.Count(oldText, m) {
			out = append(out, m)
		}
	}
	return out
}

// fileCompatRow opens the attention row for a lease that added a marker. It states the
// facts a root decides on and names no remedy.
func fileCompatRow(ctx context.Context, at location, who hookAttribution, lease, writePath string, markers []string) {
	dir, err := sessions.Dir(at.workspace)
	if err != nil {
		return
	}
	open := sessions.AttentionOpen{
		Outcome:  string(types.OutcomeDiagnostic),
		Severity: string(types.SeverityNotice),
		Source:   sessions.SourceLabel("guard", string(denyRuleCompatMarker)),
		Lease:    lease,
		Files:    []types.FileRef{{Value: writePath}},
		Message: fmt.Sprintf("job %s added %s to %s. It ran on %s.",
			lease, quotedList(markers), writePath, workerModel(ctx, at, who, lease)),
	}
	origin := trail.StampOrigin(ctx, types.Origin{EntryPoint: types.EntryPointHook, Host: who.Host, Session: who.Session, Agent: who.Agent})
	_, _, _ = sessions.OpenRequest(dir, cmp.Or(who.Session, lease), open, sessions.InvocationStart{Origin: origin, Workspace: at.workspace, Lease: lease})
}

func quotedList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = "`" + s + "`"
	}
	return strings.Join(quoted, " and ")
}
