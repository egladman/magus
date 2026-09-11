package guard

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// The FOCUS rule: the blinders. A session works on one project; this is the rule
// that notices when a read left it.
//
// Focus is a READ boundary, where the lease ledger's write_paths is a WRITE one,
// and the two catch different failures. A write outside your lane collides with
// another agent, which the diff eventually reveals. A read outside it never
// collides with anything and leaves no trace: it spends tokens on a tree nobody
// asked about, and it carries a sibling's practices and code quality into work
// that never chose them. Nothing downstream can tell that happened.
//
// It ADVISES by default and DENIES only under a bound lease, which is the shape
// the harness-tightness measurements argued for: a shell default-deny is the wrong
// layer (it fails open on the first spelling nobody enumerated), and a lease is the
// opt-in that makes a hard boundary honest, because somebody declared it.

// focusReader is one command that reads a file or searches a tree, and what its
// argument shape costs to read correctly.
//
// valueFlags are the short flags that consume the next word, so `head -c 200 file`
// does not read 200 as a path. pattern marks the commands whose FIRST operand is a
// pattern rather than a path; treating a regex as a path is the false positive that
// would train a reader to skip this advisory.
type focusReader struct {
	valueFlags string
	pattern    bool
}

// focusReaders is the set deliberately, not every command that can open a file. A
// rule that fires on an interpreter or a build tool would be guessing at what the
// program does with its arguments; these seventeen do exactly one thing with theirs.
var focusReaders = map[string]focusReader{
	"cat":  {},
	"bat":  {},
	"head": {valueFlags: "nc"},
	"tail": {valueFlags: "nc"},
	"less": {},
	"more": {},
	"wc":   {},
	"nl":   {},
	"od":   {},
	"xxd":  {},

	"grep":  {valueFlags: "ef", pattern: true},
	"egrep": {valueFlags: "ef", pattern: true},
	"fgrep": {valueFlags: "ef", pattern: true},
	"rg":    {valueFlags: "eg", pattern: true},
	"ag":    {pattern: true},
	"sed":   {valueFlags: "e", pattern: true},
	"awk":   {valueFlags: "vf", pattern: true},

	// find's first operand is the path it walks; fd's is the name it looks for.
	"find": {},
	"fd":   {valueFlags: fdValueFlags, pattern: true},
}

// fdValueFlags are fd's short flags that consume the next word, so a `-t d` type filter is
// not read as a name query. One constant because the focus rule and the file-find rule both
// parse fd, and two copies of a flag set drift into two answers about the same line.
const fdValueFlags = "tedExXS"

// focusGrade is what the focus rule has to say about one command. Empty Decision
// means it had nothing to say, which the wire's "pass" does not: a rule that stayed
// silent may be overridden by a later one, a rule that cleared the read may not.
type focusGrade struct {
	Decision string // "", "advise", or "deny"
	Reason   string
	Context  string
	// Brief is the one-line repeat, for the sessions that read widely on purpose.
	// Measured over 1,499 sessions: 95% of advisory bytes were same-session repeats.
	Brief string
	// Rel is the workspace-relative path that fell outside, and the key the
	// once-per-path marker is taken on.
	Rel string
}

// gradeFocusRead judges one shell command against the focus of the project the
// session stands in, or of the lease bound to this checkout.
//
// Silent on every uncertainty, the same contract every other guard rule keeps: no
// workspace, no project holding the cwd, a command that does not parse, an operand
// that resolves outside the workspace. A path magus cannot attribute has no lane it
// could be outside of, and an advisory fired on a guess is one readers learn to skip.
func gradeFocusRead(ctx context.Context, deps Dependencies, actingLease, command string) focusGrade {
	if strings.TrimSpace(command) == "" {
		return focusGrade{}
	}
	cmds, ok := ParseCommands(command)
	if !ok {
		return focusGrade{}
	}
	operands := focusReadOperands(cmds)
	if len(operands) == 0 {
		return focusGrade{}
	}
	location := hookLocation(ctx, deps)
	if location.workspace == "" {
		return focusGrade{}
	}
	// Not the memoized inspect: the hook process is one command long, so
	// nothing is saved, and the memo pins one root per process while the trail names
	// whichever checkout the command ran in.
	ws, err := deps.inspect(ctx, location.workspace)
	if err != nil || ws == nil {
		return focusGrade{}
	}

	// The lease's declared paths outrank the working directory, because a worker
	// runs wherever its checkout is and the orchestrator's declaration is the thing
	// that was actually decided. Only a lease gets a deny.
	focus, leaseID, ok := focusForLease(ctx, ws, location, actingLease)
	if !ok {
		if focus, ok = project.FocusAt(ws, focusDir(location)); !ok {
			return focusGrade{}
		}
		leaseID = ""
	}
	return focusVerdict(focus, leaseID, location.workspace, focusDir(location), operands)
}

