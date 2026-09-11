package hint

import (
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
)

// A breadcrumb belongs on the RESULT, not in the prose under it. Measured over 21
// days of this repo's agent sessions: the one breadcrumb magus already printed (the
// `magus query output <ref>` line after a failing run) was followed 21% of the time,
// the same rate as an unhinted next step, while re-running the same failing target
// won 84%. Text at the bottom of output does not convert.
//
// So a next is a FIELD: a harness can present it as an affordance, and because every
// entry carries a stable id, uptake per breadcrumb is a query rather than a guess.
// One that nobody takes gets deleted from the number instead of reworded.
//
// It stays a suggestion. magus informs and never decides, so a reader (or an agent)
// may ignore every one of them, and nothing here enforces an order.

// Next is one breadcrumb on a result: a complete command to run, a stable id to
// count it by, and one sentence saying what it answers.
//
// Run always has the real ids or refs filled in, never a placeholder, because a
// command that still needs editing is prose. Why is one sentence: a caller that
// repeats a result within a session shows Run every time and Why once.
//
// Argv is the same command as an argument vector, unquoted: Run is for a person to
// paste and Argv for a caller to exec, so the two differ wherever an argument needs
// shell quotes.
type Next struct {
	ID   string   `json:"id"             yaml:"id"`
	Run  string   `json:"run"            yaml:"run"`
	Argv []string `json:"argv,omitempty" yaml:"argv,omitempty"`
	Why  string   `json:"why,omitempty"  yaml:"why,omitempty"`
}

// breadcrumb builds one entry from raw, unquoted args: Run gets them shell-quoted,
// Argv gets them as they are.
func breadcrumb(id string, c Command, why string, args ...string) Next {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = matcherArg(a)
	}
	return Next{ID: id, Run: c.With(quoted...), Argv: c.Argv(args...), Why: why}
}

// nextCap bounds how many breadcrumbs one result may carry. A next that fires every
// time trains ignoring, and context is the budget it spends.
const nextCap = 3

// NextForQuery breadcrumbs a graph search: explain the top match, open a matched
// doc page's sections, connect two matches that share a kind, and list a matched
// symbol's references.
//
// Nil when nothing matched. An absent verdict has its own text and no node to
// point at.
func NextForQuery(out types.KnowledgeQueryOutput) []Next {
	if len(out.Matches) == 0 {
		return nil
	}
	next := []Next{breadcrumb("query-explain", Explain,
		"explain names a node's edges, provenance and blast radius, which is what says whether the top match is the one you meant.",
		out.Matches[0].ID)}
	// Ranked above path and refs: a reader holding a page wants the passage, and
	// nothing else in the result says the page is retrievable a heading at a time.
	if page, ok := unsectionedDocPage(out.Matches); ok {
		next = append(next, breadcrumb("query-doc-sections", Query,
			"every heading in that page is its own node, so the answer is one section to read instead of the whole file.",
			"kind="+types.KindDocSection, "id="+page))
	}
	if a, b, ok := firstSharedKind(out.Matches); ok {
		next = append(next, breadcrumb("query-path", Path,
			"two matches of one kind usually connect, and path prints the chain instead of leaving you to walk it.",
			a, b))
	}
	if sym, ok := firstSymbol(out.Matches); ok {
		next = append(next, breadcrumb("query-refs", Refs,
			"refs lists a symbol's definition and every use, generated and cross-language ones included.",
			sym))
	}
	return capNext(next)
}

// NextForExplain breadcrumbs one node's context card: the path to its heaviest
// neighbor, its references when it is a symbol, and the classification of the file
// it was read from.
func NextForExplain(out types.KnowledgeExplainOutput) []Next {
	var next []Next
	if other, ok := heaviestNeighbor(out); ok {
		next = append(next, breadcrumb("explain-path", Path,
			"path resolves the chain between two nodes, and this is the neighbor the card names most.",
			out.Node.ID, other))
	}
	if out.Node.Kind == types.KindSymbol {
		next = append(next, breadcrumb("explain-refs", Refs,
			"the card holds the graph's edges; refs holds the call sites.",
			out.Node.Label))
	}
	if p := sourcePath(out.Node.Source); p != "" {
		next = append(next, breadcrumb("explain-describe-file", DescribeFile,
			"describe file says whether that path is generated, a declared source, or claimed by nothing.",
			p))
	}
	return capNext(next)
}

