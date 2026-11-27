package job

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/types"
)

// WithoutEnded is list less the rows that ended, and how many that was. The reports derived
// from the rows (overlaps, blocked, stale) name live rows only, so only the read-only
// section can name an ended one.
func WithoutEnded(list job.List) (job.List, int) {
	ended := map[string]bool{}
	list.Jobs = slices.DeleteFunc(slices.Clone(list.Jobs), func(row job.Job) bool {
		if row.State.Terminal() {
			ended[row.ID] = true
		}
		return ended[row.ID]
	})
	list.ReadOnly = slices.DeleteFunc(slices.Clone(list.ReadOnly), func(r job.ReadOnly) bool { return ended[r.Job] })
	return list, len(ended)
}

// PrintInflight renders the in-flight changes grouped by whose turn each is, the ones
// waiting on the reader first. Unless all is set it lists the reader's changes and
// counts the rest on one line.
func PrintInflight(out io.Writer, list InflightList, all bool) {
	f := list.Fetched
	if f == nil {
		fmt.Fprintf(out, "in flight: never fetched; `%s queue ls --provider <provider> --base <branch>` reads the open changes\n", hint.BinaryName())
		return
	}
	at := f.Base
	if f.Tip != "" {
		at += " at " + ShortRevision(f.Tip)
	}
	fmt.Fprintf(out, "in flight on %s, as of %s (%s, %s, %dms)\n",
		at, time.Unix(f.At, 0).UTC().Format("2006-01-02 15:04 UTC"), f.Provider, planDash(f.Host), f.ElapsedMS)

	shown := InflightScope(list.Changes, all)
	partitions := 0
	for _, c := range list.Changes {
		partitions = max(partitions, c.Partition)
	}
	needs := "needs me"
	if all {
		needs = "needs the author"
	}
	groups := []struct {
		title     string
		attention inflightAttention
	}{
		{needs, attentionAuthor},
		{"waiting on review", attentionReview},
		{"queued, in plan order", attentionQueue},
		{"not queued", attentionNone},
	}
	for _, g := range groups {
		var rows []InflightChange
		for _, c := range shown {
			if c.Attention == g.attention {
				rows = append(rows, c)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if g.attention == attentionQueue {
			slices.SortStableFunc(rows, queueOrder)
		}
		fmt.Fprintf(out, "%s (%d)\n", g.title, len(rows))
		var table strings.Builder
		w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
		for _, c := range rows {
			fmt.Fprintf(w, "  #%s\t%s\t%s\t%s\n", c.ID, clipTitle(c.Title, 48), inflightDetail(c, partitions), inflightJobs(c.Jobs))
		}
		_ = w.Flush() // writes to a strings.Builder, which never errors
		// An empty last column leaves the padding before it at the end of the line.
		for line := range strings.Lines(table.String()) {
			fmt.Fprintln(out, strings.TrimRight(line, " \n"))
		}
	}
	if len(list.Unproposed) > 0 {
		fmt.Fprintf(out, "jobs without a change (%d)\n", len(list.Unproposed))
		for _, id := range list.Unproposed {
			fmt.Fprintf(out, "  %s\n", id)
		}
	}
	switch {
	case len(shown) > 0 || len(list.Unproposed) > 0:
	case all:
		fmt.Fprintln(out, "nothing in flight")
	default:
		fmt.Fprintln(out, "nothing of yours in flight")
	}
	if !all {
		printInflightOthers(out, list.Changes)
	}
	fmt.Fprintf(out, "refetch: %s queue ls --provider %s --base %s\n", hint.BinaryName(), f.Provider, f.Base)
}

// printInflightOthers counts the changes that are not the reader's on one line.
func printInflightOthers(out io.Writer, changes []InflightChange) {
	counts := map[inflightAttention]int{}
	for _, c := range changes {
		if !c.Mine {
			counts[c.Attention]++
		}
	}
	var parts []string
	for _, a := range []struct {
		attention inflightAttention
		word      string
	}{
		{attentionQueue, "queued"},
		{attentionAuthor, "kicked back"},
		{attentionReview, "waiting on review"},
		{attentionNone, "not queued"},
	} {
		if n := counts[a.attention]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, a.word))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(out, "others: %s\n", strings.Join(parts, ", "))
	}
}

