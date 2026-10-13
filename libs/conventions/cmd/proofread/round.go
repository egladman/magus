package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"maps"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus/libs/conventions/proofread"
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
	":(top,glob)libs/conventions/proofread/*.go",
	":(top,exclude,glob)libs/conventions/proofread/*_test.go",
	":(top,exclude)libs/conventions/proofread/calibrate.go",
	":(top,exclude)libs/conventions/proofread/round.go",
}

// runRound runs `proofread calibrate round sample|freeze|score|pool`.
func runRound(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return failure(stderr, errors.New("round needs sample, freeze, score or pool"))
	}

	switch args[0] {
	case "sample":
		return runRoundSample(args[1:], stdout, stderr)
	case "freeze":
		return runRoundFreeze(args[1:], stdout, stderr)
	case "score":
		return runRoundScore(args[1:], stdout, stderr)
	case "pool":
		return runRoundPool(args[1:], stdout, stderr)
	default:
		return failure(stderr, fmt.Errorf("unknown round subcommand %q: want sample, freeze, score or pool", args[0]))
	}
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

type repeated []string

func (r *repeated) String() string { return strings.Join(*r, ",") }

func (r *repeated) Set(v string) error {
	*r = append(*r, v)

	return nil
}

func runRoundSample(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("proofread calibrate round sample", flag.ContinueOnError)
	fset.SetOutput(stderr)
	rulesList := fset.String("rules", "", "sample firings of these comma-separated `rules`")
	seed := fset.Int64("seed", 0, "draw with this `seed`, fixed before anyone sees the sample")
	perRule := fset.Int("per-rule", 40, "draw at most `n` firings of each rule")
	minPerSource := fset.Int("min-per-source", 10, "give each source with enough fresh firings at least `n`, so its own gate runs")
	maxPerText := fset.Int("max-per-text", 2, "keep at most `n` firings of a rule from one text")
	decisionsPath := fset.String("decisions", "", "judge with the decisions table in this `file`; by default every sampled rule is deny")
	outDir := fset.String("out", "", "write the round into this `dir`")
	roundNum := fset.Int("round", 0, "the round `number` the candidate ids carry; by default read from a -out named round<N>")
	field := fset.String("field", "text", "the comma-separated `fields` joined by newlines into each text")
	kindField := fset.String("kind-field", "kind", "the `field` naming each record's kind")
	kindName := fset.String("kind", "", "judge a record whose -kind-field is empty as this `kind`")

	var excludes repeated

	fset.Var(&excludes, "exclude", "drop firings whose text, sentence or context this candidates.jsonl or case `dir` holds; repeatable")

	if err := fset.Parse(args); err != nil {
		return exitError
	}

	if fset.NArg() == 0 {
		return failure(stderr, errors.New("round sample needs at least one SOURCE file"))
	}

	cfg := sampleConfig{
		seed: *seed, perRule: *perRule, minPerSource: *minPerSource, maxPerText: *maxPerText,
		fields: splitFields(*field), kindField: *kindField, outDir: *outDir,
	}

	if err := cfg.resolve(*rulesList, *decisionsPath, *kindName, *roundNum, fset.Args(), excludes); err != nil {
		return failure(stderr, err)
	}

	counts, err := cfg.sample()
	if err != nil {
		return failure(stderr, err)
	}

	fmt.Fprintf(stdout, "wrote %d candidates to %s\n", counts.TotalCandidates, cfg.outDir)

	return writeCountsTable(stdout, counts)
}

type sampleConfig struct {
	seed                              int64
	perRule, minPerSource, maxPerText int
	fields                            []string
	kindField                         string
	outDir                            string

	round     int
	rules     []proofread.Rule
	decisions map[proofread.Rule]proofread.Decision
	kind      proofread.Kind
	sources   []sourceSpec
	excludes  []string
}

func (c *sampleConfig) resolve(rulesList, decisionsPath, kindName string, round int, sources, excludes []string) error {
	if c.outDir == "" {
		return errors.New("round sample needs -out")
	}

	if c.seed == 0 {
		return errors.New("round sample needs -seed, a nonzero seed fixed before the sample is seen")
	}

	if c.perRule < 1 || c.minPerSource < 0 || c.maxPerText < 1 {
		return errors.New("-per-rule and -max-per-text must be at least 1, and -min-per-source at least 0")
	}

	if len(c.fields) == 0 {
		return errors.New("-field names no field")
	}

	if kindName != "" {
		k, err := sampleKind(kindName)
		if err != nil {
			return err
		}

		c.kind = k
	}

	known := proofread.Rules()

	for name := range strings.SplitSeq(rulesList, ",") {
		r := proofread.Rule(strings.TrimSpace(name))
		if r == "" {
			continue
		}

		if !slices.Contains(known, r) {
			return fmt.Errorf("-rules: unknown rule %q", r)
		}

		if !slices.Contains(c.rules, r) {
			c.rules = append(c.rules, r)
		}
	}

	if len(c.rules) == 0 {
		return errors.New("round sample needs -rules")
	}

	if err := c.resolveDecisions(decisionsPath); err != nil {
		return err
	}

	c.round = round
	if c.round == 0 {
		digits, named := strings.CutPrefix(filepath.Base(filepath.Clean(c.outDir)), "round")

		n, err := strconv.Atoi(digits)
		if !named || err != nil || n < 1 {
			return fmt.Errorf("-out %s is not named round<N>, so pass -round", c.outDir)
		}

		c.round = n
	}

	names := map[string]bool{}

	for _, arg := range sources {
		s := parseSource(arg)
		if names[s.Name] {
			return fmt.Errorf("two sources are named %q, so name one with NAME=PATH", s.Name)
		}

		names[s.Name] = true
		c.sources = append(c.sources, s)
	}

	slices.SortFunc(c.sources, func(a, b sourceSpec) int { return strings.Compare(a.Name, b.Name) })
	c.excludes = excludes

	return nil
}

// resolveDecisions reads the -decisions table, or sets every sampled rule to
// deny, which runs a rule that ships off.
func (c *sampleConfig) resolveDecisions(p string) error {
	if p == "" {
		c.decisions = map[proofread.Rule]proofread.Decision{}
		for _, r := range c.rules {
			c.decisions[r] = proofread.DecisionDeny
		}

		return nil
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("read decisions %s: %w", p, err)
	}

	t, err := parseTable(data)
	if err != nil {
		return fmt.Errorf("decisions %s: %w", p, err)
	}

	if len(t.paths) > 0 {
		return fmt.Errorf("decisions %s: a sample's records have no path, so the table cannot scope rules by glob", p)
	}

	house := map[proofread.Rule]bool{}
	for _, doc := range proofread.Catalog() {
		house[doc.Name] = doc.House
	}

	for _, r := range c.rules {
		if !t.turnsOn(r, house[r]) {
			return fmt.Errorf("decisions %s sets %q off, so it cannot be sampled", p, r)
		}
	}

	c.decisions = t.rules

	return nil
}

var sourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// parseSource reads NAME=PATH, or a bare PATH named for its file.
func parseSource(arg string) sourceSpec {
	if name, path, ok := strings.Cut(arg, "="); ok && sourceName.MatchString(name) {
		return sourceSpec{Name: name, Path: path}
	}

	base := filepath.Base(arg)

	return sourceSpec{Name: strings.TrimSuffix(base, filepath.Ext(base)), Path: arg}
}

// sampleText is one deduplicated text of a source, ready to judge.
type sampleText struct {
	source       string
	record       int
	kind         proofread.Kind
	text         string
	threadLength int
}

// firing is one finding of a sampled rule, located in its text.
type firing struct {
	t       *sampleText
	f       proofread.Finding
	scope   string
	sent    string
	context string
	// key is what makes two firings the same for dedupe and exclusion: the
	// sentence of a located match, the whole text otherwise.
	key string
}