// NextForFiles breadcrumbs a classification: the blast radius when any path feeds a
// target, and the regeneration when one is generated.
//
// Derived across the whole call rather than per entry, because the classification
// answers about a SET of paths and a breadcrumb per path would blow the cap on the
// first `git status` piped into it.
func NextForFiles(files []types.FileEntry) []Next {
	var next []Next
	for _, f := range files {
		if len(f.SourceOf) > 0 {
			next = append(next, breadcrumb("file-impact", Affected,
				"a declared source pulls its project into the affected set, and --impact is the set it pulls in.",
				"--impact"))
			break
		}
	}
	for _, f := range files {
		if project := regeneratingProject(f); project != "" {
			next = append(next, breadcrumb("file-regenerate", Run,
				"a declared output is never hand-edited: change the source of truth and regenerate it into the same commit.",
				"generate:rw", project))
			break
		}
	}
	return capNext(next)
}

// regeneratingProject names the project whose target writes f, or "" when nothing
// declares it. The DECLARER, not the owner: OutputOf follows the tree the file lands
// in, and for a cross-project output only the declaring project's generate target
// produces it (see types.FileClaim).
func regeneratingProject(f types.FileEntry) string {
	for _, c := range f.Claims {
		if c.Role == "output" {
			return c.Project
		}
	}
	if len(f.OutputOf) > 0 {
		return f.OutputOf[0]
	}
	return ""
}

// NextForAffected breadcrumbs the affected listing: the shard plan for target over
// that set, and why the first project is in it.
//
// Nil for an empty set, which is a complete answer with nothing to follow.
func NextForAffected(target string, projects []string) []Next {
	if target == "" || len(projects) == 0 {
		return nil
	}
	return []Next{
		breadcrumb("affected-plan", Affected,
			"the plan is the same set sharded, which is what CI runs and what says how long it will take.",
			target, "--plan"),
		breadcrumb("affected-explain", Affected,
			"a project in the set for a reason you did not expect is a declaration to fix, not a run to sit through.",
			"--explain", projects[0]),
	}
}

// NextForFailure breadcrumbs a failing target: its captured output, and the target's
// own graph node.
//
// The output entry restates the line a failing run already prints. That line is the
// control this whole mechanism was measured against, so it stays exactly as it is
// and gains a field beside it.
func NextForFailure(project, target, ref string) []Next {
	var next []Next
	if ref != "" {
		next = append(next, breadcrumb("run-output", QueryOutput,
			"the ref holds the run's whole captured output, so nothing has to be reproduced to be read.",
			ref))
	}
	if project != "" && target != "" {
		next = append(next, breadcrumb("run-explain-target", Explain,
			"a target that fails on its inputs is explained by what feeds it, which the node names.",
			"target:"+project+":"+target))
	}
	return capNext(next)
}

// Role is who a result is being served to, read off the acting lease's row by
// [RoleFor].
//
// It exists so the obligation sits where the breadcrumb is MINTED: serving a worker a
// write outside its lane and relying on the guard to refuse it afterwards teaches the
// reader that the tool's own advice does not apply to them.
type Role string

const (
	RoleUnbound  Role = "unbound"
	RoleWorker   Role = "worker"
	RoleReviewer Role = "reviewer"
)

// RoleFor grades the acting lease id against the rows, and returns the lane that id
// may write in. No id is unbound, a read-only row or one owning no path is a
// reviewer, anything else a worker.
//
// Derived rather than stored. The row already says what a lease may write, and a
// second field saying the same thing is a field that can disagree with it. A bound id
// whose row is gone still grades as a worker with no lane: something claimed a lane,
// and serving the full unbound set on the strength of a missing row is the wrong way
// to be wrong.
func RoleFor(rows []types.Lease, id string) (Role, []string) {
	if id == "" {
		return RoleUnbound, nil
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		if row.ReadOnly || len(row.OwnedPaths) == 0 {
			return RoleReviewer, nil
		}
		return RoleWorker, row.OwnedPaths
	}
	return RoleWorker, nil
}

