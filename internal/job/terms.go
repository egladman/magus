package job

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// Terms is what one job grants its holder: the declared job, and the workspace facts
// magus resolved against it.
//
// CONTEXT, NEVER A STATUS. magus assembles what it holds and a person or an orchestrator
// hands it on; nothing here calls a model and nothing here decides what the holder should do.
//
// IT CARRIES NO PROCEDURE. Taking a job is `magus job exec`'s work to DO, and the fifty
// lines of "run this, then this" that used to render here were a procedure a reader could
// skip, mistype, or half-follow. A command magus can run itself is not documentation.
//
// DETERMINISM IS THE REQUIREMENT, not a nicety. Terms an orchestrating model paraphrases
// diverge between two holders reading one contract, which is what seven hand-typed briefs
// did on 2026-09-09: the gate leaked into every one of them and two units disagreed about
// a dedup key. Two renders of one job are byte-identical or the store has stopped being
// the single statement of the plan.
type Terms struct {
	Lease types.Job `json:"job" yaml:"job"`
	// Evidence is one line per write path the knowledge graph resolved, in declaration
	// order. A path the graph does not know is ABSENT here rather than reported as zero:
	// an unknown blast radius and a blast radius of zero are different facts, and the
	// second one would read as permission to edit freely.
	Evidence []TermsEvidence `json:"evidence,omitempty" yaml:"evidence,omitempty"`
	// GraphCold marks a brief rendered against no graph at all, so empty Evidence reads
	// as "not asked" rather than "asked, and nothing depends on any of this".
	GraphCold bool `json:"graph_cold,omitempty" yaml:"graph_cold,omitempty"`
	// Projects are the workspace projects the write paths reach, the same set
	// `magus affected` computes from those paths. It is what makes the two derived
	// lists below readable: a boundary without the projects it came from is a fence
	// with no map.
	Projects []string `json:"projects,omitempty" yaml:"projects,omitempty"`
	// DerivedDenyPaths is the boundary the WORKSPACE puts on this lease, kept apart from
	// the row's own DenyPaths. Those are what an orchestrator remembered to write
	// down; these hold whether anybody wrote them down or not, which is why a brief
	// that carried only the declared list handed workers a boundary its author's
	// memory had bounded.
	DerivedDenyPaths []TermsBoundary `json:"derived_deny_paths,omitempty" yaml:"derived_deny_paths,omitempty"`
	// WorkspaceCold marks a brief rendered without a loadable workspace, so an empty
	// DerivedDenyPaths reads as "not asked". The row alone carries the criteria, the boundary
	// and the check, and a worker in a tree whose magusfile is mid-edit still needs them.
	WorkspaceCold bool `json:"workspace_cold,omitempty" yaml:"workspace_cold,omitempty"`
	// RegeneratedOutside is what [RegeneratedOutside] finds for the row: outputs a target
	// regenerates from sources the holder may edit, in files the holder may not write.
	RegeneratedOutside []RegeneratedOutput `json:"regenerated_outside,omitempty" yaml:"regenerated_outside,omitempty"`
	// UnrunGates is what [UnrunGates] finds: ci targets over the write paths that nothing
	// the job is graded on runs.
	UnrunGates []UnrunGate `json:"unrun_gates,omitempty" yaml:"unrun_gates,omitempty"`
	// Affinity is the co-change evidence for the lease's projects against projects it
	// does NOT own. A WARNING and never a boundary: the skill reads strong hidden
	// affinity as a reason to reduce parallelism, which is the orchestrator's call to
	// make and not a path this worker is refused.
	Affinity []TermsAffinity `json:"affinity,omitempty" yaml:"affinity,omitempty"`
	// Grouping is the grouping lens over the groups that wrote in the lease's projects: what
	// splitting or chaining work there cost before. Facts for the orchestrator, like
	// Affinity; nil when it was not read.
	Grouping *types.GroupingOutput `json:"grouping,omitempty" yaml:"grouping,omitempty"`
	// Stage is where the workspace's releases stand, from its newest tag. The zero stage is
	// unknown and renders nothing.
	Stage types.ReleaseStage `json:"stage" yaml:"stage"`
	// DeletionCandidates are the unreferenced symbols in the write paths, from
	// [UnreferencedInPaths]: findings to finish or delete, never goals.
	DeletionCandidates []types.UnreferencedEntry `json:"deletion_candidates,omitempty" yaml:"deletion_candidates,omitempty"`
	// ReferencedOutside are the files outside the write paths that use what they define,
	// from [ReferencedOutside]: findings, never a boundary.
	ReferencedOutside []ReferencedFile `json:"referenced_outside,omitempty" yaml:"referenced_outside,omitempty"`
}

