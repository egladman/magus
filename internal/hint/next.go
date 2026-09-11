package hint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
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
// Argv is the same command as an argument vector, unquoted. Run is for a person to
// paste and Argv for a caller to exec, and the two differ wherever an argument needs
// shell quotes; a harness that offers the breadcrumb as an affordance needs the form
// no shell has to parse.
type Next struct {
	ID   string   `json:"id"             yaml:"id"`
	Run  string   `json:"run"            yaml:"run"`
	Argv []string `json:"argv,omitempty" yaml:"argv,omitempty"`
	Why  string   `json:"why,omitempty"  yaml:"why,omitempty"`
}

// entry builds one breadcrumb from raw, unquoted args: Run gets them shell-quoted,
// Argv gets them as they are.
func entry(id string, c Command, why string, args ...string) Next {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = matcherArg(a)
	}
	return Next{ID: id, Run: c.With(quoted...), Argv: c.Argv(args...), Why: why}
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
	next := []Next{entry("query-explain", Explain,
		"explain names a node's edges, provenance and blast radius, which is what says whether the top match is the one you meant.",
		out.Matches[0].ID)}
	// Ranked above path and refs: a reader holding a page wants the passage, and
	// nothing else in the result says the page is retrievable a heading at a time.
	if page, ok := unsectionedDocPage(out.Matches); ok {
		next = append(next, entry("query-doc-sections", Query,
			"every heading in that page is its own node, so the answer is one section to read instead of the whole file.",
			"kind="+types.KindDocSection, "id="+page))
	}
	if a, b, ok := firstSharedKind(out.Matches); ok {
		next = append(next, entry("query-path", Path,
			"two matches of one kind usually connect, and path prints the chain instead of leaving you to walk it.",
			a, b))
	}
	if sym, ok := firstSymbol(out.Matches); ok {
		next = append(next, entry("query-refs", Refs,
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
		next = append(next, entry("explain-path", Path,
			"path resolves the chain between two nodes, and this is the neighbor the card names most.",
			out.Node.ID, other))
	}
	if out.Node.Kind == types.KindSymbol {
		next = append(next, entry("explain-refs", Refs,
			"the card holds the graph's edges; refs holds the call sites.",
			out.Node.Label))
	}
	if p := sourcePath(out.Node.Source); p != "" {
		next = append(next, entry("explain-describe-file", DescribeFile,
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
			next = append(next, entry("file-impact", Affected,
				"a declared source pulls its project into the affected set, and --impact is the set it pulls in.",
				"--impact"))
			break
		}
	}
	for _, f := range files {
		if project := regeneratingProject(f); project != "" {
			next = append(next, entry("file-regenerate", Run,
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
		entry("affected-plan", Affected,
			"the plan is the same set sharded, which is what CI runs and what says how long it will take.",
			target, "--plan"),
		entry("affected-explain", Affected,
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
		next = append(next, entry("run-output", QueryOutput,
			"the ref holds the run's whole captured output, so nothing has to be reproduced to be read.",
			ref))
	}
	if project != "" && target != "" {
		next = append(next, entry("run-explain-target", Explain,
			"a target that fails on its inputs is explained by what feeds it, which the node names.",
			"target:"+project+":"+target))
	}
	return capNext(next)
}

// Role is who a result is being served to. The caller derives it from the acting
// lease's row: no acting lease is Unbound (a person, or the orchestrator), a row that
// is read-only or owns no path is Reviewer, anything else is Worker.
//
// It exists so the obligation sits where the breadcrumb is MINTED. A suggestion magus
// makes is one a reader may take on trust, so serving a worker a write outside its
// lane and relying on the guard to refuse it afterwards spends a turn teaching the
// reader that the tool's own advice does not apply to them.
type Role string

const (
	RoleUnbound  Role = "unbound"
	RoleWorker   Role = "worker"
	RoleReviewer Role = "reviewer"
)

// ForRole drops the breadcrumbs role may not be served: every write for a reviewer,
// and for a worker every write whose lane cannot be known here (a `magus run`, an
// `affected` that is not a dry read, a `:rw` charm, a VCS mutation). Unbound keeps
// the lot.
//
// Dropped, never rewritten. A template narrowed to fit a role would be a command
// nobody wrote, and the cap is applied afterwards so a filtered list still fills up
// to NextCap from what survives.
func ForRole(role Role, next []Next) []Next {
	if role == "" || role == RoleUnbound {
		return capNext(next)
	}
	kept := make([]Next, 0, len(next))
	for _, n := range next {
		if Writes(n.Argv) {
			continue
		}
		kept = append(kept, n)
	}
	return capNext(kept)
}

// Writes reports whether argv would change the tree, judged from the command alone.
//
// Deliberately blunt on `run`: magus.yaml may declare default charms, so a bare
// `magus run generate <project>` writes exactly as `generate:rw` does, and reading
// the charm off the token would call that one read-only. `affected` goes the other
// way, since its dry forms are the ones breadcrumbs use.
func Writes(argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	for _, a := range argv[1:] {
		if strings.Contains(a, ":rw") {
			return true
		}
	}
	switch argv[1] {
	case Run.Head():
		return true
	case Affected.Head():
		return !slices.ContainsFunc(argv[2:], func(a string) bool {
			return a == "--plan" || a == "--impact" || a == "--explain" || a == "--dry-run"
		})
	case VCSAdd.Head():
		return len(argv) > 2 && slices.Contains([]string{VCSAdd.Leaf(), VCSResolve.Leaf(), "merge-driver"}, argv[2])
	}
	return false
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

// The served-next journal: one line per breadcrumb actually put in front of a
// reader, written where the advisory markers already live.
//
// Two readers, one file. The guard treats a command it served within the last few
// calls as already vetted, which needs recency rather than history; `session load`
// joins the same lines onto a host's transcript so uptake per id becomes a query
// instead of a guess at what a result carried. Nothing here is a decision, so a
// write that fails is dropped rather than reported: a journal is not worth failing
// a query over.
const (
	servedNextDir    = "advisories"
	servedNextSuffix = ".served-next"

	// servedNextKept bounds the file on rotate. It is a recency window for the
	// guard, not an archive; the store is where a served id lives long enough to be
	// counted.
	servedNextKept = 200
)

// ServedNextEntry is one journal line: when a breadcrumb was served, which id, and
// the exact argv the reader was handed.
type ServedNextEntry struct {
	Ts   int64    `json:"ts"`
	ID   string   `json:"id"`
	Argv []string `json:"argv"`
}

// ServedNextPath names the journal for session, under cacheDir. The session id is
// hashed, and an empty one lands in the anonymous bucket, on the same key derivation
// the advisory markers use: a host-chosen string may hold separators, and a path
// assembled from one is a path the input picked.
//
// Empty when cacheDir is, which is the caller's signal that there is nowhere to
// write.
func ServedNextPath(cacheDir, session string) string {
	if cacheDir == "" {
		return ""
	}
	key := "anon"
	if s := strings.TrimSpace(session); s != "" {
		sum := sha256.Sum256([]byte(s))
		key = hex.EncodeToString(sum[:6])
	}
	return filepath.Join(cacheDir, servedNextDir, key+servedNextSuffix)
}

// AppendServedNext records that next was served to session, one line each. Best
// effort throughout: every failure returns silently, and an entry that will not
// marshal is skipped rather than losing the rest of the batch.
func AppendServedNext(cacheDir, session string, next []Next) {
	path := ServedNextPath(cacheDir, session)
	if path == "" || len(next) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	ts := time.Now().UnixMilli()
	var buf bytes.Buffer
	for _, n := range next {
		line, err := json.Marshal(ServedNextEntry{Ts: ts, ID: n.ID, Argv: n.Argv})
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(buf.Bytes())
	if err := f.Close(); err != nil {
		return
	}
	rotateServedNext(path)
}

// rotateServedNext trims the journal to its newest servedNextKept lines.
func rotateServedNext(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte("\n"))
	if len(lines) <= servedNextKept {
		return
	}
	kept := bytes.Join(lines[len(lines)-servedNextKept:], []byte("\n"))
	_ = os.WriteFile(path, append(kept, '\n'), 0o644)
}

// ReadServedNext returns every journal entry under cacheDir, oldest first, across
// all sessions that wrote one. A line that will not decode is skipped: the file is
// append-only from several processes, so a torn tail is expected rather than
// exceptional.
func ReadServedNext(cacheDir string) []ServedNextEntry {
	if cacheDir == "" {
		return nil
	}
	dir := filepath.Join(cacheDir, servedNextDir)
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []ServedNextEntry
	for _, e := range names {
		if e.IsDir() || !strings.HasSuffix(e.Name(), servedNextSuffix) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var entry ServedNextEntry
			if err := json.Unmarshal(line, &entry); err != nil || entry.ID == "" {
				continue
			}
			out = append(out, entry)
		}
	}
	slices.SortStableFunc(out, func(a, b ServedNextEntry) int { return int(a.Ts - b.Ts) })
	return out
}