// ServableTo drops the breadcrumbs role may not be served: every write for a
// reviewer, and for a worker every write that does not land inside lane. Unbound
// keeps the lot.
//
// Dropped, never rewritten. A template narrowed to fit a role would be a command
// nobody wrote, and the cap is applied afterwards so a filtered list still fills up
// to nextCap from what survives.
func ServableTo(role Role, lane []string, next []Next) []Next {
	if role == "" || role == RoleUnbound {
		return capNext(next)
	}
	kept := make([]Next, 0, len(next))
	for _, n := range next {
		if mutatesTree(n.Argv) && (role != RoleWorker || !withinLane(lane, n.Argv)) {
			continue
		}
		kept = append(kept, n)
	}
	return capNext(kept)
}

// readCommands is every declared Command that cannot change the tree. It is the
// ALLOWLIST mutatesTree grades against, so a verb added to clicommand.go and not
// added here reads as a write until somebody decides otherwise.
//
// `affected` is absent on purpose: it runs unless asked for one of its dry forms, so
// it gets a branch below rather than a row here.
var readCommands = []Command{
	Query, QueryOutput, QueryInvocation,
	GraphExport, GraphStats, GraphDiff,
	Status, Describe, DescribeTargets, DescribeTarget, DescribeProject, DescribeFile,
	DescribeGraph, DescribeMCPTools,
	Explain, Path, Diff, Doctor, Where, X, Ls, LsTargets, Refs,
	MemoryLs, MemoryVerify,
	Ledger, LedgerBrief,
	NotesLs, NotesGet,
	Session, SessionShow, SessionAttention,
	VCSCheckpoint,
	ConfigView, ConfigToken, ConfigTokenPrint,
	ConfigConsoleToken, ConfigMCPConnectorLs,
	AgentSample,
}

// mutatesTree reports whether argv would change the tree, judged from the command
// alone.
//
// DENY BY DEFAULT: a verb readCommands does not carry is a write. The inverse failed
// open, so `ledger accept`, `memory put`, `clean` and `self update` would all have
// been served to a reviewer as reads.
//
// Deliberately blunt on `run`: magus.yaml may declare default charms, so a bare
// `magus run generate <project>` writes exactly as `generate:rw` does, and reading
// the charm off the token would call that one read-only. `affected` goes the other
// way, since its dry forms are the ones breadcrumbs use.
func mutatesTree(argv []string) bool {
	if len(argv) < 2 {
		return true
	}
	for _, a := range argv[1:] {
		if strings.Contains(a, ":rw") {
			return true
		}
	}
	if argv[1] == Affected.Head() {
		return !slices.ContainsFunc(argv[2:], func(a string) bool {
			return a == "--plan" || a == "--impact" || a == "--explain" || a == "--dry-run"
		})
	}
	ran, ok := longestCommand(argv[1:])
	if !ok {
		return true
	}
	return !slices.ContainsFunc(readCommands, func(r Command) bool { return slices.Equal(r.tokens, ran.tokens) })
}

// longestCommand resolves args to the declared command it invokes, longest path
// first: `ledger accept` is its own command and not the readable `ledger` it opens
// with.
func longestCommand(args []string) (Command, bool) {
	var best Command
	for _, c := range AllCommands {
		if len(args) < len(c.tokens) || len(c.tokens) <= len(best.tokens) {
			continue
		}
		if slices.Equal(args[:len(c.tokens)], c.tokens) {
			best = c
		}
	}
	return best, len(best.tokens) > 0
}

// withinLane reports whether every project a write names sits inside lane, so a
// worker keeps the regeneration of its own project and loses everybody else's.
func withinLane(lane []string, argv []string) bool {
	projects := writeProjects(argv)
	if len(lane) == 0 || len(projects) == 0 {
		return false
	}
	for _, p := range projects {
		if !laneCovers(lane, p) {
			return false
		}
	}
	return true
}

// writeProjects names the project operands of a write, or nothing when the command
// takes none. Nothing is the conservative answer: a write that names no project
// touches whatever the workspace resolves, which is wider than any lane.
func writeProjects(argv []string) []string {
	if len(argv) < 4 || argv[1] != Run.Head() {
		return nil
	}
	var projects []string
	for _, a := range argv[3:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		projects = append(projects, a)
	}
	return projects
}

// laneCovers reports whether one of lane's declared globs covers project. An entry
// naming the whole tree by naming nothing is skipped, the way the guard's own
// declaration match skips it: it would put the lease on every path in the plan.
func laneCovers(lane []string, project string) bool {
	rel := path.Clean(strings.TrimSpace(project))
	for _, raw := range lane {
		decl := path.Clean(strings.TrimSpace(raw))
		if decl == "." || decl == "/" {
			continue
		}
		if ok, err := doublestar.Match(decl, rel); err == nil && ok {
			return true
		}
		if ok, err := doublestar.Match(decl+"/**", rel); err == nil && ok {
			return true
		}
	}
	return false
}