// TermsEvidence is what the knowledge graph knows about one write path: the node it
// resolved to and how many nodes can transitively reach it, the same count
// `magus explain` prints.
type TermsEvidence struct {
	Path        string `json:"path"         yaml:"path"`
	Node        string `json:"node"         yaml:"node"`
	BlastRadius int    `json:"blast_radius" yaml:"blast_radius"`
}

// TermsBoundary is one path the workspace keeps out of a lease's reach, with the
// declaration that keeps it there.
//
// The reason is the load-bearing half. A bare path reads as an arbitrary fence, and a
// worker cannot tell a generated file it should REGENERATE from a file another live
// lease is holding, which are opposite instructions.
type TermsBoundary struct {
	Path   string `json:"path"   yaml:"path"`
	Reason string `json:"reason" yaml:"reason"`
}

// TermsAffinity is one project pair that changes together while the lease owns only one
// side of it, and neither project declares a dependency on the other.
//
// HIDDEN coupling only. A pair that changes together and says so in its declarations is
// the workspace working as designed, and the root project of a monorepo co-changes with
// everything; reporting those turns a warning list into a census. What is worth a
// worker's attention is coupling no declaration would have told it about, which is the
// pair a partition is most likely to have split wrongly.
type TermsAffinity struct {
	Project string `json:"project" yaml:"project"`
	With    string `json:"with"    yaml:"with"`
	Commits int    `json:"commits" yaml:"commits"`
}

// TermsFacts is what the WORKSPACE contributes to a brief: everything [NewTerms] cannot
// derive from the row alone, because it needs a knowledge graph and a loadable workspace.
// The zero value is a brief rendered from the row and nothing else.
type TermsFacts struct {
	Evidence           []TermsEvidence
	GraphCold          bool
	WorkspaceCold      bool
	Projects           []string
	DerivedDenyPaths   []TermsBoundary
	RegeneratedOutside []RegeneratedOutput
	UnrunGates         []UnrunGate
	Affinity           []TermsAffinity
	Grouping           *types.GroupingOutput
	Stage              types.ReleaseStage
	DeletionCandidates []types.UnreferencedEntry
	ReferencedOutside  []ReferencedFile
}

// NewTerms renders one job's terms: the job itself, plus what the workspace contributed.
func NewTerms(row types.Job, facts TermsFacts) Terms {
	return Terms{
		Lease:              row,
		Evidence:           facts.Evidence,
		GraphCold:          facts.GraphCold,
		WorkspaceCold:      facts.WorkspaceCold,
		Projects:           facts.Projects,
		DerivedDenyPaths:   facts.DerivedDenyPaths,
		RegeneratedOutside: facts.RegeneratedOutside,
		UnrunGates:         facts.UnrunGates,
		Affinity:           facts.Affinity,
		Grouping:           facts.Grouping,
		Stage:              facts.Stage,
		DeletionCandidates: facts.DeletionCandidates,
		ReferencedOutside:  facts.ReferencedOutside,
	}
}