func (c *sampleConfig) sample() (*roundCounts, error) {
	if err := refuseResample(c.outDir); err != nil {
		return nil, err
	}

	seen, excludeSpecs, err := loadExcludes(c.excludes)
	if err != nil {
		return nil, err
	}

	var texts []*sampleText

	for i := range c.sources {
		got, err := c.readSource(&c.sources[i])
		if err != nil {
			return nil, err
		}

		texts = append(texts, got...)
	}

	findings := c.judgeAll(texts)

	counts := c.newCounts(excludeSpecs)

	for _, s := range c.sources {
		counts.TextsScanned[s.Name] = s.Texts
	}

	kindsBySource := map[proofread.Kind]bool{}
	for _, t := range texts {
		kindsBySource[t.kind] = true
	}

	var picked []candidate

	for _, r := range c.rules {
		rc := c.newRuleCount(r, kindsBySource)
		counts.ByRule[string(r)] = rc

		pool := c.pool(r, texts, findings, seen, rc)
		taken := c.draw(r, pool, rc)

		for _, f := range taken {
			picked = append(picked, f.candidate())
		}
	}

	rng := rand.New(rand.NewPCG(uint64(c.seed), 0)) //nolint:gosec // a reproducible draw, not a secret
	rng.Shuffle(len(picked), func(i, j int) { picked[i], picked[j] = picked[j], picked[i] })

	for i := range picked {
		picked[i].ID = fmt.Sprintf("r%d-%04d", c.round, i+1)
	}

	counts.TotalCandidates = len(picked)

	if err := writeRound(c.outDir, picked, counts); err != nil {
		return nil, err
	}

	return counts, nil
}

// refuseResample keeps a drawn round from being drawn again: a seed changed
// after its sample was seen is a second look the freeze cannot account for.
func refuseResample(dir string) error {
	for _, name := range []string{freezeFile, candidatesFile, labelsAFile, labelsBFile, adjudicatedFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return fmt.Errorf("%s already holds %s, and a round is drawn once: start a new round directory", dir, name)
		}
	}

	return nil
}

func (c *sampleConfig) newCounts(excludes []excludeSpec) *roundCounts {
	commit, dirty := "unknown", false
	if head, err := gitOutput("rev-parse", "HEAD"); err == nil {
		commit = head
		changed, err := ruleFilesChanged("HEAD")
		dirty = err != nil || len(changed) > 0
	}

	rules := make([]string, len(c.rules))
	for i, r := range c.rules {
		rules[i] = string(r)
	}

	return &roundCounts{
		Round: c.round, Seed: c.seed, ToolCommit: commit, RuleFilesDirty: dirty, ProofreadVersion: proofreadVersion(),
		Rules: rules, PerRule: c.perRule, MinPerSource: c.minPerSource, MaxPerText: c.maxPerText, Field: c.fields,
		Sources: c.sources, Exclude: excludes, TextsScanned: map[string]int{}, ByRule: map[string]*ruleCount{},
		Notes: map[string]string{
			"judging":   "each text judged once with -only the sampled rules and the decisions recorded per rule",
			"dedupe":    "texts deduplicated per source on lowercased, whitespace-collapsed text, before judging",
			"redaction": "emails, tokens, keys and private key blocks replaced with [REDACTED] before judging",
			"exclusion": "a firing is excluded when its text, its sentence or its context equals a text, sentence or context " +
				"of an -exclude file or of the cases proofread carries, compared lowercased with whitespace collapsed",
			"selection": "at most -max-per-text firings of a rule per text; unique on sentence (whole text when unlocated) and on context; " +
				"each source's fresh firings shuffled with PCG(seed, fnv64a(rule)) in source-name order; sources with at least " +
				"-min-per-source fresh firings, largest first, get that many while -per-rule allows; the rest round-robin " +
				"across sources in name order; candidates then shuffled with PCG(seed, 0) and numbered",
		},
	}
}

func (c *sampleConfig) newRuleCount(r proofread.Rule, kinds map[proofread.Kind]bool) *ruleCount {
	rc := &ruleCount{
		BySourceSeen: map[string]int{}, BySourceExcluded: map[string]int{}, BySourceFresh: map[string]int{},
		BySourceTaken: map[string]int{}, Kinds: []string{}, KindsWithoutASource: []string{},
		ShippedDecisions: map[string]string{}, Decisions: map[string]string{},
	}

	for _, doc := range proofread.Catalog() {
		if doc.Name != r {
			continue
		}

		rc.Code = string(doc.Code)

		for _, k := range doc.Kinds {
			rc.Kinds = append(rc.Kinds, string(k))
			rc.ShippedDecisions[string(k)] = string(doc.Decisions[k])

			d := doc.Decisions[k]
			if set, ok := c.decisions[r]; ok {
				d = set
			}

			rc.Decisions[string(k)] = string(d)

			if !kinds[k] {
				rc.KindsWithoutASource = append(rc.KindsWithoutASource, string(k))
			}
		}
	}

	return rc
}

// readSource reads one JSON-lines source, redacting each text and dropping
// one the source already held.
func (c *sampleConfig) readSource(s *sourceSpec) ([]*sampleText, error) {
	f, err := os.Open(s.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()

	var out []*sampleText

	held := map[string]bool{}
	record := 0

	err = readRecords(io.TeeReader(f, h), func(rec map[string]any) {
		record++

		text := joinText(rec, c.fields)
		if text == "" {
			return
		}

		s.Records++

		text = redact(text)

		key := normalize(text)
		if held[key] {
			s.Duplicates++

			return
		}

		held[key] = true
		t := &sampleText{source: s.Name, record: record, text: text, kind: c.kind}

		if v, ok := rec[c.kindField].(string); ok && v != "" {
			t.kind = proofread.Kind(v)
		}

		// compat(until: no round source record carries "anchored"): records sampled before
		// review-inline was a kind mark an inline comment this way.
		if anchored, _ := rec["anchored"].(bool); anchored && t.kind == proofread.KindReviewReply {
			t.kind = proofread.KindReviewInline
		}

		if n, ok := rec["thread_length"].(float64); ok {
			t.threadLength = int(n)
		}

		out = append(out, t)
	})
	if err != nil {
		return nil, fmt.Errorf("source %s: %w", s.Path, err)
	}

	// readRecords stops at EOF, so the hash already covers the whole file.
	s.SHA256 = hex.EncodeToString(h.Sum(nil))
	s.Texts = len(out)

	for _, t := range out {
		if t.kind == "" {
			return nil, fmt.Errorf("source %s: record %d has no %q field and no -kind is given", s.Path, t.record, c.kindField)
		}

		if _, err := sampleKind(string(t.kind)); err != nil {
			return nil, fmt.Errorf("source %s: record %d: %w", s.Path, t.record, err)
		}
	}

	return out, nil
}

// judgeAll judges every text on all CPUs, the findings indexed as texts are.
func (c *sampleConfig) judgeAll(texts []*sampleText) [][]proofread.Finding {
	out := make([][]proofread.Finding, len(texts))
	jobs := make(chan int, 256)

	var wg sync.WaitGroup

	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for i := range jobs {
				out[i] = c.judge(texts[i])
			}
		})
	}

	for i := range texts {
		jobs <- i
	}

	close(jobs)
	wg.Wait()

	return out
}

func (c *sampleConfig) judge(t *sampleText) []proofread.Finding {
	opts := []proofread.Option{
		proofread.WithOnly(c.rules...), proofread.WithDecisions(c.decisions), proofread.WithThreadLength(t.threadLength),
	}
	if t.kind == proofread.KindDocComment {
		return proofread.Judge(proofread.Symbol{Doc: t.text}, opts...)
	}

	return proofread.JudgeText(t.text, t.kind, opts...)
}

// pool returns rule r's fresh firings per source, in record and finding order.
func (c *sampleConfig) pool(r proofread.Rule, texts []*sampleText, findings [][]proofread.Finding,
	seen *seenIndex, rc *ruleCount,
) map[string][]firing {
	out := map[string][]firing{}
	unique := map[string]bool{}

	for i, t := range texts {
		kept := 0

		for _, f := range findings[i] {
			if f.Rule != r {
				continue
			}

			rc.Seen++
			rc.BySourceSeen[t.source]++

			if kept == c.maxPerText {
				rc.CappedPerText++

				continue
			}

			kept++

			fi := locateFiring(t, f)

			if seen.holds(fi) {
				rc.Excluded++
				rc.BySourceExcluded[t.source]++

				continue
			}

			sk, ck := "s:"+fi.key, "c:"+normalize(fi.context)
			if unique[sk] || unique[ck] {
				rc.Duplicates++

				continue
			}

			unique[sk], unique[ck] = true, true
			out[t.source] = append(out[t.source], fi)
		}
	}

	for src, fs := range out {
		rc.BySourceFresh[src] = len(fs)
		rc.Fresh += len(fs)
	}

	return out
}

