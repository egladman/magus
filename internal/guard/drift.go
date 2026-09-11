package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// The SCOPE-DRIFT rule: a session that has been editing one part of the workspace is about
// to write somewhere the rest of its work does not reach.
//
// The fact it reports is a graph fact rather than a hunch: the project this write lands in
// shares no declared dependency edge with any project the session has written to, so
// nothing a target run for one half validates the other. It ADVISES and never denies,
// because nobody declared this boundary, and a lease already covering the path is exactly
// the case it skips.

// advisoryTouchedProjects names the file holding the projects a session has written
// to. It is a marker KIND rather than a notice so that hint.MarkerPath keys it on the
// same hashed session id as every advisory marker, and so the retention sweep that
// runs beside those markers reaps it too.
const advisoryTouchedProjects hint.MarkerKind = "touched-projects"

// scopeDrift is what the drift rule resolved about one write: the project it lands in,
// and the advisory owed for it.
//
// The two are separate because they are owed at different times. The advisory is one
// rung among several and speaks only when nothing louder did; the project is recorded
// whatever the verdict said, since the session touched it either way. Recording only
// when the rung spoke would leave projects out of the set and fire this advisory on a
// project the session had already been editing, which is the false positive that
// teaches a reader to skip the whole family.
type scopeDrift struct {
	markers hint.Gate
	project string
	advice  string
}

// gradeScopeDrift judges one write against the projects this session has already
// written to.
//
// Silent on every uncertainty, the contract every guard rule here keeps: no workspace,
// a workspace that is not the one the host reported, a path no project owns, or a
// session with nothing recorded yet. A first write has no scope to have drifted from.
func gradeScopeDrift(ctx context.Context, deps Dependencies, markers hint.Gate, actingLease, writePath string) scopeDrift {
	writePath = strings.TrimSpace(writePath)
	if writePath == "" || markers.CacheDir() == "" {
		return scopeDrift{}
	}
	location := hookLocation(ctx, deps)
	if location.workspace == "" {
		return scopeDrift{}
	}
	// The memoized load, which adviseGeneratedWrite already pays on this same call, so
	// the graph costs this rule nothing on the write path. It resolves from the process
	// cwd, so a hook running outside the checkout the host reported would classify the
	// path against the wrong tree: that case goes silent rather than guessing.
	ws, err := deps.inspect(ctx, "")
	if err != nil || ws == nil || ws.Root() != location.workspace {
		return scopeDrift{}
	}
	drift := driftVerdict(ws, writePath, touchedProjects(markers))
	drift.markers = markers
	if drift.advice != "" && leaseCoversWrite(ctx, actingLease, location, writePath) {
		// A worker writing inside the paths its orchestrator leased it is in scope by
		// declaration, whatever the graph says about the projects those paths span.
		drift.advice = ""
	}
	return drift
}

// driftVerdict names the project writePath lands in, and the advisory owed when that
// project shares no dependency edge with any of touched.
//
// Split from the resolution above so the policy can be graded against a fixture
// workspace, the way focusVerdict is.
//
// Relatedness runs in BOTH directions, and one closure each way answers it: a project
// the touched set reaches through depends_on is downstream work the session is already
// paying for, and a project that reaches back into the touched set is upstream of it.
// Only a project on neither side is a second unit.
func driftVerdict(ws types.WorkspaceReader, writePath string, touched []string) scopeDrift {
	write, ok := project.FocusForPaths(ws, []string{writePath})
	if !ok {
		return scopeDrift{}
	}
	landed := scopeDrift{project: write.Seeds[0]}
	// The cheap check first: a project already in the set needs no graph walk, which is
	// what keeps the common case (the same project again) off the closure path.
	if len(touched) == 0 || slices.Contains(touched, landed.project) {
		return landed
	}
	from, ok := project.FocusForPaths(ws, touchedPaths(ws, touched))
	if !ok {
		return landed
	}
	if slices.Contains(from.Projects, landed.project) {
		return landed
	}
	for _, seed := range from.Seeds {
		if slices.Contains(write.Projects, seed) {
			return landed
		}
	}
	landed.advice = scopeDriftAdvice(landed.project, from.Seeds)
	return landed
}

// touchedPaths spells the touched projects as paths FocusForPaths can resolve.
//
// The root project is the reason this exists. That function reads its arguments as
// lease DECLARATIONS, and types.LiteralPrefix answers "" for a bare ".", so a set
// holding only the root resolves to no seed at all and the whole rule goes quiet. In a
// workspace whose root project owns most of the tree, that is the rule being quiet
// nearly always, which is how it would have shipped inert.
func touchedPaths(ws types.WorkspaceReader, touched []string) []string {
	out := make([]string, 0, len(touched))
	for _, p := range touched {
		if p == "." {
			p = ws.Root()
		}
		out = append(out, p)
	}
	return out
}

// scopeDriftAdvice names magus-multi-agent rather than any skill a particular
// harness installs. This ships in the binary and judges whatever workspace it is
// pointed at, so the one skill it may route to is one magus itself installs, and
// that skill's own opening line is to count write sets.
func scopeDriftAdvice(proj string, touched []string) string {
	return fmt.Sprintf(
		"magus workspace: consider handing %s to its own subagent or session before you write here. The magus-multi-agent skill partitions work by write set, and this write opens a second one.\n"+
			"%s depends on none of the projects this session has written to (%s), and none of them depends on it, so nothing in the graph ties the two halves of this diff together: no target run for one half validates the other.\n"+
			"`%s` says whether they connect at all and how far apart. If they do, this is one unit after all.",
		proj, proj, strings.Join(touched, ", "), hint.Path.With(proj, touched[0]))
}

// record adds the write's project to the session's touched set.
//
// Append-only and best-effort: a set magus cannot write means the rule speaks again
// later, which is the smaller failure. Duplicate lines are harmless, so two hooks
// racing here cost a byte rather than a wrong answer.
func (d scopeDrift) record() {
	if d.project == "" || d.markers.CacheDir() == "" || slices.Contains(touchedProjects(d.markers), d.project) {
		return
	}
	path := hint.MarkerPath(d.markers.CacheDir(), d.markers.Session(), advisoryTouchedProjects)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(d.project + "\n")
}

// touchedProjects reads the projects this session has written to, oldest first.
//
// Line-separated rather than whitespace-separated: a project path is a path, and a
// directory holding a space would otherwise read back as two projects that own nothing.
func touchedProjects(g hint.Gate) []string {
	if g.CacheDir() == "" {
		return nil
	}
	body, err := os.ReadFile(hint.MarkerPath(g.CacheDir(), g.Session(), advisoryTouchedProjects))
	if err != nil {
		return nil
	}
	lines := strings.Split(string(body), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" && !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	return out
}

// leaseCoversWrite reports whether the acting lease's declared write paths cover this
// write. A lease that declares none covers nothing: an orchestrator that named no lane
// drew no boundary this rule could defer to.
func leaseCoversWrite(ctx context.Context, actingLease string, location location, writePath string) bool {
	if actingLease == "" || !types.ValidJobID(actingLease) || location.cacheDir == "" {
		return false
	}
	rel, inside := workspaceRelative(location.workspace, writePath)
	if !inside {
		return false
	}
	leases, err := leaseRows(ctx, location)
	if err != nil {
		return false
	}
	me, enrolled := liveLease(liveLeases(leases), actingLease)
	if !enrolled {
		return false
	}
	_, mine, err := declarationCovering(me.WritePaths, rel)
	return err == nil && mine
}