// String renders the terms. The order is fixed and nothing outside it is printed: a
// section with nothing in it is dropped, so a holder never reads a heading that grants it
// room the job did not.
//
// The narrowest section is the check, and it is the whole point of rendering at all.
// Naming one check and no others is what stops `ci` leaking into every job's terms.
func (b Terms) String() string {
	var s strings.Builder
	fmt.Fprintf(&s, "job: %s\n", b.Lease.ID)
	if t := b.Lease.ModelTier; t != nil {
		fmt.Fprintf(&s, "model tier: %s\nskills: %s\n", t, t.Skills)
	}

	writeList(&s, "interface", interfaceLines(b.Lease.Goals))
	writeBlock(&s, "criteria", b.Lease.Criteria)
	writeAttempts(&s, b.Lease)
	writeList(&s, "may break", b.Lease.Breaks)
	if b.Stage.Known() {
		writeList(&s, "release stage", []string{stageLine(b.Stage)})
	}
	writeList(&s, deletionHeading, deletionLines(b.DeletionCandidates))
	writeList(&s, "write paths", b.Lease.WritePaths)
	writeList(&s, "deny paths", b.Lease.DenyPaths)
	writeList(&s, referencedHeading, referencedLines(b.ReferencedOutside))
	writeList(&s, "projects", b.Projects)

	switch {
	case b.WorkspaceCold:
		writeList(&s, "deny paths the workspace declares",
			[]string{"none: this workspace would not load, so nothing was derived from it"})
	case len(b.DerivedDenyPaths) > 0:
		lines := make([]string, len(b.DerivedDenyPaths))
		for i, d := range b.DerivedDenyPaths {
			lines[i] = fmt.Sprintf("%s: %s", d.Path, d.Reason)
		}
		writeList(&s, "deny paths the workspace declares", lines)
	}
	writeList(&s, regeneratedHeading, regeneratedLines(b.RegeneratedOutside))
	writeList(&s, unrunHeading, unrunLines(b.UnrunGates))

	if len(b.Affinity) > 0 {
		lines := make([]string, len(b.Affinity))
		for i, a := range b.Affinity {
			lines[i] = fmt.Sprintf("%s changes with %s in %d commits, and neither declares a dependency on the other", a.Project, a.With, a.Commits)
		}
		writeList(&s, "affinity warnings", lines)
	}
	if b.Grouping != nil {
		writeList(&s, "grouping history", groupingLines(*b.Grouping))
	}

	switch {
	case b.GraphCold:
		writeList(&s, "graph evidence", []string{"none: the knowledge graph is cold (" + hint.GraphBuild.String() + ")"})
	case len(b.Evidence) > 0:
		lines := make([]string, len(b.Evidence))
		for i, e := range b.Evidence {
			lines[i] = fmt.Sprintf("%s -> %s, blast radius %d", e.Path, e.Node, e.BlastRadius)
		}
		writeList(&s, "graph evidence", lines)
	}

	writeBlock(&s, "the only check you run", b.Lease.Validation)
	writeList(&s, "depends on", b.Lease.DependsOn)
	return s.String()
}

// interfaceLines is one entry per symbol goal: what the job must leave behind, as the
// signature the fork froze. It leads the terms because the contract a holder implements
// is the part the rest of the brief only bounds.
func interfaceLines(goals []types.Goal) []string {
	var lines []string
	for _, g := range goals {
		if g.Kind != types.GoalKindSymbol {
			continue
		}
		line := strings.Join(g.Symbols, ", ") + " (" + string(g.Expect) + ")"
		if g.Signature != "" {
			line += ": " + g.Signature
		}
		lines = append(lines, line)
		if d := strings.TrimSpace(g.Description); d != "" {
			lines = append(lines, "  "+d)
		}
	}
	return lines
}

// maxUnmetShown caps the unmet goals the newest previous attempt lists.
const maxUnmetShown = 5

