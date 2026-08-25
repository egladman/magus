package doctor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// unexplainedWindow is how much history the hotspot scan reads, and unexplainedTop is how
// many of the hottest files the check asks about.
//
// Both are small on purpose. The question is whether the code this workspace works on
// hardest is explained anywhere, and a top-40 list answers a different, unanswerable one:
// every file is unexplained in a repo that keeps no notes, and a finding naming forty of
// them is one nobody reads twice.
const (
	unexplainedWindow = 200
	unexplainedTop    = 10
)

// Explanations is what the workspace's notes store covers, supplied by the caller because
// the store is resolved by the CLI rather than by the workspace.
//
// The zero value is a real state and not a fault: notes are opt-in, and a workspace keeping
// none has not failed at anything.
type Explanations struct {
	// Notes is how many notes the store holds at all.
	Notes int
	// Files are the paths some note anchors, directly or through a symbol declared there.
	Files []string
}

// checkUnexplainedHotspots reports whether the code this workspace edits hardest is
// explained anywhere a later reader would find it.
//
// Every input already existed and nothing joined them: the hotspot lens knows which files
// absorb the most work, the notes store knows which code somebody wrote a reason down for,
// and the gap between those two sets is where understanding has been deferred - the code
// most likely to be changed next by whoever knows least about it.
//
// It reports a RATIO rather than a list of shame. Naming ten files as unexplained in a
// workspace that keeps no notes would be red on the first run and every run after, and a
// check red by default is one people learn to skip - taking the real findings with it. The
// advice fires only where it can mean something: a workspace that HAS notes, none of which
// reach its hottest code.
func (r *runner) checkUnexplainedHotspots(_ []*types.Project) types.DoctorCheck {
	const name = "unexplained-hotspots"

	exp := r.explanations()
	// No notes at all is a choice, not a gap. Grading a workspace 0-of-10 against a
	// feature it declined is the kind of finding that teaches people to stop reading.
	if exp.Notes == 0 {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorOK,
			Evidence: types.EvidenceUnknown,
			Message:  "no notes in this workspace, so what its hottest files mean is unrecorded rather than unexplained",
			Details:  []string{"start one where a reason is worth keeping: magus notes new <name>"},
		}
	}

	analyzer, ok := r.ws.(types.InsightAnalyzer)
	if !ok {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorOK,
			Evidence: types.EvidenceUnknown,
			Message:  "this workspace exposes no history lens; skipped",
		}
	}
	hot, err := analyzer.Hotspots(r.runCtx(), types.InsightOptions{Commits: unexplainedWindow, Files: true})
	if err != nil || len(hot.Files) == 0 {
		// No history is the common shape of this - a fresh clone, a non-git tree - and it
		// is emphatically not "the hot code is all explained".
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorOK,
			Evidence: types.EvidenceUnknown,
			Message:  fmt.Sprintf("no file history in the last %d commits; nothing to rank", unexplainedWindow),
		}
	}

	top := hot.Files
	if len(top) > unexplainedTop {
		top = top[:unexplainedTop]
	}
	anchored := workspaceRelSet(r.root, exp.Files)

	var covered int
	var bare []string
	for _, f := range top {
		if anchored[filepath.ToSlash(f.Path)] {
			covered++
			continue
		}
		bare = append(bare, fmt.Sprintf("%s: %d commits, %d author(s)", f.Path, f.Commits, f.Authors))
	}

	msg := fmt.Sprintf("%d of the %d most-edited file(s) carry a note explaining them", covered, len(top))
	if covered > 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Evidence: types.EvidenceInferred, Message: msg}
	}
	return types.DoctorCheck{
		Name:     name,
		Status:   types.DoctorAdvice,
		Evidence: types.EvidenceInferred,
		Message:  msg + "; the code this workspace works on hardest is what it has written down least about",
		Details: append(bare,
			fmt.Sprintf("%d note(s) exist and anchor other code; anchor one here: magus notes new <name>", exp.Notes)),
	}
}

// explanations is the notes coverage the caller supplied, or the zero value.
func (r *runner) explanations() Explanations {
	if r.opts.explanations == nil {
		return Explanations{}
	}
	return *r.opts.explanations
}

// workspaceRelSet renders anchored paths workspace-relative and slash-separated, the shape
// the hotspot lens reports.
//
// An absolute path compared against a relative one matches nothing, and the check would then
// report every hot file as unexplained - a failure indistinguishable from a real finding.
func workspaceRelSet(root string, paths []string) map[string]bool {
	out := make(map[string]bool, len(paths))
	for _, p := range paths {
		if root != "" && filepath.IsAbs(p) {
			if rel, err := filepath.Rel(root, p); err == nil {
				p = rel
			}
		}
		out[strings.TrimPrefix(filepath.ToSlash(p), "./")] = true
	}
	return out
}
