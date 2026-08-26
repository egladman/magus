package review

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/prompt"
	"github.com/egladman/magus/types"
)

// PromptFileLimit caps how many files a prompt names individually. The changeset arrives ordered
// by what magus recommends reading first, so the cut takes the tail rather than an arbitrary
// slice - and the count of what was left out is always stated, because a silently truncated list
// reads as a complete one.
const PromptFileLimit = 25

// PromptBranchLimit caps how many colliding branches a prompt names. On a repository where every
// branch touches the same few shared files, the honest answer is "many", and listing forty of them
// buries the changeset the prompt is actually about.
const PromptBranchLimit = 6

// PromptOverlapPathLimit caps the shared paths named for any ONE branch.
//
// The branch cap alone stopped bounding this once local branches were scanned: against a large
// changeset a single long-lived branch shares seventy files, and six of those lines is most of the
// prompt. What the reader needs is which branch and roughly how much, not a manifest - the exact
// list is a `magus diff -o json` away, and this is the section's second cut rather than its first.
const PromptOverlapPathLimit = 6

// The skills a review prompt points a reader's model at.
//
// CHECKED references, not bare strings: MustSkill resolves each against the catalog magus actually
// ships, so renaming a skill breaks the build here rather than leaving a reader pointed at
// something they cannot load. A prompt that names a skill nobody has still renders perfectly,
// which is exactly why the check has to be structural.
//
// Only skills magus SHIPS can appear. A workspace-local skill exists nowhere but its own tree, so
// naming one would send every other magus user looking for a file they do not have - and
// MustSkill refuses it.
var (
	SkillQuery        = agent.MustSkill("magus-query")
	SkillArchitecture = agent.MustSkill("magus-architecture-review")
)

// PromptInput is everything a review prompt is built from. A struct because these arrive from
// four different lookups and three of them are optional, which is exactly the shape that turns
// into an unreadable positional call.
type PromptInput struct {
	// Changeset is the annotated, reading-ordered set of changed files.
	Changeset types.Diff
	// Origin names the branch under review. Empty fields simply do not render.
	Origin types.ReviewOrigin
	// Overlap is every branch magus knows about with its full path list. Prompt narrows it to
	// the intersection with this changeset itself, so a caller passes what it has rather than
	// pre-filtering and getting the rule wrong somewhere else.
	Overlap []types.BranchChange
	// Variant selects the short form or the one that also carries the rationale.
	Variant prompt.Variant
}

// Prompt renders a review prompt for a PERSON TO PASTE into whichever model they use.
//
// magus assembles it and stops there: nothing here calls a model, holds a key, or sends anything
// anywhere. That is the point rather than a limitation - the clipboard is the airgap, and it is
// what keeps the review something the reader wrote.
//
// It carries CONTEXT AND INSTRUCTIONS, not the diff, and it NAMES skills rather than restating
// them. magus already ships the durable half of a review briefing as installed, stamped,
// doctor-checked skills in two lengths; a copy pasted in here would be a second definition free to
// drift from the installed one, and it would spend the reader's context on what their tools
// already loaded. So everything above the last section is the part no skill can know: the facts
// about this particular changeset, which do not exist until magus computes them.
//
// What it does NOT do is draft the review. It asks for findings, and says so out loud, because the
// reviewer's own words are the whole value of a review to the person receiving it.
func Prompt(in PromptInput) string {
	b := prompt.New("Review this change", in.Variant)

	b.Lead(
		"Find what is worth commenting on. I will write the actual review comments myself, so",
		"give me findings - file, line, what is wrong - and flag the ones you are unsure of.",
		"Do not draft review prose or a summary I could paste.",
	)

	b.Section("What this is").
		Field("branch", in.Origin.Branch).
		Field("compared against", in.Changeset.Base).
		Bullet("%d changed file(s)", len(in.Changeset.Files)).
		Field("projects edited directly", strings.Join(in.Changeset.SeedProjects, ", "))
	// The gap between the two counts is the whole "why is docs in my build" question, so the
	// closure is reported whenever it is wider than the set somebody actually edited.
	if n := len(in.Changeset.AffectedProjects); n > len(in.Changeset.SeedProjects) {
		b.Bullet("%d project(s) rebuild as a result", n).
			Because("That is the reverse dependency closure, not the edited set: a project in it",
				"rebuilds because something it depends on changed.")
	}

	b.Section("Read in this order").
		Note("magus ranked these by what they can break, consequence first.").
		Because("Role, blast radius and coverage below are magus's own annotations. They are",
			"evidence, not verdicts - a file magus ranks low can still be the wrong change.").
		Items(promptFiles(in.Changeset.Files), PromptFileLimit,
			"Ask magus for the rest with `magus diff -o json`.")

	b.Section("What magus could not measure").
		Note("Do not read any of these as evidence that there is nothing there.").
		Items(in.Changeset.Notes, 0, "")

	b.Section("Other branches changing these same files").
		Note("A local branch is current; one marked (as of your last fetch) is only that fresh.").
		Because("A collision here is worth flagging before the merge finds it.").
		// No command is named for the remainder, because none reports it. A pointer at something
		// that does not answer the question is worse than admitting the list was cut.
		Items(promptOverlap(in.Changeset, in.Overlap), PromptBranchLimit,
			"Shared files this widely contested are usually the workspace's own config.")

	b.Section("Use what is already installed").
		Note("Load these rather than inferring from the diff alone:").
		Skill(SkillQuery, "what references what, without guessing from a text search").
		Skill(SkillArchitecture, "where code belongs, grounded in the graph").
		Because("A graph answer is checked against declared sources; a text search is a guess.").
		Text("Follow the conventions this workspace documents over generic ones.").
		Text("Before reporting a finding, look for the test that PINS the behavior you are about").
		Text("to call a bug. If you cannot find where a claim is verified, say it is unverified.").
		Because("Roughly one review finding in ten is wrong that way: the code says what it says",
			"on purpose, and the comment beside it usually says why.")

	return b.String()
}