// queueOrder puts planned changes first, by partition then position, and keeps the
// provider's order among the rest.
func queueOrder(a, b InflightChange) int {
	switch {
	case a.Position == 0 && b.Position == 0:
		return 0
	case a.Position == 0:
		return 1
	case b.Position == 0:
		return -1
	case a.Partition != b.Partition:
		return a.Partition - b.Partition
	}
	return a.Position - b.Position
}

// PrintChange names the change job id's checkout carries, whose turn it is, and the
// changes it touches.
func PrintChange(out io.Writer, changes []InflightChange, id string) {
	turns := map[inflightAttention]string{
		attentionAuthor: "its author's turn",
		attentionReview: "waiting on review",
		attentionQueue:  "the queue's turn",
		attentionNone:   "not queued",
	}
	for _, c := range changes {
		if !slices.Contains(c.Jobs, id) {
			continue
		}
		line := fmt.Sprintf("\nchange: #%s %s, %s", c.ID, clipTitle(c.Title, 48), turns[c.Attention])
		if d := inflightDetail(c, 0); d != "" {
			line += ", " + d
		}
		fmt.Fprintln(out, line)
		for _, n := range c.Neighbours {
			touched := slices.Concat(n.Paths, n.Declarations, n.Units)
			fmt.Fprintf(out, "  touches #%s: %s %s\n", n.ID, n.Evidence, strings.Join(touched, ", "))
		}
	}
}

// clipTitle cuts s to at most n bytes at a word boundary, marking the cut.
func clipTitle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], ' ')
	if cut <= 0 {
		cut = n
	}
	return strings.TrimRight(s[:cut], " ") + "..."
}

func PrintTree(out io.Writer, report job.List) {
	if len(report.Jobs) == 0 {
		fmt.Fprintf(out, "No jobs. Declare one with `%s`, or with the `%s` MCP tool (magus\\workspace().putJob), and every worktree of this repository reads it here.\n",
			hint.JobFork.With("<job>"), hint.ToolClient)
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "JOB\tHOLDER\tSTATE\tMODEL\tPATHS\tPROOF\tCHECK")
	marks := map[string][]string{}
	for word, ids := range map[string][]string{"overdue": report.Overdue, "orphan": report.Orphans, "stale": report.Stale} {
		for _, id := range ids {
			marks[id] = append(marks[id], word)
		}
	}
	for _, line := range TreeOrder(report.Jobs) {
		row := line.Job
		state := planDash(string(row.State))
		if m := marks[row.ID]; len(m) > 0 {
			slices.Sort(m)
			state += fmt.Sprintf(" (%s)", strings.Join(m, ", "))
		}
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			strings.Repeat("  ", line.Depth), row.ID,
			HolderCell(row), state, modelCell(row),
			len(row.Paths.Write), planDash(string(row.WriteProof)), planDash(row.Validation))
	}
	_ = w.Flush() // a report that cannot be written has nowhere better to say so

	for _, section := range []struct {
		title string
		ids   []string
	}{
		{"overdue: past the deadline their timeout set, so their writes are denied", report.Overdue},
		{"orphans: live under a job that has ended", report.Orphans},
		{"stale: not updated within jobs.stale_after", report.Stale},
	} {
		if len(section.ids) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s\n", section.title)
		for _, id := range section.ids {
			fmt.Fprintf(out, "  %s: if nobody holds it, `%s`\n", id, hint.JobExit.With(id))
		}
	}
	if len(report.Blocked) > 0 {
		fmt.Fprintln(out, "\nblocked: own no paths until every dependency passes")
		for _, b := range report.Blocked {
			fmt.Fprintf(out, "  %s: %s\n", b.Job, b.String())
		}
	}
	if len(report.ReadOnly) > 0 {
		fmt.Fprintln(out, "\n"+types.FormatDiagnostic(types.StoreRecordsNewer,
			"read-only jobs require what this magus lacks: update magus"))
		for _, r := range report.ReadOnly {
			fmt.Fprintf(out, "  %s: requires %s\n", r.Job, strings.Join(r.Lacks, ", "))
		}
	}
	printOverlaps(out, report.Overlaps)
}