// draw takes up to perRule firings of r from pool, recording what it took.
func (c *sampleConfig) draw(r proofread.Rule, pool map[string][]firing, rc *ruleCount) []firing {
	h := fnv.New64a()
	h.Write([]byte(r))

	rng := rand.New(rand.NewPCG(uint64(c.seed), h.Sum64())) //nolint:gosec // a reproducible draw, not a secret

	names := slices.Sorted(maps.Keys(pool))
	for _, n := range names {
		fs := pool[n]
		rng.Shuffle(len(fs), func(i, j int) { fs[i], fs[j] = fs[j], fs[i] })
	}

	take := allocate(names, pool, c.perRule, c.minPerSource)

	var out []firing

	for _, n := range names {
		out = append(out, pool[n][:take[n]]...)
		rc.BySourceTaken[n] = take[n]
	}

	rc.Candidates = len(out)
	rc.Short = len(out) < c.perRule

	return out
}

// allocate spreads quota across sources: first min to each source holding at
// least min, largest first, while the quota allows, then one at a time across
// every source in name order until the quota or the firings run out.
func allocate(names []string, pool map[string][]firing, quota, minimum int) map[string]int {
	take := map[string]int{}
	left := quota

	if minimum > 0 {
		eligible := slices.Clone(names)
		eligible = slices.DeleteFunc(eligible, func(n string) bool { return len(pool[n]) < minimum })
		slices.SortStableFunc(eligible, func(a, b string) int { return len(pool[b]) - len(pool[a]) })

		for _, n := range eligible {
			if left < minimum {
				break
			}

			take[n] = minimum
			left -= minimum
		}
	}

	for left > 0 {
		moved := false

		for _, n := range names {
			if left == 0 {
				break
			}

			if take[n] < len(pool[n]) {
				take[n]++
				left--
				moved = true
			}
		}

		if !moved {
			break
		}
	}

	return take
}

func (f firing) candidate() candidate {
	return candidate{
		Rule: string(f.f.Rule), Code: string(f.f.Code), Kind: string(f.t.kind), Source: f.t.source, Record: f.t.record,
		Match: f.f.Match, Message: f.f.Message, Line: f.f.Line, Column: f.f.Column, EndLine: f.f.EndLine, EndColumn: f.f.EndColumn,
		Sentence: f.sent, Context: f.context, Scope: f.scope, Text: f.t.text,
	}
}

// The context a labeler reads: this many bytes each side of the match, or
// of a whole text from its start.
const (
	contextBytes  = 300
	wholeBytes    = 600
	sentenceBytes = 600
)

// locateFiring places f's match in t's text and cuts its sentence and
// context; a finding with no match, or one not found as written, is about
// the paragraph its line opens or the whole text.
func locateFiring(t *sampleText, f proofread.Finding) firing {
	out := firing{t: t, f: f}
	text := t.text

	start, end, ok := locate(text, f)
	if ok {
		s, e := sentenceAround(text, start, end)
		out.scope = "sentence"
		out.sent = clip(text[s:e], sentenceBytes)
		out.key = normalize(text[s:e])

		lo, hi := runeFloor(text, max(0, start-contextBytes)), runeCeil(text, min(len(text), end+contextBytes))
		out.context = ellipsis(lo > 0) + text[lo:start] + "[[" + text[start:end] + "]]" + text[end:hi] + ellipsis(hi < len(text))

		return out
	}

	if f.Line > 0 {
		if s, e, found := blockAtLine(text, f.Line); found {
			out.scope = "paragraph"
			out.sent = clip(text[s:e], sentenceBytes)
			out.key = normalize(text[s:e])
			out.context = "[[" + clip(text[s:e], wholeBytes) + "]]"

			return out
		}
	}

	out.scope = "text"
	out.sent = clip(text, sentenceBytes)
	out.key = normalize(text)
	out.context = "[[" + clip(text, wholeBytes) + "]]"

	return out
}

// locate finds f.Match in text: at the line and column the finding gives,
// else first on its line, else first anywhere.
func locate(text string, f proofread.Finding) (int, int, bool) {
	if f.Match == "" {
		return 0, 0, false
	}

	if f.Line > 0 {
		if ls, le, found := lineSpan(text, f.Line); found {
			if f.Column > 0 {
				s := ls + f.Column - 1
				if s+len(f.Match) <= len(text) && text[s:s+len(f.Match)] == f.Match {
					return s, s + len(f.Match), true
				}
			}

			if i := strings.Index(text[ls:le], f.Match); i >= 0 {
				return ls + i, ls + i + len(f.Match), true
			}
		}
	}

	if i := strings.Index(text, f.Match); i >= 0 {
		return i, i + len(f.Match), true
	}

	return 0, 0, false
}

// lineSpan is the byte span of the 1-based line n of text, its newline
// excluded.
func lineSpan(text string, n int) (int, int, bool) {
	start := 0

	for range n - 1 {
		i := strings.IndexByte(text[start:], '\n')
		if i < 0 {
			return 0, 0, false
		}

		start += i + 1
	}

	end := len(text)
	if i := strings.IndexByte(text[start:], '\n'); i >= 0 {
		end = start + i
	}

	return start, end, true
}

// sentenceAround is the span of the sentences [start, end) overlaps, the
// same boundaries sentenceSpans cuts, so a firing's sentence compares equal
// to the same sentence split out of an exclude file.
func sentenceAround(text string, start, end int) (int, int) {
	lo, hi := -1, -1

	for _, sp := range sentenceSpans(text) {
		if sp[1] <= start || sp[0] >= end {
			continue
		}

		if lo < 0 {
			lo = sp[0]
		}

		hi = sp[1]
	}

	if lo < 0 {
		return start, end
	}

	return min(lo, start), max(hi, end)
}

// blockSpans cuts text into blocks: runs of non-blank lines, where a list
// item or a heading starts a new block and a heading is a block of its own.
func blockSpans(text string) [][2]int {
	var out [][2]int

	open := -1
	closeAt := func(end int) {
		if open >= 0 {
			out = append(out, [2]int{open, end})
			open = -1
		}
	}

	pos := 0
	for pos <= len(text) {
		end := len(text)
		if i := strings.IndexByte(text[pos:], '\n'); i >= 0 {
			end = pos + i
		}

		line := strings.TrimSpace(text[pos:end])

		switch {
		case line == "":
			closeAt(pos)
		case heading.MatchString(line):
			closeAt(pos)
			out = append(out, [2]int{pos, end})
		case startsListItem(line):
			closeAt(pos)

			open = pos
		case open < 0:
			open = pos
		}

		if end == len(text) {
			closeAt(end)

			break
		}

		pos = end + 1
	}

	return out
}

var (
	listItem = regexp.MustCompile(`^([-*+]|\d+[.)])\s`)
	heading  = regexp.MustCompile(`^#{1,6}(\s|$)`)
)

func startsListItem(line string) bool { return listItem.MatchString(line) }

// blockAtLine is the block that holds the 1-based line n.
func blockAtLine(text string, n int) (int, int, bool) {
	ls, _, ok := lineSpan(text, n)
	if !ok {
		return 0, 0, false
	}

	for _, b := range blockSpans(text) {
		if ls >= b[0] && ls < b[1] || ls == b[0] {
			return b[0], b[1], true
		}
	}

	return 0, 0, false
}

// sentenceSpans cuts each block at a '.', '!' or '?' that is followed by
// whitespace or ends the block, trimming each sentence's whitespace.
func sentenceSpans(text string) [][2]int {
	var out [][2]int

	add := func(s, e int) {
		for s < e && isSpace(text[s]) {
			s++
		}

		for e > s && isSpace(text[e-1]) {
			e--
		}

		if s < e {
			out = append(out, [2]int{s, e})
		}
	}

	for _, b := range blockSpans(text) {
		start := b[0]

		for i := b[0]; i < b[1]; i++ {
			if strings.IndexByte(".!?", text[i]) >= 0 && (i+1 == b[1] || isSpace(text[i+1])) {
				add(start, i+1)
				start = i + 1
			}
		}

		add(start, b[1])
	}

	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func runeFloor(s string, i int) int {
	for i > 0 && i < len(s) && !isRuneStart(s[i]) {
		i--
	}

	return i
}

func runeCeil(s string, i int) int {
	for i < len(s) && !isRuneStart(s[i]) {
		i++
	}

	return i
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// clip cuts s to at most n bytes on a rune boundary, marking a cut with "...".
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:runeFloor(s, n)] + "..."
}

func ellipsis(cut bool) string {
	if cut {
		return "..."
	}

	return ""
}