// capNext trims to nextCap and normalizes empty to nil.
//
// Absence has to be measurable, so a result with nothing to suggest carries no field
// at all rather than an empty list.
func capNext(next []Next) []Next {
	if len(next) == 0 {
		return nil
	}
	if len(next) > nextCap {
		return next[:nextCap]
	}
	return next
}

// firstSharedKind returns the first two match ids of one kind, in rank order.
func firstSharedKind(matches []types.KnowledgeMatch) (a, b string, ok bool) {
	seen := make(map[string]string, len(matches))
	for _, m := range matches {
		if first, dup := seen[m.Kind]; dup {
			return first, m.ID, true
		}
		seen[m.Kind] = m.ID
	}
	return "", "", false
}

// unsectionedDocPage returns the highest-ranked doc page's repo-relative path, which
// is the id fragment its headings share, and false when a section already matched.
//
// A result that already carries sections has led the reader to the passage, so the
// breadcrumb would point at what is on the screen. It fires only for the page-level
// answer, which is the one that leaves a whole file to scan.
func unsectionedDocPage(matches []types.KnowledgeMatch) (string, bool) {
	page := ""
	for _, m := range matches {
		switch m.Kind {
		case types.KindDocSection:
			return "", false
		case types.KindDoc:
			if page == "" {
				page = strings.TrimPrefix(m.ID, types.KindDoc+":")
			}
		}
	}
	return page, page != ""
}

// firstSymbol returns the highest-ranked symbol match's NAME, which is what refs
// takes; a symbol's node id is not accepted there.
func firstSymbol(matches []types.KnowledgeMatch) (string, bool) {
	for _, m := range matches {
		if m.Kind == types.KindSymbol && m.Label != "" {
			return m.Label, true
		}
	}
	return "", false
}

// heaviestNeighbor returns the node joined to the focus by the most edges, ties going
// to the one the card lists first (out edges before in).
//
// Edge count rather than a weight: an explain card carries no per-edge score. A card
// whose neighbors all sit on one edge therefore answers with the first one, which is
// why the breadcrumb claims only that the card names it most.
func heaviestNeighbor(out types.KnowledgeExplainOutput) (string, bool) {
	counts := make(map[string]int)
	var order []string
	for _, e := range slices.Concat(out.Out, out.In) {
		if e.Other == "" || e.Other == out.Node.ID {
			continue
		}
		if counts[e.Other] == 0 {
			order = append(order, e.Other)
		}
		counts[e.Other]++
	}
	best := ""
	for _, id := range order {
		if best == "" || counts[id] > counts[best] {
			best = id
		}
	}
	return best, best != ""
}

// sourcePath strips a node's line suffix, since provenance is "path" or "path:line"
// and describe file classifies paths. It returns "" for provenance that names no file.
//
// An extension on the base name is the test, because a target's provenance is its
// PROJECT directory: `magus describe file .` runs and answers nothing anyone asked.
// An extensionless file loses the breadcrumb, which is the cheap direction to be wrong
// in. path.Ext cannot serve here, since it reads "." as an extension of itself.
func sourcePath(source string) string {
	if source == "" {
		return ""
	}
	if i := strings.LastIndex(source, ":"); i > 0 {
		if line := source[i+1:]; line != "" && strings.IndexFunc(line, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			source = source[:i]
		}
	}
	base := path.Base(source)
	if dot := strings.LastIndex(base, "."); dot <= 0 || dot == len(base)-1 {
		return ""
	}
	return source
}

// Render writes the two-line `next:` block: the command on its own line, and the
// reason indented under it. why decides what a caller shows on the second line, since
// the CLI silences a Why it has already fired and -s drops it outright.
//
// One renderer for both doors: a reader who meets the breadcrumbs over MCP and on a
// terminal meets one layout.
func Render(next []Next, why func(Next) string) string {
	if len(next) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nnext:\n")
	for _, n := range next {
		b.WriteString("  " + n.Run + "\n")
		if w := why(n); w != "" {
			b.WriteString("      " + w + "\n")
		}
	}
	return b.String()
}