func printOverlaps(out io.Writer, overlaps []job.Overlap) {
	if len(overlaps) == 0 {
		return
	}
	fmt.Fprintln(out, "\noverlaps")
	for _, o := range overlaps {
		fmt.Fprintf(out, "  %s and %s claim common ground\n", o.JobA, o.JobB)
		fmt.Fprintf(out, "    %s: %s\n", o.JobA, strings.Join(o.PathsA, ", "))
		fmt.Fprintf(out, "    %s: %s\n", o.JobB, strings.Join(o.PathsB, ", "))
		if o.Claims == job.ClaimsDisjoint {
			fmt.Fprintln(out, "    claims: disjoint (different declarations of one file: an integration order, not a wait)")
		} else if o.Claims != "" {
			fmt.Fprintf(out, "    claims: %s\n", o.Claims)
		}
		if line := overlapFootprintLine(o.Footprint); line != "" {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
}

// modelCell is the model the job names, else the model tier the store assigned it.
func modelCell(row job.Job) string {
	requested := row.Agent.RequestedModel()
	if requested.IsZero() && row.ModelTier != nil {
		return row.ModelTier.Name
	}
	return planDash(requested.String())
}

// HolderCell is who holds row as the tree names it: the server for its own jobs, the
// checkout a session job was taken in, and "-" for one nobody has taken. The checkout is
// the one fact that tells holders apart: every person and agent on a machine runs as the
// same OS account, and a CLI exec sees no other identity.
func HolderCell(row job.Job) string {
	switch {
	case row.ServerRun():
		return string(job.EntryPointServer)
	case row.CheckoutRoot != "":
		return filepath.Base(row.CheckoutRoot)
	}
	return "-"
}

// overlapFootprintLine is the footprint verdict under an overlapping pair, or "" when
// nobody computed one.
func overlapFootprintLine(f *job.OverlapFootprint) string {
	switch {
	case f == nil:
		return ""
	case f.Verdict == job.FootprintShared:
		return fmt.Sprintf("footprints: shared (%s)", strings.Join(f.Shared, ", "))
	case f.Verdict == job.FootprintUnknown:
		return fmt.Sprintf("footprints: unknown (%s)", f.Reason)
	}
	return "footprints: " + f.Verdict
}

// DescribeOutput is a job's terms plus where its declared postconditions stand, which is what
// `-o json`/`-o yaml` for `describe job` renders. json and yaml flatten the embedded
// terms, so the wire shape is the terms plus one `postconditions` key, the same
// queryWithNext/explainWithNext pattern uses to add a field without changing the type
// underneath it.
type DescribeOutput struct {
	Terms `yaml:",inline"`
	// Postconditions is where each of the job's declared postconditions stands, graded against the evidence
	// magus holds right now. Grading is still a READ: it records nothing, so asking never
	// advances a job and never blocks the holder still working on it. It is the same
	// grading `job wait` does, so the two cannot disagree.
	Postconditions job.Status `json:"postconditions" yaml:"postconditions"`
}

// describedJob is the job as `job apply` reads it. The stored row names no
// api_version, so the record adds it.
type describedJob struct {
	APIVersion types.APIVersion `json:"api_version"`
	job.Job
}

// MarshalJSON prints the job member as a describedJob. The shallower job
// member shadows Terms' own. YAML is no apply input and keeps the stored row.
func (o DescribeOutput) MarshalJSON() ([]byte, error) {
	return codec.Marshal(struct {
		Lease describedJob `json:"job"`
		Terms
		Postconditions job.Status `json:"postconditions"`
	}{
		Lease:          describedJob{APIVersion: types.APIVersion(job.APIVersion), Job: o.Lease},
		Terms:          o.Terms,
		Postconditions: o.Postconditions,
	})
}

// PrintClaimNotes prints each of a fork's claim notes on a line of its own.
func PrintClaimNotes(out io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
}

// ForkReport is ForkOutput with the write path claims the fork derived from its symbol
// postconditions. The fork computes them, and only paths.write stores them.
type ForkReport struct {
	ForkOutput    `yaml:",inline"`
	DerivedClaims []string `json:"derived_claims,omitempty" yaml:"derived_claims,omitempty"`
}

// PrintEntries lists a job's entries, one line each, or nothing when it has none.
func PrintEntries(out io.Writer, entries []job.Entry) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintln(out, "entries: writes into its paths by somebody other than its holder")
	for _, e := range entries {
		written := "not yet written"
		if e.Consumed != 0 {
			written = "written " + time.Unix(e.Consumed, 0).UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(out, "  %s  entered %s by %s, %s\n", e.Path,
			time.Unix(e.At, 0).UTC().Format(time.RFC3339), e.By.Label(), written)
	}
}

// PrintWorktree says where the checkout a job is bound to stands: made or adopted for it,
// removed, or kept and why. built is true when the fork just built its ./magus from its own
// sources.
func PrintWorktree(out io.Writer, row job.Job, built bool) {
	wt := row.Worktree
	switch {
	case wt == nil:
	case wt.Removed != 0:
		fmt.Fprintf(out, "checkout %s: removed once job %s ended\n", wt.Root, row.ID)
	case wt.Kept != "":
		fmt.Fprintf(out, "checkout %s: kept after job %s ended: %s\n", wt.Root, row.ID, wt.Kept)
	case !wt.Made:
		fmt.Fprintf(out, "checkout %s: adopted for job %s at %.12s; its holder takes it there with `%s`, and magus never removes it\n",
			wt.Root, row.ID, wt.Base, hint.JobExec.With(row.ID))
	default:
		fmt.Fprintf(out, "checkout %s: made for job %s at %.12s; its holder takes it there with `%s`, and magus removes it once the job ends unless it holds work\n",
			wt.Root, row.ID, wt.Base, hint.JobExec.With(row.ID))
		switch {
		case built:
			fmt.Fprintf(out, "  built %s from the checkout's own sources\n", wt.Binary)
		case wt.Binary != "":
			fmt.Fprintf(out, "  placed %s, the magus the forking checkout runs\n", wt.Binary)
		default:
			fmt.Fprintln(out, "  placed no magus: the forking checkout runs magus from PATH, and so does the job")
		}
	}
}

// PrintLandingBootstrap names the build a fork with no checkout of its own needs when the
// ./magus it lands beside was built before its checkpoint's Go build inputs, and says so
// when that cannot be told.
func PrintLandingBootstrap(ctx context.Context, out io.Writer, store *Store, row job.Job) {
	exe, _ := os.Executable()
	bootstrap, err := store.LandingBootstrap(ctx, exe, row.Checkpoint)
	switch {
	case err != nil:
		fmt.Fprintf(out, "landing magus: whether ./magus judges %s's checkpoint could not be told: %s\n", row.ID, err)
	case bootstrap == NoCheckoutHook:
		fmt.Fprintf(out, "landing magus: ./magus was built before the Go build inputs of %s's checkpoint, so its guard judges another tree, and this workspace has %s to rebuild it\n",
			row.ID, NoCheckoutHook)
	case bootstrap != "":
		fmt.Fprintf(out, "landing magus: ./magus was built before the Go build inputs of %s's checkpoint, so its guard judges another tree\n  next: `%s` rebuilds it\n",
			row.ID, bootstrap)
	}
}

// PrintFeedLine writes one event as one line. Fixed columns rather than prose, because the
// value of this feed is skimming a column: a person watching four workers is looking for
// the word "deny" going past, not reading sentences.
func PrintFeedLine(out io.Writer, e FeedEvent) {
	stamp := time.UnixMilli(e.Ts).Format("15:04:05")
	switch e.Kind {
	case FeedFile:
		fmt.Fprintf(out, "%s  file  %s\n", stamp, e.Action)
	case FeedTool:
		verdict := e.Decision
		if verdict == "" {
			verdict = "observed" // the guard judged nothing; see recorder.AppendAgentCommand
		}
		fmt.Fprintf(out, "%s  tool  %-8s %s\n", stamp, verdict, e.Action)
	default:
		status := "ok"
		if e.Outcome == journal.OutcomeError {
			status = "failed"
		}
		fmt.Fprintf(out, "%s  run   %-8s %s  %s\n", stamp, status, e.Action, e.Ref)
	}
	if e.Note != "" && e.Kind == FeedFile {
		fmt.Fprintf(out, "          %s\n", e.Note)
	}
}

func PrintStatus(out io.Writer, s Status) {
	if s.Verified {
		fmt.Fprintf(out, "verified %s, recorded %s\n", s.Job, job.StatePass)
	} else {
		fmt.Fprintf(out, "rejected %s\n", s.Job)
		for _, violation := range s.Violations {
			fmt.Fprintf(out, "  %s\n", violation)
		}
		for _, note := range s.Notes {
			fmt.Fprintln(out, note)
		}
	}
	if s.Command != "" {
		fmt.Fprintf(out, "its holder reports it ran %s\n", s.Command)
	}
	for _, gate := range s.Gates {
		state := "rejected"
		if gate.Verified {
			state = "verified"
		}
		fmt.Fprintf(out, "postcondition %s: %s", gate.ID, state)
		if gate.OutputRef != "" {
			fmt.Fprintf(out, " (%s)", gate.OutputRef)
		}
		fmt.Fprintln(out)
		// A failed gate names the ref but not how to read it; one command away
		// from the projects the run touched and the diff it produced.
		if !gate.Verified && gate.OutputRef != "" {
			fmt.Fprintf(out, "  %s\n", hint.QueryOutput.With(gate.OutputRef))
		}
	}
	printFootprint(out, s)
	PrintEntries(out, s.Entries)
	if !s.Clean() {
		fmt.Fprintln(out, "unresolved risks its holder reported")
		for _, risk := range s.UnresolvedRisks {
			fmt.Fprintf(out, "  %s\n", risk)
		}
	}
}

// printFootprint writes the declarations the job's diff landed in, one line each. An
// unknown footprint says why in one line: silence would read as a job that touched nothing.
// A landing outside the job's declaration claims is among the violations above it.
func printFootprint(out io.Writer, s Status) {
	const heading = "footprint"
	if !s.FootprintKnown {
		reason := s.FootprintReason
		if reason == "" {
			reason = "nothing observed the tree"
		}
		fmt.Fprintf(out, "%s: not known, %s\n", heading, reason)
		return
	}
	if len(s.Footprint) == 0 {
		fmt.Fprintf(out, "%s: no line changed since the checkpoint\n", heading)
		return
	}
	fmt.Fprintf(out, "%s: where its diff since the checkpoint landed\n", heading)
	var printed []string
	for _, r := range s.Footprint {
		line := r.Location().String()
		if r.Declaration == "" {
			line = fmt.Sprintf("%s:%d-%d", r.File.Path, r.Lines[0], r.Lines[1])
		}
		if slices.Contains(printed, line) {
			continue
		}
		printed = append(printed, line)
		if slices.ContainsFunc(s.Entries, func(e job.Entry) bool { return e.Consumed != 0 && Covers(e.Path, r.File.Path) }) {
			line += "  (entered: may be the entrant's write, not the holder's)"
		}
		fmt.Fprintf(out, "  %s\n", line)
	}
}

// ApplyReport is `job apply`'s structured output: each row as written, or as it would be.
type ApplyReport struct {
	Jobs   []ApplyEntry `json:"jobs"    yaml:"jobs"`
	DryRun bool         `json:"dry_run" yaml:"dry_run"`
}

type ApplyEntry struct {
	Job     job.Job  `json:"job"     yaml:"job"`
	Created bool     `json:"created" yaml:"created"`
	Changed []string `json:"changed" yaml:"changed"`
	// RegeneratedOutside is computed for a row whose write or deny paths the apply set.
	RegeneratedOutside []RegeneratedOutput `json:"regenerated_outside,omitempty" yaml:"regenerated_outside,omitempty"`
	// UnrunGates is computed beside it, for the same rows.
	UnrunGates []UnrunGate `json:"unrun_gates,omitempty" yaml:"unrun_gates,omitempty"`
	// ClaimNotes is ForkOutput's, for every applied row.
	ClaimNotes []string `json:"claim_notes,omitempty" yaml:"claim_notes,omitempty"`
	// SetAside is [Applied.SetAside].
	SetAside string `json:"set_aside,omitempty" yaml:"set_aside,omitempty"`
}

// PrintApply prints each row's change, then the claim notes for it.
func PrintApply(out io.Writer, applied []Applied, claims map[string][]string, dryRun bool) {
	for _, a := range applied {
		switch {
		case a.Created && dryRun:
			fmt.Fprintf(out, "would create %s with %d write path(s)\n", a.Next.ID, len(a.Next.Paths.Write))
		case a.Created:
			fmt.Fprintf(out, "created %s, %s, with %d write path(s)\n", a.Next.ID, a.Next.State, len(a.Next.Paths.Write))
			if a.Next.ModelTier != nil {
				fmt.Fprintf(out, "model tier: %s\n", a.Next.ModelTier)
			}
		case len(a.Changed) == 0:
			fmt.Fprintf(out, "unchanged %s\n", a.Next.ID)
		default:
			verb := "updated"
			if dryRun {
				verb = "would update"
			}
			if a.Prev.State == a.Next.State {
				fmt.Fprintf(out, "%s %s, still %s:\n", verb, a.Next.ID, a.Next.State)
			} else {
				fmt.Fprintf(out, "%s %s, %s to %s, so its holder takes it with `%s`:\n", verb, a.Next.ID, a.Prev.State, a.Next.State, hint.JobExec.With(a.Next.ID))
			}
			for _, line := range a.ChangeLines() {
				fmt.Fprintf(out, "  %s\n", line)
			}
			if note := a.SetAside(); note != "" {
				fmt.Fprintf(out, "note: %s\n", note)
			}
		}
		PrintClaimNotes(out, claims[a.Next.ID])
	}
	if dryRun {
		fmt.Fprintln(out, "dry run: nothing written; rerun without --dry-run to write it")
	}
}

// PruneReport is `job prune`'s structured output.
type PruneReport struct {
	Ended  []Ending `json:"ended"   yaml:"ended"`
	DryRun bool     `json:"dry_run" yaml:"dry_run"`
}

func PrintPruned(out io.Writer, ended []Ending, dryRun bool) {
	verb := "ended"
	if dryRun {
		verb = "would end"
	}
	for _, e := range ended {
		fmt.Fprintf(out, "%s %s: %s\n", verb, e.ID, e.Reason)
	}
	if dryRun {
		fmt.Fprintf(out, "would end %d job(s); --dry-run ended nothing\n", len(ended))
		return
	}
	fmt.Fprintf(out, "ended %d job(s)\n", len(ended))
}

func inflightJobs(ids []string) string {
	switch len(ids) {
	case 0:
		return ""
	case 1:
		return "job " + ids[0]
	}
	return "jobs " + strings.Join(ids, ", ")
}