// focusVerdict judges resolved operands against a resolved focus, and is where the
// whole rule's policy lives: advise for a session, deny for a lease.
//
// Split from the resolution above so it can be graded without a workspace on disk.
// The first operand that leaves the focus is the verdict: a line that reads three
// files needs one explanation, not three.
func focusVerdict(focus project.Focus, leaseID, root, dir string, paths []string) focusGrade {
	for _, op := range paths {
		rel, inside := workspaceRelative(root, focusResolve(dir, op))
		if !inside || focus.Contains(rel) {
			continue
		}
		owner := focus.Owner(rel)
		if leaseID != "" {
			return focusGrade{Decision: "deny", Rel: rel, Reason: fmt.Sprintf(
				"magus workspace: read inside the focus lease %s was given (%s). "+leaseActorClause("widen this lane")+"\n"+
					"%s belongs to project %s, which is outside that focus: %s, plus what each declares depends_on. The declaration is the orchestrator's, recorded in this workspace's ledger; magus is reading it back, not inventing a rule.",
				leaseID, strings.Join(focus.Seeds, ", "), rel, owner, strings.Join(focus.Projects, ", "))}
		}
		return focusGrade{
			Decision: "advise",
			Rel:      rel,
			Context: fmt.Sprintf(
				"magus workspace: stay inside %s and what it depends on, or run `%s` to see what owns this path before you read further.\n"+
					"%s belongs to project %s, which is outside this session's focus: %s, plus the workspace-root files every project resolves through. A sibling project is not an input to this work, so how it is written is not evidence about how this one should be.",
				strings.Join(focus.Seeds, ", "), hint.DescribeFile.With(rel), rel, owner, strings.Join(focus.Projects, ", ")),
			Brief: fmt.Sprintf("magus workspace: %s is outside %s's focus (it belongs to %s).",
				rel, strings.Join(focus.Seeds, ", "), owner),
		}
	}
	return focusGrade{}
}

// focusForLease resolves the focus the bound lease declares, reporting the row it
// came from so the caller knows a deny is available.
//
// A lease that declares neither read paths nor write paths (a read_only investigator)
// yields none, and the caller falls back to the working directory, which advises.
// That is the intended asymmetry: a hard read boundary needs somebody to have
// declared one.
func focusForLease(ctx context.Context, ws types.WorkspaceReader, location location, actingLease string) (project.Focus, string, bool) {
	if actingLease == "" || !types.ValidLeaseID(actingLease) || location.cacheDir == "" {
		return project.Focus{}, "", false
	}
	leases, err := leaseRows(ctx, location)
	if err != nil {
		return project.Focus{}, "", false
	}
	me, enrolled := liveLease(liveLeases(leases), actingLease)
	if !enrolled {
		return project.Focus{}, "", false
	}
	declared := me.ReadPaths
	if len(declared) == 0 {
		// The write lane doubles as the read lane when nothing widened it: a worker
		// leased to edit a project is a worker that was pointed at that project.
		declared = me.WritePaths
	}
	focus, ok := project.FocusForPaths(ws, declared)
	if !ok {
		return project.Focus{}, "", false
	}
	return focus, me.ID, true
}

// focusReadOperands is every path a read-shaped command in the line was pointed at.
func focusReadOperands(cmds []hint.Invocation) []string {
	var out []string
	for _, c := range cmds {
		reader, ok := focusReaders[path.Base(c.Name)]
		if !ok {
			continue
		}
		ops := operands(c.Args, reader.valueFlags)
		if reader.pattern && len(ops) > 0 {
			ops = ops[1:]
		}
		for _, op := range ops {
			// A bare word is a name at the workspace root, which is shared anyway, and
			// dropping it here is what keeps a stray pattern out of the verdict.
			if strings.Contains(op, "/") && !slices.Contains(out, op) {
				out = append(out, op)
			}
		}
	}
	return out
}

// focusResolve reads an operand the way the shell would: against the directory the
// command runs in, not against the workspace root.
func focusResolve(dir, op string) string {
	if filepath.IsAbs(op) {
		return op
	}
	return filepath.Join(dir, op)
}

// focusDir is where the command runs: the directory the host's envelope named, or
// the workspace root when it named none. Falling back to the root computes the ROOT
// project's focus, which is wider than the truth and so fails open.
func focusDir(location location) string {
	if location.dir != "" {
		return location.dir
	}
	return location.workspace
}