// promptFiles renders each changed file as one line: the path, then the annotations that change
// how it should be read.
//
// Reach and coverage are OMITTED rather than zeroed when unmeasured. "Nothing references this" and
// "nobody looked" are different claims, and only one of them is reassuring.
func promptFiles(files []types.DiffFile) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		var facts []string
		if f.Role != "" {
			facts = append(facts, f.Role)
		}
		if f.Project != "" {
			facts = append(facts, "in "+f.Project)
		}
		if f.Surface == types.DiffSurfacePublic {
			facts = append(facts, "referenced from other projects")
		}
		if f.Coverage != nil {
			facts = append(facts, fmt.Sprintf("%.0f%% covered", f.Coverage.Ratio*100))
		}
		line := "`" + f.Path + "`"
		if len(facts) > 0 {
			line += " - " + strings.Join(facts, "; ")
		}
		out = append(out, line)
	}
	return out
}

// promptOverlap renders each colliding branch as one line: the ref, then the files it shares with
// this changeset.
func promptOverlap(rev types.Diff, branches []types.BranchChange) []string {
	shared := overlapWith(rev, branches)
	out := make([]string, 0, len(shared))
	for _, c := range shared {
		// The freshness is per branch now that local ones are scanned, and only the stale kind
		// is marked: captioning every line would spend the reader's attention on the branches
		// where there is nothing to doubt.
		when := ""
		if !c.Local {
			when = " (as of your last fetch)"
		}
		out = append(out, fmt.Sprintf("`%s`%s: %s", c.Ref, when, promptPaths(c.Paths)))
	}
	return out
}

// promptPaths lists a branch's shared paths, saying how many it did not name.
//
// The remainder is COUNTED rather than dropped, for the reason every other cut in this file is:
// a truncated list that does not admit it reads as the whole answer, and here that would understate
// how contested a file is at exactly the moment the reader is deciding whether to care.
func promptPaths(paths []string) string {
	if len(paths) <= PromptOverlapPathLimit {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, and %d more",
		strings.Join(paths[:PromptOverlapPathLimit], ", "), len(paths)-PromptOverlapPathLimit)
}

// overlapWith narrows each branch to the paths it shares with this changeset, dropping the
// branches that share none.
//
// The narrowing is the whole value. "This branch also changes 1,400 files" is not a fact about the
// change in front of the reader; "this branch also changes the two files you are reading" is. The
// unnarrowed version made a prompt forty times the size of the answer it was carrying.
func overlapWith(rev types.Diff, branches []types.BranchChange) []types.BranchChange {
	mine := make(map[string]struct{}, len(rev.Files))
	for _, f := range rev.Files {
		mine[f.Path] = struct{}{}
	}
	var out []types.BranchChange
	for _, br := range branches {
		var shared []string
		for _, p := range br.Paths {
			if _, ok := mine[p]; ok {
				shared = append(shared, p)
			}
		}
		if len(shared) > 0 {
			out = append(out, types.BranchChange{Ref: br.Ref, Paths: shared, Local: br.Local})
		}
	}
	return out
}
