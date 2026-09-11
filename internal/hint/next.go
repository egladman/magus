package hint

import (
	"path"
	"slices"
	"strings"

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
type Next struct {
	ID  string `json:"id"            yaml:"id"`
	Run string `json:"run"           yaml:"run"`
	Why string `json:"why,omitempty" yaml:"why,omitempty"`
}

// NextCap bounds how many breadcrumbs one result may carry. A next that fires every
// time trains ignoring, and context is the budget it spends.
const NextCap = 3

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
	next := []Next{{
		ID:  "query-explain",
		Run: Explain.With(out.Matches[0].ID),
		Why: "explain names a node's edges, provenance and blast radius, which is what says whether the top match is the one you meant.",
	}}
	// Ranked above path and refs: a reader holding a page wants the passage, and
	// nothing else in the result says the page is retrievable a heading at a time.
	if page, ok := unsectionedDocPage(out.Matches); ok {
		next = append(next, Next{
			ID:  "query-doc-sections",
			Run: Query.With(matcherArg("kind="+types.KindDocSection), matcherArg("id="+page)),
			Why: "every heading in that page is its own node, so the answer is one section to read instead of the whole file.",
		})
	}
	if a, b, ok := firstSharedKind(out.Matches); ok {
		next = append(next, Next{
			ID:  "query-path",
			Run: Path.With(a, b),
			Why: "two matches of one kind usually connect, and path prints the chain instead of leaving you to walk it.",
		})
	}
	if sym, ok := firstSymbol(out.Matches); ok {
		next = append(next, Next{
			ID:  "query-refs",
			Run: Refs.With(sym),
			Why: "refs lists a symbol's definition and every use, generated and cross-language ones included.",
		})
	}
	return capNext(next)
}

// NextForExplain breadcrumbs one node's context card: the path to its heaviest
// neighbor, its references when it is a symbol, and the classification of the file
// it was read from.
func NextForExplain(out types.KnowledgeExplainOutput) []Next {
	var next []Next
	if other, ok := heaviestNeighbor(out); ok {
		next = append(next, Next{
			ID:  "explain-path",
			Run: Path.With(out.Node.ID, other),
			Why: "path resolves the chain between two nodes, and this is the neighbor the card names most.",
		})
	}
	if out.Node.Kind == types.KindSymbol {
		next = append(next, Next{
			ID:  "explain-refs",
			Run: Refs.With(out.Node.Label),
			Why: "the card holds the graph's edges; refs holds the call sites.",
		})
	}
	if p := sourcePath(out.Node.Source); p != "" {
		next = append(next, Next{
			ID:  "explain-describe-file",
			Run: DescribeFile.With(p),
			Why: "describe file says whether that path is generated, a declared source, or claimed by nothing.",
		})
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
			next = append(next, Next{
				ID:  "file-impact",
				Run: Affected.With("--impact"),
				Why: "a declared source pulls its project into the affected set, and --impact is the set it pulls in.",
			})
			break
		}
	}
	for _, f := range files {
		if project := regeneratingProject(f); project != "" {
			next = append(next, Next{
				ID:  "file-regenerate",
				Run: Run.With("generate:rw", project),
				Why: "a declared output is never hand-edited: change the source of truth and regenerate it into the same commit.",
			})
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
		{
			ID:  "affected-plan",
			Run: Affected.With(target, "--plan"),
			Why: "the plan is the same set sharded, which is what CI runs and what says how long it will take.",
		},
		{
			ID:  "affected-explain",
			Run: Affected.With("--explain", projects[0]),
			Why: "a project in the set for a reason you did not expect is a declaration to fix, not a run to sit through.",
		},
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
		next = append(next, Next{
			ID:  "run-output",
			Run: QueryOutput.With(ref),
			Why: "the ref holds the run's whole captured output, so nothing has to be reproduced to be read.",
		})
	}
	if project != "" && target != "" {
		next = append(next, Next{
			ID:  "run-explain-target",
			Run: Explain.With("target:" + project + ":" + target),
			Why: "a target that fails on its inputs is explained by what feeds it, which the node names.",
		})
	}
	return capNext(next)
}

// capNext trims to NextCap and normalizes empty to nil.
//
// Absence has to be measurable, so a result with nothing to suggest carries no field
// at all rather than an empty list.
func capNext(next []Next) []Next {
	if len(next) == 0 {
		return nil
	}
	if len(next) > NextCap {
		return next[:NextCap]
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
