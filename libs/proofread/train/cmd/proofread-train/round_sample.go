package main

import (
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
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/egladman/magus/libs/proofread"
	"github.com/egladman/magus/libs/proofread/internal/calibrate"
)

func runRoundSample(args []string, stdout, stderr io.Writer) int {
	fset := flag.NewFlagSet("proofread-train calibrate round sample", flag.ContinueOnError)
	fset.SetOutput(stderr)
	rulesList := fset.String("rules", "", "sample firings of these comma-separated `rules`")
	seed := fset.Int64("seed", 0, "draw with this `seed`, fixed before anyone sees the sample")
	perRule := fset.Int("per-rule", 40, "draw at most `n` firings of each rule")
	minPerSource := fset.Int("min-per-source", 10, "give each source with enough fresh firings at least `n`, so its own gate runs")
	maxPerText := fset.Int("max-per-text", 2, "keep at most `n` firings of a rule from one text")
	decisionsPath := fset.String("decisions", "", "judge with the decisions table in this `file`; by default every sampled rule is deny")
	outDir := fset.String("out", "", "write the round into this `dir`")
	roundNum := fset.Int("round", 0, "the round `number` the candidate ids carry; by default read from an --out named round<N>")
	field := fset.String("field", "text", "the comma-separated `fields` joined by newlines into each text")
	kindField := fset.String("kind-field", "kind", "the `field` naming each record's kind")
	kindName := fset.String("kind", "", "judge a record whose --kind-field is empty as this `kind`")

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
		return errors.New("round sample needs --out")
	}

	if c.seed == 0 {
		return errors.New("round sample needs --seed, a nonzero seed fixed before the sample is seen")
	}

	if c.perRule < 1 || c.minPerSource < 0 || c.maxPerText < 1 {
		return errors.New("--per-rule and --max-per-text must be at least 1, and --min-per-source at least 0")
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
		return errors.New("round sample needs --rules")
	}

	if err := c.resolveDecisions(decisionsPath); err != nil {
		return err
	}

	c.round = round
	if c.round == 0 {
		digits, named := strings.CutPrefix(filepath.Base(filepath.Clean(c.outDir)), "round")

		n, err := strconv.Atoi(digits)
		if !named || err != nil || n < 1 {
			return fmt.Errorf("--out %s is not named round<N>, so pass --round", c.outDir)
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

	var raw struct {
		Rules    map[string]string `json:"rules"`
		Paths    json.RawMessage   `json:"paths"`
		Minimums json.RawMessage   `json:"minimums"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("decisions %s: %w", p, err)
	}

	if len(raw.Paths) > 0 && string(raw.Paths) != "null" {
		return fmt.Errorf("decisions %s: a sample's records have no path, so the table cannot scope rules by glob", p)
	}

	decisions, err := ruleDecisions(raw.Rules)
	if err != nil {
		return fmt.Errorf("decisions %s: %w", p, err)
	}

	house := map[proofread.Rule]bool{}
	for _, doc := range proofread.Catalog() {
		house[doc.Name] = doc.House
	}

	for _, r := range c.rules {
		if d, set := decisions[r]; (set && d == proofread.DecisionOff) || (!set && house[r]) {
			return fmt.Errorf("decisions %s sets %q off, so it cannot be sampled", p, r)
		}
	}

	c.decisions = decisions

	return nil
}

// ruleDecisions checks each name against the rules and each value against the
// three decisions. A copy of cmd/proofread's: neither main package can import the other.
func ruleDecisions(in map[string]string) (map[proofread.Rule]proofread.Decision, error) {
	known := proofread.Rules()
	out := make(map[proofread.Rule]proofread.Decision, len(in))

	for _, name := range slices.Sorted(maps.Keys(in)) {
		r, d := proofread.Rule(name), proofread.Decision(in[name])
		if !slices.Contains(known, r) {
			return nil, fmt.Errorf("unknown rule %q", name)
		}

		switch d {
		case proofread.DecisionOff, proofread.DecisionAdvise, proofread.DecisionDeny:
		default:
			return nil, fmt.Errorf("rule %q: unknown decision %q: want off, advise or deny", name, in[name])
		}

		out[r] = d
	}

	return out, nil
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
			if k, ok := proofread.ParseKind(v); ok {
				t.kind = k
			}
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
			return nil, fmt.Errorf("source %s: record %d has no %q field and no --kind is given", s.Path, t.record, c.kindField)
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

	cases, err := calibrate.LoadCases(calibrate.LabeledCases())
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
		cases, err := calibrate.LoadCases(os.DirFS(p))
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
