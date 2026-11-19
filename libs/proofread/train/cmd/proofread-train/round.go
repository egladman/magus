package main

import (
	"context"

	"github.com/egladman/magus/libs/clikit/command"
)

// The files of a round directory. Labelers fill copies of the template as
// labels-a.jsonl and labels-b.jsonl; the adjudicator writes adjudicated.jsonl.
const (
	candidatesFile  = "candidates.jsonl"
	templateFile    = "labels-template.jsonl"
	countsFile      = "counts.json"
	freezeFile      = "freeze.json"
	guidelineFile   = "guideline.md"
	labelsAFile     = "labels-a.jsonl"
	labelsBFile     = "labels-b.jsonl"
	adjudicatedFile = "adjudicated.jsonl"
	scoresFile      = "scores.json"
)

// rulePathspecs are the files whose change can change what a rule fires on:
// the proofread package's sources, less its tests, the case replay and the
// round math, which score no text.
var rulePathspecs = []string{
	":(top,glob)libs/proofread/*.go",
	":(top,exclude,glob)libs/proofread/*_test.go",
	":(top,exclude)libs/proofread/calibrate.go",
	":(top,exclude)libs/proofread/round.go",
}

// roundCommand is `proofread-train calibrate round sample|freeze|score|pool`.
var roundCmd = command.Command{
	Name:     "round",
	Short:    "sample, freeze or score a labeling round, or pool two rounds to decide a deny",
	Children: []command.Command{roundSampleCmd, roundFreezeCmd, roundScoreCmd, roundPoolCmd},
}

// candidate is one firing a labeler judges. Sentence and Context are what a
// labeler reads first; Text is the whole judged text, so no call waits on
// context the packet cut.
type candidate struct {
	ID     string `json:"id"`
	Rule   string `json:"rule"`
	Code   string `json:"code"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Record is the 1-based ordinal of the record in its source file.
	Record  int    `json:"record"`
	Match   string `json:"match"`
	Message string `json:"message"`
	// Line, Column, EndLine and EndColumn place Match in Text as the finding
	// reported them, 0 when it did not.
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	EndColumn int    `json:"end_column,omitempty"`
	Sentence  string `json:"sentence"`
	Context   string `json:"context"`
	// Scope is sentence for a located match, paragraph for a finding about the
	// paragraph a line opens, and text for one about the whole text.
	Scope string `json:"scope"`
	Text  string `json:"text"`
}

type labelRow struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note"`
}

type adjudicatedRow struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Reason      string `json:"reason"`
	Adjudicated *bool  `json:"adjudicated,omitempty"`
}

type sourceSpec struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	// Records counts the records with text, Texts those left after dropping
	// a text the source already held.
	Records    int `json:"records"`
	Texts      int `json:"texts"`
	Duplicates int `json:"duplicates"`
}

type excludeSpec struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	// Strings counts the texts, sentences and contexts it contributed.
	Strings int `json:"strings"`
}

// roundCounts is counts.json: how the sampler drew a round, enough to draw
// it again from the same sources.
type roundCounts struct {
	Round            int                   `json:"round"`
	Seed             int64                 `json:"seed"`
	TotalCandidates  int                   `json:"total_candidates"`
	ToolCommit       string                `json:"tool_commit"`
	RuleFilesDirty   bool                  `json:"rule_files_dirty"`
	ProofreadVersion string                `json:"proofread_version"`
	Rules            []string              `json:"sampled_rules"`
	PerRule          int                   `json:"per_rule"`
	MinPerSource     int                   `json:"min_per_source"`
	MaxPerText       int                   `json:"max_per_text"`
	Field            []string              `json:"field"`
	Sources          []sourceSpec          `json:"sources"`
	Exclude          []excludeSpec         `json:"exclude"`
	TextsScanned     map[string]int        `json:"texts_scanned"`
	Notes            map[string]string     `json:"notes"`
	ByRule           map[string]*ruleCount `json:"rules"`
}

type ruleCount struct {
	Code       string `json:"code"`
	Candidates int    `json:"candidates"`
	Short      bool   `json:"fewer_than_per_rule"`
	// Seen counts every firing; CappedPerText those past -max-per-text in one
	// text; Excluded those whose text, sentence or context an exclude file
	// holds; Duplicates those repeating a fresh firing's sentence or context;
	// Fresh what remains to draw from.
	Seen                int               `json:"firings_seen_all_sources"`
	CappedPerText       int               `json:"capped_per_text"`
	Excluded            int               `json:"excluded_already_used"`
	Duplicates          int               `json:"duplicates"`
	Fresh               int               `json:"fresh_unique_in_pool"`
	BySourceSeen        map[string]int    `json:"by_source_firings"`
	BySourceExcluded    map[string]int    `json:"by_source_excluded"`
	BySourceFresh       map[string]int    `json:"by_source_fresh_unique"`
	BySourceTaken       map[string]int    `json:"by_source_selected"`
	Kinds               []string          `json:"kinds"`
	KindsWithoutASource []string          `json:"kinds_without_a_source"`
	ShippedDecisions    map[string]string `json:"shipped_decisions"`
	Decisions           map[string]string `json:"decisions"`
}

// roundSampleOpts holds the flags of the one command a process runs.
var roundSampleOpts roundSampleFlags