// normalize is how texts compare for dedupe and exclusion: lowercased, the
// context markers and cut marks dropped, whitespace collapsed.
func normalize(s string) string {
	s = strings.NewReplacer("[[", "", "]]", "").Replace(s)
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(strings.TrimPrefix(s, "..."), "...")

	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// seenIndex holds what earlier rounds and the committed cases already
// showed someone: whole strings, and the sentences cut from them.
type seenIndex struct {
	wholes, sentences map[string]bool
}

func (x *seenIndex) add(s string) int {
	s = strings.NewReplacer("[[", "", "]]", "").Replace(s)

	n := normalize(s)
	if n == "" {
		return 0
	}

	added := 1
	x.wholes[n] = true

	for _, sp := range sentenceSpans(s) {
		if k := normalize(s[sp[0]:sp[1]]); k != "" {
			x.sentences[k] = true
			added++
		}
	}

	return added
}

func (x *seenIndex) holds(f firing) bool {
	if x.wholes[normalize(f.t.text)] || x.wholes[normalize(f.context)] {
		return true
	}

	return f.scope != "text" && (x.sentences[f.key] || x.wholes[f.key])
}

// loadExcludes indexes the cases proofread carries and each exclude path: a
// candidates.jsonl of an earlier round, or a directory of case files.
func loadExcludes(paths []string) (*seenIndex, []excludeSpec, error) {
	x := &seenIndex{wholes: map[string]bool{}, sentences: map[string]bool{}}

	cases, err := proofread.LoadCases(proofread.LabeledCases())
	if err != nil {
		return nil, nil, fmt.Errorf("load the cases proofread carries: %w", err)
	}

	carried := excludeSpec{Path: "(the cases proofread carries)"}
	for _, c := range cases {
		carried.Strings += x.add(c.Text)
	}

	specs := []excludeSpec{carried}

	for _, p := range paths {
		spec, err := x.addPath(p)
		if err != nil {
			return nil, nil, err
		}

		specs = append(specs, spec)
	}

	return x, specs, nil
}

func (x *seenIndex) addPath(p string) (excludeSpec, error) {
	spec := excludeSpec{Path: p}

	info, err := os.Stat(p)
	if err != nil {
		return spec, err
	}

	if info.IsDir() {
		cases, err := proofread.LoadCases(os.DirFS(p))
		if err != nil {
			return spec, fmt.Errorf("exclude %s: %w", p, err)
		}

		for _, c := range cases {
			spec.Strings += x.add(c.Text)
		}

		return spec, nil
	}

	if filepath.Ext(p) != ".jsonl" {
		return spec, fmt.Errorf("exclude %s: want a candidates .jsonl file or a directory of case files", p)
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return spec, err
	}

	spec.SHA256 = sha256Hex(data)

	err = readRecords(bytes.NewReader(data), func(rec map[string]any) {
		for _, k := range []string{"text", "sentence", "context"} {
			if v, ok := rec[k].(string); ok {
				spec.Strings += x.add(v)
			}
		}
	})
	if err != nil {
		return spec, fmt.Errorf("exclude %s: %w", p, err)
	}

	return spec, nil
}

// secrets are the shapes redacted before judging: what looks like an email,
// a token, a key or a private key block. A git hash is left alone, since a
// commit named in a sentence is evidence a rule reads.
var secrets = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`),
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
}

// assignedSecret is a long value after a word that names a secret; the word
// stays, so the sentence still reads.
var assignedSecret = regexp.MustCompile(`(?i)\b((?:bearer|token|api[_-]?key|secret|password)(?:\s*[:=]\s*|\s+)["']?)[A-Za-z0-9._~+/-]{16,}=*`)

// mixedSecret is a long run of letters and digits in both cases with several
// digits, the shape of a generated key; a hex hash, lowercase only, does not
// qualify.
var mixedSecret = regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}\b`)

const redacted = "[REDACTED]"

func redact(text string) string {
	for _, re := range secrets {
		text = re.ReplaceAllString(text, redacted)
	}

	text = assignedSecret.ReplaceAllString(text, "${1}"+redacted)

	return mixedSecret.ReplaceAllStringFunc(text, func(m string) string {
		digits := 0

		for _, r := range m {
			if r >= '0' && r <= '9' {
				digits++
			}
		}

		if digits >= 3 && strings.ContainsAny(m, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(m, "abcdefghijklmnopqrstuvwxyz") {
			return redacted
		}

		return m
	})
}

func writeRound(dir string, cands []candidate, counts *roundCounts) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var cb, tb bytes.Buffer

	for _, c := range cands {
		if err := json.NewEncoder(&cb).Encode(c); err != nil {
			return err
		}

		if err := json.NewEncoder(&tb).Encode(labelRow{ID: c.ID}); err != nil {
			return err
		}
	}

	if err := os.WriteFile(filepath.Join(dir, candidatesFile), cb.Bytes(), 0o600); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, templateFile), tb.Bytes(), 0o600); err != nil {
		return err
	}

	return writeJSONFile(filepath.Join(dir, countsFile), counts)
}

func writeJSONFile(p string, v any) error {
	data, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}

	return os.WriteFile(p, append(data, '\n'), 0o600)
}

func writeCountsTable(w io.Writer, counts *roundCounts) int {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tSEEN\tCAPPED\tEXCLUDED\tDUPLICATE\tFRESH\tTAKEN\tBY_SOURCE")

	for _, r := range counts.Rules {
		rc := counts.ByRule[r]

		var by []string
		for _, s := range slices.Sorted(maps.Keys(rc.BySourceTaken)) {
			by = append(by, fmt.Sprintf("%s=%d", s, rc.BySourceTaken[s]))
		}

		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", r, rc.Seen, rc.CappedPerText, rc.Excluded, rc.Duplicates, rc.Fresh,
			rc.Candidates, strings.Join(by, " "))
	}

	if err := tw.Flush(); err != nil {
		return exitError
	}

	return exitOK
}

// gates are the promotion bars a freeze fixes before anyone labels.
type gates struct {
	Advise    adviseGate    `json:"advise"`
	Deny      denyGate      `json:"deny"`
	Agreement agreementGate `json:"agreement"`
	Source    sourceGate    `json:"source"`
}

type adviseGate struct {
	WilsonZ    float64 `json:"wilson_z"`
	LowerBound float64 `json:"lower_bound"`
	MinFirings int     `json:"min_firings"`
}

// denyGate is checked across rounds, never by one: Rounds frozen rounds on
// different sources, pooled to PooledFirings with at most MaxWrong wrong.
type denyGate struct {
	Rounds        int `json:"rounds"`
	PooledFirings int `json:"pooled_firings"`
	MaxWrong      int `json:"max_wrong"`
}

// agreementGate passes a rule whose disagreements are at most n/PerDisagreement
// of its n firings, or whose alpha reaches Alpha, and whose every disagreement
// carries a note from both labelers.
type agreementGate struct {
	PerDisagreement int     `json:"firings_per_disagreement"`
	Alpha           float64 `json:"alpha"`
}

// sourceGate holds the worst source with at least MinFirings to the advise
// lower bound.
type sourceGate struct {
	MinFirings int `json:"min_firings"`
}

func defaultGates() gates {
	return gates{
		Advise:    adviseGate{WilsonZ: 1.96, LowerBound: 0.70, MinFirings: 30},
		Deny:      denyGate{Rounds: 2, PooledFirings: 60, MaxWrong: 1},
		Agreement: agreementGate{PerDisagreement: 10, Alpha: 0.80},
		Source:    sourceGate{MinFirings: 10},
	}
}

// parseGates sets the gates key=value, comma-separated, over the defaults.
func parseGates(list string) (gates, error) {
	g := defaultGates()

	setters := map[string]func(string) error{
		"z":              floatInto(&g.Advise.WilsonZ),
		"advise-lower":   floatInto(&g.Advise.LowerBound),
		"advise-firings": intInto(&g.Advise.MinFirings),
		"deny-rounds":    intInto(&g.Deny.Rounds),
		"deny-firings":   intInto(&g.Deny.PooledFirings),
		"deny-wrong":     intInto(&g.Deny.MaxWrong),
		"agree-per":      intInto(&g.Agreement.PerDisagreement),
		"alpha":          floatInto(&g.Agreement.Alpha),
		"source-firings": intInto(&g.Source.MinFirings),
	}

	for item := range strings.SplitSeq(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		key, value, ok := strings.Cut(item, "=")

		set, known := setters[key]
		if !ok || !known {
			return g, fmt.Errorf("-gates %q: want key=value with key one of %s", item, strings.Join(slices.Sorted(maps.Keys(setters)), ", "))
		}

		if err := set(value); err != nil {
			return g, fmt.Errorf("-gates %s: %w", key, err)
		}
	}

	if g.Agreement.PerDisagreement < 1 || g.Advise.WilsonZ <= 0 {
		return g, errors.New("-gates: agree-per must be at least 1 and z positive")
	}

	return g, nil
}

func floatInto(p *float64) func(string) error {
	return func(s string) error {
		v, err := strconv.ParseFloat(s, 64)
		*p = v

		return err
	}
}

func intInto(p *int) func(string) error {
	return func(s string) error {
		v, err := strconv.Atoi(s)
		*p = v

		return err
	}
}

type digest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// freezeRecord is freeze.json: what a round committed to before anyone
// labeled it. A rule file changed after RuleCommit voids the round.
type freezeRecord struct {
	Round      int      `json:"round"`
	Frozen     string   `json:"frozen"`
	Purpose    string   `json:"purpose,omitempty"`
	RuleCommit string   `json:"rule_commit"`
	RulePaths  []string `json:"rule_paths"`
	// ProofreadVersion is build info and informative only: a cached build
	// output keeps the stamp of the checkout that built it. ProofreadSHA256 and
	// RuleCommit identify the sampler. Records frozen before the field existed
	// lack it.
	ProofreadVersion string         `json:"proofread_version"`
	ProofreadSHA256  string         `json:"proofread_sha256,omitempty"`
	SamplerSeed      int64          `json:"sampler_seed"`
	Rules            []string       `json:"rules"`
	Gates            gates          `json:"gates"`
	Guideline        *digest        `json:"guideline"`
	Sources          []sourceSpec   `json:"sources"`
	Exclude          []excludeSpec  `json:"exclude"`
	ShortOfMin       map[string]int `json:"short_of_min"`
	VoidIf           string         `json:"void_if"`
}

func runRoundFreeze(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("proofread calibrate round freeze", flag.ContinueOnError)
	fset.SetOutput(stderr)
	dir := fset.String("dir", "", "freeze the round sampled into this `dir`")
	guideline := fset.String("guideline", "", "the guideline `file` labelers judge by")
	gateList := fset.String("gates", "", "comma-separated key=value `gates` over the defaults: z, advise-lower, advise-firings, "+
		"deny-rounds, deny-firings, deny-wrong, agree-per, alpha, source-firings")
	purpose := fset.String("purpose", "", "one line on what the round confirms")

	if err := fset.Parse(args); err != nil {
		return exitError
	}

	if *dir == "" || *guideline == "" || fset.NArg() > 0 {
		return failure(stderr, errors.New("round freeze takes -dir DIR -guideline FILE and no operand"))
	}

	g, err := parseGates(*gateList)
	if err != nil {
		return failure(stderr, err)
	}

	rec, err := freeze(*dir, *guideline, *purpose, g)
	if err != nil {
		return failure(stderr, err)
	}

	fmt.Fprintf(stdout, "froze round %d at %s: %s\n", rec.Round, rec.RuleCommit, strings.Join(rec.Rules, ", "))

	return exitOK
}

func freeze(dir, guidelinePath, purpose string, g gates) (*freezeRecord, error) {
	for _, name := range []string{freezeFile, labelsAFile, labelsBFile, adjudicatedFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return nil, fmt.Errorf("%s already holds %s, and a round is frozen once, before anyone labels it", dir, name)
		}
	}

	var counts roundCounts
	if err := readJSONFile(filepath.Join(dir, countsFile), &counts); err != nil {
		return nil, fmt.Errorf("run round sample first: %w", err)
	}

	head, err := gitOutput("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}

	if changed, err := ruleFilesChanged("HEAD"); err != nil {
		return nil, err
	} else if len(changed) > 0 {
		return nil, fmt.Errorf("the rule files are not committed: %s", strings.Join(changed, ", "))
	}

	if counts.RuleFilesDirty || counts.ToolCommit == "unknown" {
		return nil, errors.New("the sample was drawn from rule files no commit holds, so draw it again from a clean tree")
	}

	if moved, err := gitOutput(append([]string{"diff", "--name-only", counts.ToolCommit, "HEAD", "--"}, rulePathspecs...)...); err != nil {
		return nil, err
	} else if moved != "" {
		return nil, fmt.Errorf("the rule files changed between the sample at %s and HEAD: %s",
			counts.ToolCommit, strings.Join(strings.Fields(moved), ", "))
	}

	for _, s := range counts.Sources {
		data, err := os.ReadFile(s.Path)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", s.Name, err)
		}

		if sum := sha256Hex(data); sum != s.SHA256 {
			return nil, fmt.Errorf("source %s changed since the sample: sha256 %s, sampled %s", s.Path, sum, s.SHA256)
		}
	}

	guideline, err := os.ReadFile(guidelinePath)
	if err != nil {
		return nil, err
	}

	if err := copyGuideline(dir, guidelinePath, guideline); err != nil {
		return nil, err
	}

	short := map[string]int{}

	for _, r := range counts.Rules {
		if n := counts.ByRule[r].Candidates; n < g.Advise.MinFirings {
			short[r] = n
		}
	}

	binarySum, err := executableSHA256()
	if err != nil {
		return nil, err
	}

	rec := &freezeRecord{
		Round: counts.Round, Frozen: time.Now().UTC().Format(time.DateOnly), Purpose: purpose, RuleCommit: head,
		RulePaths: rulePathspecs, ProofreadVersion: proofreadVersion(), ProofreadSHA256: binarySum,
		SamplerSeed: counts.Seed, Rules: counts.Rules,
		Gates: g, Guideline: &digest{Path: guidelinePath, SHA256: sha256Hex(guideline)}, Sources: counts.Sources,
		Exclude: counts.Exclude, ShortOfMin: short,
		VoidIf: "any rule file changes after rule_commit before the round is scored, or the guideline changes after the freeze",
	}

	return rec, writeJSONFile(filepath.Join(dir, freezeFile), rec)
}

// copyGuideline puts the guideline beside the candidates, where labelers
// read it, unless it is already that file.
func copyGuideline(dir, src string, data []byte) error {
	dst := filepath.Join(dir, guidelineFile)

	a, errA := filepath.Abs(src)
	b, errB := filepath.Abs(dst)

	if errA == nil && errB == nil && a == b {
		return nil
	}

	return os.WriteFile(dst, data, 0o600)
}

// The labels a labeler may give. UNSURE counts as WRONG in the precision.
const (
	labelCorrect = "CORRECT"
	labelWrong   = "WRONG"
	labelUnsure  = "UNSURE"
)

// tally is one rule's or one source's score, the shape scores.json keeps per
// rule and per source.
type tally struct {
	N             int      `json:"n"`
	Correct       int      `json:"correct"`
	Precision     *float64 `json:"precision"`
	WilsonLB      *float64 `json:"wilson_lb"`
	Disagreements int      `json:"disagreements"`
	// Unnoted counts disagreements one labeler left without a note.
	Unnoted       int      `json:"unnoted_disagreements"`
	UnnotedIDs    []string `json:"unnoted_ids,omitempty"`
	RawAgreement  *float64 `json:"raw_agreement"`
	Alpha         *float64 `json:"alpha"`
	PrecisionGate bool     `json:"precision_gate"`
	AgreementGate bool     `json:"agreement_gate"`
}

type worstSource struct {
	Source   string  `json:"source"`
	N        int     `json:"n"`
	WilsonLB float64 `json:"wilson_lb"`
	Pass     bool    `json:"pass"`
}

type ruleScore struct {
	tally

	BySource    map[string]tally `json:"by_source"`
	WorstSource *worstSource     `json:"worst_source"`
	AdviseGate  bool             `json:"advise_gate"`
	SpotCheck   []string         `json:"spot_check"`
}

type scoresRecord struct {
	Round      int                   `json:"round"`
	Seed       int64                 `json:"seed"`
	RuleCommit string                `json:"rule_commit"`
	Replay     bool                  `json:"replay,omitempty"`
	Gates      gates                 `json:"gates"`
	Method     map[string]string     `json:"method"`
	Rules      map[string]*ruleScore `json:"rules"`
}

func runRoundScore(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("proofread calibrate round score", flag.ContinueOnError)
	fset.SetOutput(stderr)
	dir := fset.String("dir", "", "score the round labeled in this `dir`")
	out := fset.String("out", "", "write the scores to this `file` (default DIR/scores.json)")
	replay := fset.Bool("replay", false, "score a round whose rule files changed since its freeze, to reproduce its numbers; "+
		"the result decides nothing")

	if err := fset.Parse(args); err != nil {
		return exitError
	}

	if *dir == "" || fset.NArg() > 0 {
		return failure(stderr, errors.New("round score takes -dir DIR and no operand"))
	}

	rec, err := scoreRound(*dir, *replay)
	if err != nil {
		return failure(stderr, err)
	}

	path := *out
	if path == "" {
		path = filepath.Join(*dir, scoresFile)
	}

	if err := writeJSONFile(path, rec); err != nil {
		return failure(stderr, err)
	}

	if err := writeScoresTable(stdout, rec); err != nil {
		return failure(stderr, err)
	}

	return exitOK
}

func scoreRound(dir string, replay bool) (*scoresRecord, error) {
	fr, err := readFreeze(filepath.Join(dir, freezeFile))
	if err != nil {
		return nil, err
	}

	if !replay {
		if err := checkFrozen(dir, fr); err != nil {
			return nil, err
		}
	}

	cands, err := readJSONLines[candidate](filepath.Join(dir, candidatesFile))
	if err != nil {
		return nil, err
	}

	a, err := readLabels(filepath.Join(dir, labelsAFile), cands)
	if err != nil {
		return nil, err
	}

	b, err := readLabels(filepath.Join(dir, labelsBFile), cands)
	if err != nil {
		return nil, err
	}

	final, err := finalLabels(filepath.Join(dir, adjudicatedFile), cands, a, b)
	if err != nil {
		return nil, err
	}

	rec := &scoresRecord{
		Round: fr.Round, Seed: fr.SamplerSeed, RuleCommit: fr.RuleCommit, Replay: replay, Gates: fr.Gates,
		Rules: map[string]*ruleScore{}, Method: scoreMethod(fr.Gates),
	}

	rules := slices.Clone(fr.Rules)

	for _, c := range cands {
		if !slices.Contains(rules, c.Rule) {
			rules = append(rules, c.Rule)
		}
	}

	for _, r := range rules {
		rec.Rules[r] = scoreRule(r, cands, a, b, final, fr)
	}

	return rec, nil
}

// checkFrozen refuses a round the freeze no longer describes: rule files
// changed since its rule commit, or a guideline changed since the freeze.
func checkFrozen(dir string, fr *freezeRecord) error {
	changed, err := ruleFilesChanged(fr.RuleCommit)
	if err != nil {
		return err
	}

	if len(changed) > 0 {
		return fmt.Errorf("round %d is void, since the rule files changed after its rule commit %s (%s), "+
			"and -replay only reproduces its numbers", fr.Round, fr.RuleCommit, strings.Join(changed, ", "))
	}

	if fr.Guideline == nil {
		return nil
	}

	data, err := os.ReadFile(filepath.Join(dir, guidelineFile))
	if err != nil {
		return err
	}

	if sum := sha256Hex(data); sum != fr.Guideline.SHA256 {
		return fmt.Errorf("round %d is void: %s changed since the freeze (sha256 %s, frozen %s)",
			fr.Round, guidelineFile, sum, fr.Guideline.SHA256)
	}

	return nil
}

func scoreMethod(g gates) map[string]string {
	return map[string]string{
		"final_label": "the adjudicated label where adjudicated.jsonl has one, else the label both labelers gave; UNSURE counts as WRONG",
		"wilson":      fmt.Sprintf("Wilson score interval lower bound, z=%g, on final CORRECT over n", g.Advise.WilsonZ),
		"alpha":       "Krippendorff alpha, nominal, labelers A and B, null when only one label occurs",
		"agreement_gate": fmt.Sprintf("no disagreement left without a note by either labeler, and disagreements <= n/%d or alpha >= %.2f, and n > 0",
			g.Agreement.PerDisagreement, g.Agreement.Alpha),
		"precision_gate": fmt.Sprintf("Wilson lower bound >= %.2f over >= %d firings", g.Advise.LowerBound, g.Advise.MinFirings),
		"advise_gate": fmt.Sprintf("precision and agreement gates pooled, and the worst source with >= %d firings also at "+
			"lower bound >= %.2f; no source reaching %d fails the gate", g.Source.MinFirings, g.Advise.LowerBound, g.Source.MinFirings),
		"deny_gate": fmt.Sprintf("not decided by one round: %d frozen rounds on different sources, pooled to %d firings, "+
			"at most %d wrong", g.Deny.Rounds, g.Deny.PooledFirings, g.Deny.MaxWrong),
		"spot_check": "per rule passing advise: 10 ids drawn from the sorted agreed-CORRECT ids with PCG(seed+1, fnv64a(rule))",
	}
}

func scoreRule(rule string, cands []candidate, a, b map[string]labelRow, final map[string]string, fr *freezeRecord) *ruleScore {
	g := fr.Gates
	bySource := map[string][]candidate{}

	var mine []candidate

	for _, c := range cands {
		if c.Rule == rule {
			mine = append(mine, c)
			bySource[c.Source] = append(bySource[c.Source], c)
		}
	}

	rs := &ruleScore{tally: tallyCandidates(mine, a, b, final, g), BySource: map[string]tally{}, SpotCheck: []string{}}

	for src, cs := range bySource {
		t := tallyCandidates(cs, a, b, final, g)
		rs.BySource[src] = t

		if t.N < g.Source.MinFirings || t.WilsonLB == nil {
			continue
		}

		if rs.WorstSource == nil || *t.WilsonLB < rs.WorstSource.WilsonLB ||
			*t.WilsonLB == rs.WorstSource.WilsonLB && src < rs.WorstSource.Source {
			rs.WorstSource = &worstSource{Source: src, N: t.N, WilsonLB: *t.WilsonLB, Pass: *t.WilsonLB >= g.Advise.LowerBound}
		}
	}

	rs.AdviseGate = rs.PrecisionGate && rs.AgreementGate && rs.WorstSource != nil && rs.WorstSource.Pass

	if rs.AdviseGate {
		rs.SpotCheck = spotCheck(rule, mine, a, b, final, fr.SamplerSeed)
	}

	return rs
}

func tallyCandidates(cs []candidate, a, b map[string]labelRow, final map[string]string, g gates) tally {
	t := tally{N: len(cs)}
	units := make([][]string, 0, len(cs))

	for _, c := range cs {
		if final[c.ID] == labelCorrect {
			t.Correct++
		}

		la, lb := a[c.ID], b[c.ID]
		units = append(units, []string{la.Label, lb.Label})

		if la.Label != lb.Label {
			t.Disagreements++

			if strings.TrimSpace(la.Note) == "" || strings.TrimSpace(lb.Note) == "" {
				t.Unnoted++
				t.UnnotedIDs = append(t.UnnotedIDs, c.ID)
			}
		}
	}

	if t.N == 0 {
		return t
	}

	t.Precision = rounded(float64(t.Correct) / float64(t.N))
	t.WilsonLB = rounded(proofread.WilsonLower(t.Correct, t.N, g.Advise.WilsonZ))
	t.RawAgreement = rounded(float64(t.N-t.Disagreements) / float64(t.N))

	if alpha, ok := proofread.NominalAlpha(units); ok {
		t.Alpha = rounded(alpha)
	}

	lower := proofread.WilsonLower(t.Correct, t.N, g.Advise.WilsonZ)
	t.PrecisionGate = t.N >= g.Advise.MinFirings && lower >= g.Advise.LowerBound
	t.AgreementGate = t.Unnoted == 0 && (t.Disagreements*g.Agreement.PerDisagreement <= t.N ||
		t.Alpha != nil && *t.Alpha >= g.Agreement.Alpha)

	return t
}

// spotCheck draws the agreed-CORRECT labels a person audits before a rule is
// promoted.
func spotCheck(rule string, cs []candidate, a, b map[string]labelRow, final map[string]string, seed int64) []string {
	var agreed []string

	for _, c := range cs {
		if a[c.ID].Label == labelCorrect && b[c.ID].Label == labelCorrect && final[c.ID] == labelCorrect {
			agreed = append(agreed, c.ID)
		}
	}

	slices.Sort(agreed)

	h := fnv.New64a()
	h.Write([]byte(rule))

	rng := rand.New(rand.NewPCG(uint64(seed+1), h.Sum64())) //nolint:gosec // a reproducible draw, not a secret
	rng.Shuffle(len(agreed), func(i, j int) { agreed[i], agreed[j] = agreed[j], agreed[i] })

	return agreed[:min(10, len(agreed))]
}

func rounded(v float64) *float64 {
	r := math.Round(v*1e4) / 1e4

	return &r
}

// readLabels reads one labeler's sheet: one row per candidate, a known label
// on each, in any case.
func readLabels(p string, cands []candidate) (map[string]labelRow, error) {
	rows, err := readJSONLines[labelRow](p)
	if err != nil {
		return nil, err
	}

	ids := candidateIDs(cands)
	out := map[string]labelRow{}

	for _, r := range rows {
		r.Label = strings.ToUpper(strings.TrimSpace(r.Label))

		switch {
		case !ids[r.ID]:
			return nil, fmt.Errorf("%s: id %q is no candidate", p, r.ID)
		case out[r.ID].ID != "":
			return nil, fmt.Errorf("%s: id %q is labeled twice", p, r.ID)
		case r.Label != labelCorrect && r.Label != labelWrong && r.Label != labelUnsure:
			return nil, fmt.Errorf("%s: id %q has label %q, want CORRECT, WRONG or UNSURE", p, r.ID, r.Label)
		}

		out[r.ID] = r
	}

	var missing []string

	for _, c := range cands {
		if _, ok := out[c.ID]; !ok {
			missing = append(missing, c.ID)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("%s leaves %d candidates unlabeled: %s", p, len(missing), strings.Join(missing, ", "))
	}

	return out, nil
}

// finalLabels is each candidate's label: the adjudicated one, else the one
// both labelers agree on. A disagreement nobody adjudicated is an error.
func finalLabels(p string, cands []candidate, a, b map[string]labelRow) (map[string]string, error) {
	adjudicated := map[string]string{}

	if _, err := os.Stat(p); err == nil {
		rows, err := readJSONLines[adjudicatedRow](p)
		if err != nil {
			return nil, err
		}

		ids := candidateIDs(cands)

		for _, r := range rows {
			label := strings.ToUpper(strings.TrimSpace(r.Label))

			switch {
			case !ids[r.ID]:
				return nil, fmt.Errorf("%s: id %q is no candidate", p, r.ID)
			case label != labelCorrect && label != labelWrong && label != labelUnsure:
				return nil, fmt.Errorf("%s: id %q has label %q, want CORRECT, WRONG or UNSURE", p, r.ID, r.Label)
			}

			adjudicated[r.ID] = label
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	out := map[string]string{}

	var open []string

	for _, c := range cands {
		label, ok := adjudicated[c.ID]

		switch {
		case ok:
		case a[c.ID].Label == b[c.ID].Label:
			label = a[c.ID].Label
		default:
			open = append(open, c.ID)
		}

		if label == labelUnsure {
			label = labelWrong
		}

		out[c.ID] = label
	}

	if len(open) > 0 {
		return nil, fmt.Errorf("%d disagreements have no row in %s: %s", len(open), p, strings.Join(open, ", "))
	}

	return out, nil
}

func candidateIDs(cands []candidate) map[string]bool {
	ids := make(map[string]bool, len(cands))
	for _, c := range cands {
		ids[c.ID] = true
	}

	return ids
}

// readFreeze reads freeze.json. Gates written as prose take the defaults.
func readFreeze(p string) (*freezeRecord, error) {
	var raw struct {
		freezeRecord

		Gates map[string]json.RawMessage `json:"gates"`
	}

	if err := readJSONFile(p, &raw); err != nil {
		return nil, fmt.Errorf("a round is scored only once frozen: %w", err)
	}

	fr := raw.freezeRecord
	fr.Gates = defaultGates()

	// compat(until: no round directory holds a freeze.json whose deny, agreement
	// or report gate is a prose string; round 4's, written before freeze
	// existed, is the one known): a gate that is not an object keeps its default.
	for key, target := range map[string]any{
		"advise": &fr.Gates.Advise, "deny": &fr.Gates.Deny, "agreement": &fr.Gates.Agreement, "source": &fr.Gates.Source,
	} {
		if v, ok := raw.Gates[key]; ok && len(v) > 0 && v[0] == '{' {
			if err := json.Unmarshal(v, target); err != nil {
				return nil, fmt.Errorf("%s: gates.%s: %w", p, key, err)
			}
		}
	}

	if fr.RuleCommit == "" {
		return nil, fmt.Errorf("%s names no rule_commit", p)
	}

	return &fr, nil
}

// poolSource is a source as a freeze records it.
type poolSource struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// poolRound is one frozen, unvoided round as pooling reads it.
type poolRound struct {
	Round      int
	RuleCommit string
	Sources    []poolSource
	Gates      gates
	Score      *scoresRecord
}

type poolRoundInfo struct {
	Round      int          `json:"round"`
	RuleCommit string       `json:"rule_commit"`
	Sources    []poolSource `json:"sources"`
}

// poolRuleRound is what one round contributes to a rule's pool.
type poolRuleRound struct {
	Round         int  `json:"round"`
	N             int  `json:"n"`
	Wrong         int  `json:"wrong"`
	Unnoted       int  `json:"unnoted_disagreements"`
	AgreementGate bool `json:"agreement_gate"`
}

// poolRule is a rule's firings pooled over both rounds. DenyGate is null when
// the rule fired in only one round, since one round never decides a deny.
type poolRule struct {
	Rounds    []poolRuleRound `json:"rounds"`
	N         int             `json:"n"`
	Correct   int             `json:"correct"`
	Wrong     int             `json:"wrong"`
	Precision *float64        `json:"precision"`
	WilsonLB  *float64        `json:"wilson_lb"`
	DenyGate  *bool           `json:"deny_gate"`
	Why       string          `json:"why,omitempty"`
}

// poolRecord is the pool.json of two rounds.
type poolRecord struct {
	Rounds  []poolRoundInfo      `json:"rounds"`
	Deny    denyGate             `json:"deny"`
	WilsonZ float64              `json:"wilson_z"`
	Method  map[string]string    `json:"method"`
	Rules   map[string]*poolRule `json:"rules"`
}

func runRoundPool(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("proofread calibrate round pool", flag.ContinueOnError)
	fset.SetOutput(stderr)

	var dirs repeated

	fset.Var(&dirs, "dir", "a scored round `dir`, given twice")
	out := fset.String("out", "", "write the pool to this `file` and print a table, instead of printing the JSON")

	if err := fset.Parse(args); err != nil {
		return exitError
	}

	if len(dirs) != 2 || fset.NArg() > 0 {
		return failure(stderr, errors.New("round pool takes -dir DIR twice and no operand"))
	}

	if *out != "" {
		for _, d := range dirs {
			if inside(d, *out) {
				return failure(stderr, fmt.Errorf("-out %s is inside the round directory %s, which pooling never writes", *out, d))
			}
		}
	}

	rounds := make([]*poolRound, len(dirs))

	for i, d := range dirs {
		r, err := loadPoolRound(d)
		if err != nil {
			return failure(stderr, err)
		}

		rounds[i] = r
	}

	rec, err := poolRounds(rounds)
	if err != nil {
		return failure(stderr, err)
	}

	if *out == "" {
		data, err := json.MarshalIndent(rec, "", " ")
		if err != nil {
			return failure(stderr, err)
		}

		fmt.Fprintf(stdout, "%s\n", data)

		return exitOK
	}

	if err := writeJSONFile(*out, rec); err != nil {
		return failure(stderr, err)
	}

	if err := writePoolTable(stdout, rec); err != nil {
		return failure(stderr, err)
	}

	return exitOK
}

// inside reports whether path p is dir or lies under it.
func inside(dir, p string) bool {
	d, errD := filepath.Abs(dir)
	f, errF := filepath.Abs(p)

	if errD != nil || errF != nil {
		return false
	}

	rel, err := filepath.Rel(d, f)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// loadPoolRound scores the round at dir the way `score` does, so an unfrozen
// round or one voided by a rule change is refused, and reads the sources its
// freeze recorded.
func loadPoolRound(dir string) (*poolRound, error) {
	score, err := scoreRound(dir, false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dir, err)
	}

	fr, err := readFreeze(filepath.Join(dir, freezeFile))
	if err != nil {
		return nil, err
	}

	if len(fr.Sources) == 0 {
		return nil, fmt.Errorf("round %d records no sources in its freeze, so its sources cannot be told from another round's", fr.Round)
	}

	r := &poolRound{Round: fr.Round, RuleCommit: fr.RuleCommit, Gates: fr.Gates, Score: score}

	named := map[string]bool{}

	for _, s := range fr.Sources {
		if s.SHA256 == "" {
			return nil, fmt.Errorf("round %d records no sha256 for source %s", fr.Round, s.Name)
		}

		named[s.Name] = true
		r.Sources = append(r.Sources, poolSource{Name: s.Name, SHA256: s.SHA256})
	}

	for _, rs := range score.Rules {
		for name := range rs.BySource {
			if !named[name] {
				return nil, fmt.Errorf("round %d has candidates from source %s, which its freeze does not record", fr.Round, name)
			}
		}
	}

	return r, nil
}

// poolRounds pools rounds[0] and rounds[1] per rule. The rounds must share no
// source name and no source sha256, since a second round over the same texts
// confirms nothing the first did not, and must have frozen the same deny gate.
func poolRounds(rounds []*poolRound) (*poolRecord, error) {
	a, b := rounds[0], rounds[1]

	for _, sa := range a.Sources {
		for _, sb := range b.Sources {
			switch {
			case sa.Name == sb.Name:
				return nil, fmt.Errorf("rounds %d and %d both draw from source %s", a.Round, b.Round, sa.Name)
			case sa.SHA256 == sb.SHA256:
				return nil, fmt.Errorf("rounds %d and %d draw from the same data: sources %s and %s share sha256 %s",
					a.Round, b.Round, sa.Name, sb.Name, sa.SHA256)
			}
		}
	}

	if a.Gates.Deny != b.Gates.Deny || a.Gates.Advise.WilsonZ != b.Gates.Advise.WilsonZ {
		return nil, fmt.Errorf("rounds %d and %d froze different deny gates or Wilson z", a.Round, b.Round)
	}

	g := a.Gates.Deny
	if g.Rounds > len(rounds) {
		return nil, fmt.Errorf("the deny gate asks for %d rounds and pool reads two", g.Rounds)
	}

	z := a.Gates.Advise.WilsonZ

	rec := &poolRecord{Deny: g, WilsonZ: z, Rules: map[string]*poolRule{}, Method: poolMethod(g, z)}

	for _, r := range rounds {
		rec.Rounds = append(rec.Rounds, poolRoundInfo{Round: r.Round, RuleCommit: r.RuleCommit, Sources: r.Sources})

		for name, rs := range r.Score.Rules {
			if rs.N == 0 {
				continue
			}

			pr := rec.Rules[name]
			if pr == nil {
				pr = &poolRule{}
				rec.Rules[name] = pr
			}

			wrong := rs.N - rs.Correct
			pr.Rounds = append(pr.Rounds, poolRuleRound{
				Round: r.Round, N: rs.N, Wrong: wrong, Unnoted: rs.Unnoted, AgreementGate: rs.AgreementGate,
			})
			pr.N += rs.N
			pr.Correct += rs.Correct
			pr.Wrong += wrong
		}
	}

	for _, pr := range rec.Rules {
		pr.Precision = rounded(float64(pr.Correct) / float64(pr.N))
		pr.WilsonLB = rounded(proofread.WilsonLower(pr.Correct, pr.N, z))

		if len(pr.Rounds) < 2 {
			pr.Why = fmt.Sprintf("fired in round %d only, and one round never decides a deny", pr.Rounds[0].Round)

			continue
		}

		var short []string

		if pr.N < g.PooledFirings {
			short = append(short, fmt.Sprintf("pooled n %d < %d", pr.N, g.PooledFirings))
		}

		if pr.Wrong > g.MaxWrong {
			short = append(short, fmt.Sprintf("wrong %d > %d", pr.Wrong, g.MaxWrong))
		}

		// A deny refuses text outright, so it rests only on labels both rounds
		// agreed on well enough to pass their own agreement gate.
		for _, rr := range pr.Rounds {
			if !rr.AgreementGate {
				short = append(short, fmt.Sprintf("round %d fails its agreement gate", rr.Round))
			}
		}

		pass := len(short) == 0
		pr.DenyGate = &pass
		pr.Why = strings.Join(short, ", ")
	}

	return rec, nil
}

func poolMethod(g denyGate, z float64) map[string]string {
	return map[string]string{
		"pooled":     "per rule, the firings of both rounds; wrong is n less final CORRECT, UNSURE counting as WRONG",
		"deny_gate":  fmt.Sprintf("pooled n >= %d and wrong <= %d, over %d frozen rounds", g.PooledFirings, g.MaxWrong, g.Rounds),
		"no_verdict": "deny_gate is null for a rule that fired in one round only",
		"wilson":     fmt.Sprintf("Wilson score interval lower bound, z=%g, on pooled CORRECT over n", z),
		"sources":    "refused unless both rounds are frozen, unvoided and share no source name and no source sha256",
	}
}

func writePoolTable(w io.Writer, rec *poolRecord) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tN\tWRONG\tPRECISION\tLOWER\tDENY")

	for _, name := range slices.Sorted(maps.Keys(rec.Rules)) {
		pr := rec.Rules[name]

		deny := "none"
		if pr.DenyGate != nil {
			deny = verdict(*pr.DenyGate)
		}

		if pr.Why != "" {
			deny += " (" + pr.Why + ")"
		}

		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\n", name, pr.N, pr.Wrong, percent(pr.Precision), percent(pr.WilsonLB), deny)
	}

	return tw.Flush()
}

func writeScoresTable(w io.Writer, rec *scoresRecord) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RULE\tSOURCE\tN\tCORRECT\tPRECISION\tLOWER\tDISAGREE\tUNNOTED\tALPHA\tAGREEMENT\tADVISE")

	for _, r := range slices.Sorted(maps.Keys(rec.Rules)) {
		rs := rec.Rules[r]

		worst := "no source reaches the minimum"
		if rs.WorstSource != nil {
			worst = fmt.Sprintf("worst %s %s", rs.WorstSource.Source, verdict(rs.WorstSource.Pass))
		}

		fmt.Fprintf(tw, "%s\t(all)\t%s\t%s (%s)\n", r, tallyColumns(rs.tally), verdict(rs.AdviseGate), worst)

		for _, src := range slices.Sorted(maps.Keys(rs.BySource)) {
			fmt.Fprintf(tw, "%s\t%s\t%s\t\n", r, src, tallyColumns(rs.BySource[src]))
		}
	}

	return tw.Flush()
}

func tallyColumns(t tally) string {
	return fmt.Sprintf("%d\t%d\t%s\t%s\t%d\t%d\t%s\t%s", t.N, t.Correct, percent(t.Precision), percent(t.WilsonLB),
		t.Disagreements, t.Unnoted, decimal(t.Alpha), verdict(t.AgreementGate))
}

func verdict(pass bool) string {
	if pass {
		return "pass"
	}

	return "fail"
}

func decimal(p *float64) string {
	if p == nil {
		return "-"
	}

	return fmt.Sprintf("%.3f", *p)
}

// ruleFilesChanged lists the rule files that differ from rev in the work
// tree, untracked ones included.
func ruleFilesChanged(rev string) ([]string, error) {
	diff, err := gitOutput(append([]string{"diff", "--name-only", rev, "--"}, rulePathspecs...)...)
	if err != nil {
		return nil, err
	}

	untracked, err := gitOutput(append([]string{"ls-files", "--others", "--exclude-standard", "--"}, rulePathspecs...)...)
	if err != nil {
		return nil, err
	}

	return append(strings.Fields(diff), strings.Fields(untracked)...), nil
}

func gitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s (%s): %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}

	return strings.TrimSpace(string(out)), nil
}

// proofreadVersion names the build judging: its module version and the
// commit it was built from, when the build recorded them.
func proofreadVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	parts := []string{info.Main.Version}

	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision":
			parts = append(parts, s.Value)
		case s.Key == "vcs.modified" && s.Value == "true":
			parts = append(parts, "modified")
		}
	}

	return strings.Join(parts, " ")
}

// executableSHA256 is the digest of the running binary. Build info alone does
// not name the sampler, since a cached build output restores a binary another
// checkout built.
func executableSHA256() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find the running proofread binary: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("hash the running proofread binary: %w", err)
	}

	return sha256Hex(data), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func readJSONFile(p string, v any) error {
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}

	return nil
}

func readJSONLines[T any](p string) ([]T, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []T

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}

		var v T
		if err := json.Unmarshal(line, &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", p, n, err)
		}

		out = append(out, v)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}

	return out, nil
}