// writeAttempts writes which attempt the holder is on and what the attempts before it left:
// the newest in full, so the next holder starts from what failed and never from a
// transcript, and each older one in a line, newest first. A first attempt with no cap writes
// nothing.
func writeAttempts(s *strings.Builder, row types.Job) {
	if row.Attempts > 1 || row.MaxAttempts > 0 {
		line := fmt.Sprintf("attempt %d", max(row.Attempts, 1))
		if row.MaxAttempts > 0 {
			line += fmt.Sprintf(" of %d", row.MaxAttempts)
		}
		fmt.Fprintf(s, "\n%s\n", line)
	}
	if len(row.Prior) == 0 {
		return
	}
	newest := row.Prior[len(row.Prior)-1]
	lines := append([]string{priorSummary(newest)}, priorDetail(newest)...)
	for _, p := range slices.Backward(row.Prior[:len(row.Prior)-1]) {
		lines = append(lines, priorSummary(p)+", "+retryLine(p.Retry))
	}
	writeList(s, "previous attempts", lines)
}

// priorSummary is one ended attempt in a line: how it ended, at which tier, and how much
// more work and room it was given on the way.
func priorSummary(p types.PriorAttempt) string {
	line := fmt.Sprintf("attempt %d: %s", p.N, p.State)
	if p.EndReason != "" {
		line += " (" + p.EndReason + ")"
	}
	if p.ModelTier != "" {
		line += ", tier " + p.ModelTier
	}
	return line + fmt.Sprintf(", %d rework round(s), %d widening(s)", p.ReworkRounds, p.Widenings)
}

// priorDetail is what the newest ended attempt left for the next holder, indented under its
// summary, ending with its retry verdict.
func priorDetail(p types.PriorAttempt) []string {
	var lines []string
	if len(p.Unmet) > 0 {
		ids := make([]string, 0, maxUnmetShown)
		for _, g := range p.Unmet[:min(len(p.Unmet), maxUnmetShown)] {
			ids = append(ids, g.ID)
		}
		line := "  unmet: " + strings.Join(ids, ", ")
		if more := len(p.Unmet) - len(ids); more > 0 {
			line += fmt.Sprintf(" and %d more", more)
		}
		lines = append(lines, line)
	}
	for _, v := range p.Violations {
		lines = append(lines, "  violation: "+firstLine(v))
	}
	for _, r := range p.UnresolvedRisks {
		lines = append(lines, "  unresolved risk: "+firstLine(r))
	}
	for _, d := range p.Denials {
		line := "  denied: " + d.Rule
		if reason := firstLine(d.Reason); reason != "" {
			line += ": " + reason
		}
		lines = append(lines, line)
	}
	if !p.DenialsKnown {
		lines = append(lines, "  (denials unknown: the trail was rotated)")
	}
	for _, ref := range p.FailingRefs {
		lines = append(lines, "  failed run: reopen with `"+hint.QueryOutput.With(ref)+"`")
	}
	return append(lines, "  "+retryLine(p.Retry))
}

// retryLine is an attempt's retry verdict as describe prints it.
func retryLine(v types.RetryVerdict) string {
	if v.Retryable {
		return "retryable"
	}
	line := "not retryable until " + string(cmp.Or(v.Change, types.RetryAfterPerson))
	if v.Reason != "" {
		line += ": " + firstLine(v.Reason)
	}
	return line
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func stageLine(s types.ReleaseStage) string {
	line := "newest release " + s.Tag
	if s.Major == 0 {
		line += ": below 1.0 a major floor ships as a minor release"
	}
	return line
}

// writeBlock emits a heading and a verbatim body. Verbatim is deliberate for the criteria:
// acceptance criteria the orchestrator wrote are the contract, and reflowing them is the
// paraphrase this whole type exists to remove.
func writeBlock(s *strings.Builder, heading, body string) {
	if strings.TrimSpace(body) == "" {
		return
	}
	fmt.Fprintf(s, "\n%s\n%s\n", heading, strings.TrimRight(body, "\n"))
}

func writeList(s *strings.Builder, heading string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(s, "\n%s\n", heading)
	for _, it := range items {
		fmt.Fprintf(s, "  %s\n", it)
	}
}