var roundSampleCmd = command.Command{
	Name:   "sample",
	Short:  "draw the firings of the rules over SOURCE files into a round directory",
	Usage:  "proofread-train calibrate round sample [flags] SOURCE...",
	Effect: command.EffectLocalWrite,
	Flags: []command.Flag{
		{Name: "rules", Kind: command.String, Doc: "sample firings of these comma-separated `rules`", Target: &roundSampleOpts.rules},
		{Name: "seed", Kind: command.Int, Default: 0, Doc: "draw with this `seed`, fixed before anyone sees the sample", Target: &roundSampleOpts.seed},
		{Name: "per-rule", Kind: command.Int, Default: 40, Doc: "draw at most `n` firings of each rule", Target: &roundSampleOpts.perRule},
		{Name: "min-per-source", Kind: command.Int, Default: 10, Doc: "give each source with enough fresh firings at least `n`, so its own gate runs", Target: &roundSampleOpts.minPerSource},
		{Name: "max-per-text", Kind: command.Int, Default: 2, Doc: "keep at most `n` firings of a rule from one text", Target: &roundSampleOpts.maxPerText},
		{Name: "decisions", Kind: command.String, Doc: "judge with the decisions table in this `file`; by default every sampled rule is deny", Target: &roundSampleOpts.decisions},
		{Name: "out", Kind: command.String, Doc: "write the round into this `dir`", Target: &roundSampleOpts.out},
		{Name: "round", Kind: command.Int, Default: 0, Doc: "the round `number` the candidate ids carry; by default read from an --out named round<N>", Target: &roundSampleOpts.round},
		{Name: "field", Kind: command.String, Default: "text", Doc: "the comma-separated `fields` joined by newlines into each text", Target: &roundSampleOpts.field},
		{Name: "kind-field", Kind: command.String, Default: "kind", Doc: "the `field` naming each record's kind", Target: &roundSampleOpts.kindField},
		{Name: "kind", Kind: command.String, Doc: "judge a record whose --kind-field is empty as this `kind`", Target: &roundSampleOpts.kind},
		{Name: "exclude", Kind: command.StringList, Doc: "drop firings whose text, sentence or context this candidates.jsonl or case `dir` holds; repeatable", Target: &roundSampleOpts.excludes},
	},
	Run: func(_ context.Context, inv command.Invocation) error {
		return reported(runRoundSample(&roundSampleOpts, inv.Args, inv.Stdout, inv.Stderr))
	},
}

// roundFreezeOpts holds the flags of the one command a process runs.
var roundFreezeOpts struct{ dir, guideline, gateList, purpose string }

var roundFreezeCmd = command.Command{
	Name:   "freeze",
	Short:  "freeze a sampled round with its guideline and gates",
	Effect: command.EffectLocalWrite,
	Flags: []command.Flag{
		{Name: "dir", Kind: command.String, Doc: "freeze the round sampled into this `dir`", Target: &roundFreezeOpts.dir},
		{Name: "guideline", Kind: command.String, Doc: "the guideline `file` labelers judge by", Target: &roundFreezeOpts.guideline},
		{Name: "gates", Kind: command.String, Doc: "comma-separated key=value `gates` over the defaults: z, advise-lower, advise-firings, " +
			"deny-rounds, deny-firings, deny-wrong, agree-per, alpha, source-firings", Target: &roundFreezeOpts.gateList},
		{Name: "purpose", Kind: command.String, Doc: "one line on what the round confirms", Target: &roundFreezeOpts.purpose},
	},
	Run: func(_ context.Context, inv command.Invocation) error {
		return reported(runRoundFreeze(roundFreezeOpts.dir, roundFreezeOpts.guideline, roundFreezeOpts.gateList, roundFreezeOpts.purpose, inv.Args, inv.Stdout, inv.Stderr))
	},
}

// roundPoolOpts holds the flags of the one command a process runs.
var roundPoolOpts struct {
	dirs []string
	out  string
}

var roundPoolCmd = command.Command{
	Name:   "pool",
	Short:  "pool two scored rounds to decide a deny",
	Effect: command.EffectLocalWrite,
	Flags: []command.Flag{
		{Name: "dir", Kind: command.StringList, Doc: "a scored round `dir`, given twice", Target: &roundPoolOpts.dirs},
		{Name: "out", Kind: command.String, Doc: "write the pool to this `file` and print a table, instead of printing the JSON", Target: &roundPoolOpts.out},
	},
	Run: func(_ context.Context, inv command.Invocation) error {
		return reported(runRoundPool(roundPoolOpts.dirs, roundPoolOpts.out, inv.Args, inv.Stdout, inv.Stderr))
	},
}

// roundScoreOpts holds the flags of the one command a process runs.
var roundScoreOpts struct {
	dir, out string
	replay   bool
}

var roundScoreCmd = command.Command{
	Name:   "score",
	Short:  "score a labeled round",
	Effect: command.EffectLocalWrite,
	Flags: []command.Flag{
		{Name: "dir", Kind: command.String, Doc: "score the round labeled in this `dir`", Target: &roundScoreOpts.dir},
		{Name: "out", Kind: command.String, Doc: "write the scores to this `file` (default DIR/scores.json)", Target: &roundScoreOpts.out},
		{Name: "replay", Kind: command.Bool, Default: false, Doc: "score a round whose rule files changed since its freeze, to reproduce its numbers; " +
			"the result decides nothing", Target: &roundScoreOpts.replay},
	},
	Run: func(_ context.Context, inv command.Invocation) error {
		return reported(runRoundScore(roundScoreOpts.dir, roundScoreOpts.out, roundScoreOpts.replay, inv.Args, inv.Stdout, inv.Stderr))
	},
}
