package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// claims are review replies the claim rule reports, none of them a case
// proofread carries.
var claims = []string{
	"Parsing drops from 900ms to 300ms.",
	"The cache warms in 40ms now.",
	"Lookups are 5x faster after this.",
	"The build takes 12 seconds less.",
	"Memory use falls by 30 percent.",
	"The queue drains in 2 minutes now.",
	"Reads are 3x faster with the index.",
	"The retry loop finishes in 80ms.",
	"Cold start drops from 2s to 600ms.",
	"The parser is 4x faster on large files.",
	"Writes take 15ms less per batch.",
	"The scan now ends in 7 seconds.",
}

func writeJSONL(t *testing.T, p string, rows ...any) {
	t.Helper()

	var b bytes.Buffer
	for _, r := range rows {
		if err := json.NewEncoder(&b).Encode(r); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func replies(texts ...string) []any {
	out := make([]any, len(texts))
	for i, s := range texts {
		out[i] = map[string]any{"text": s, "kind": "review-reply"}
	}

	return out
}

func runRoundCmd(t *testing.T, want int, args ...string) string {
	t.Helper()

	var stdout, stderr bytes.Buffer
	if code := run(append([]string{"calibrate", "round"}, args...), strings.NewReader(""), &stdout, &stderr); code != want {
		t.Fatalf("round %s: exit %d, want %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, want, stdout.String(), stderr.String())
	}

	return stdout.String() + stderr.String()
}

func readCandidates(t *testing.T, dir string) []candidate {
	t.Helper()

	cands, err := readJSONLines[candidate](filepath.Join(dir, candidatesFile))
	if err != nil {
		t.Fatal(err)
	}

	return cands
}

func readCounts(t *testing.T, dir string) roundCounts {
	t.Helper()

	var c roundCounts
	if err := readJSONFile(filepath.Join(dir, countsFile), &c); err != nil {
		t.Fatal(err)
	}

	return c
}

func TestClaimsFire(t *testing.T) {
	for _, s := range claims {
		found := proofread.JudgeText(s, proofread.KindReviewReply, proofread.WithOnly(proofread.Rule("claim")),
			proofread.WithDecisions(map[proofread.Rule]proofread.Decision{"claim": proofread.DecisionDeny}))
		if len(found) == 0 {
			t.Errorf("claim does not fire on %q, which the round tests rely on", s)
		}
	}
}

func TestRoundSampleDrawsTheSameRoundFromTheSameSeed(t *testing.T) {
	tmp := t.TempDir()
	alpha, beta := filepath.Join(tmp, "alpha.jsonl"), filepath.Join(tmp, "beta.jsonl")
	writeJSONL(t, alpha, replies(claims...)...)
	writeJSONL(t, beta, replies("Indexing drops from 9s to 3s.", "The sweep is 2x faster.", "Startup falls by 50ms.")...)

	draw := func(dir string, seed int) []candidate {
		runRoundCmd(t, 0, "sample", "-rules", "claim", "-seed", fmt.Sprint(seed), "-per-rule", "8", "-min-per-source", "4",
			"-round", "7", "-out", dir, alpha, beta)

		return readCandidates(t, dir)
	}

	first, again, other := draw(filepath.Join(tmp, "a"), 11), draw(filepath.Join(tmp, "b"), 11), draw(filepath.Join(tmp, "c"), 12)

	if !slices.Equal(first, again) {
		t.Errorf("the same seed drew different rounds:\n%v\n%v", first, again)
	}

	if slices.Equal(first, other) {
		t.Error("another seed drew the same round")
	}

	counts := readCounts(t, filepath.Join(tmp, "a"))
	rc := counts.ByRule["claim"]

	// alpha holds 4 or more fresh firings and gets 4 first; beta, under the
	// minimum, shares the other 4 round-robin with alpha.
	want := map[string]int{"alpha": 6, "beta": 2}
	if rc == nil || rc.Candidates != 8 || rc.BySourceTaken["alpha"] != want["alpha"] || rc.BySourceTaken["beta"] != want["beta"] {
		t.Fatalf("claim counts = %+v, want 8 taken as %v", rc, want)
	}

	if rc.Seen != 15 || rc.Fresh != 15 || rc.Excluded != 0 || rc.Short {
		t.Errorf("seen %d fresh %d excluded %d short %v, want 15, 15, 0, false", rc.Seen, rc.Fresh, rc.Excluded, rc.Short)
	}

	ids := make([]string, len(first))
	for i, c := range first {
		ids[i] = c.ID

		if c.Rule != "claim" || c.Kind != "review-reply" || c.Scope != "sentence" || c.Text == "" || !strings.Contains(c.Context, "[["+c.Match+"]]") {
			t.Errorf("candidate %+v lacks its rule, kind, scope, text or marked match", c)
		}
	}

	if want := []string{"r7-0001", "r7-0002", "r7-0003", "r7-0004", "r7-0005", "r7-0006", "r7-0007", "r7-0008"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}

	template, err := readJSONLines[labelRow](filepath.Join(tmp, "a", templateFile))
	if err != nil || len(template) != 8 || template[0].ID != "r7-0001" || template[0].Label != "" {
		t.Errorf("template = %+v, %v; want one empty row per candidate", template, err)
	}
}

func TestRoundSampleExcludesWhatWasShownBefore(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "replies.jsonl")
	earlier := filepath.Join(tmp, "round3", candidatesFile)

	// A sentence proofread carries as a case, a sentence an earlier round
	// showed inside a longer context, a text repeated, and an email.
	writeJSONL(t, src, replies(
		"Startup drops from 410ms to 260ms.",
		"Thanks for the look. The cache warms in 40ms now.",
		"Lookups are 5x faster after this.",
		"Lookups are 5x  faster after this.",
		"Mail ops@example.com: the build takes 12 seconds less.",
	)...)
	writeJSONL(t, earlier, map[string]any{"id": "v0001", "context": "...in review. [[The cache warms in 40ms now.]] Ship it."})

	dir := filepath.Join(tmp, "round8")
	runRoundCmd(t, 0, "sample", "-rules", "claim", "-seed", "5", "-min-per-source", "0", "-exclude", earlier, "-out", dir, src)

	counts := readCounts(t, dir)
	rc := counts.ByRule["claim"]

	if rc.Excluded != 2 || rc.Fresh != 2 || rc.Candidates != 2 || !rc.Short {
		t.Errorf("claim counts = %+v, want 2 excluded and 2 fresh, short of 40", rc)
	}

	if s := counts.Sources[0]; s.Records != 5 || s.Texts != 4 || s.Duplicates != 1 || len(s.SHA256) != 64 {
		t.Errorf("source = %+v, want 5 records, 4 texts, 1 duplicate and a sha256", s)
	}

	for _, c := range readCandidates(t, dir) {
		if strings.Contains(c.Text, "example.com") {
			t.Errorf("candidate %s keeps the email: %q", c.ID, c.Text)
		}

		if c.Record == 5 && !strings.Contains(c.Text, "[REDACTED]") {
			t.Errorf("candidate %s text = %q, want the email redacted", c.ID, c.Text)
		}
	}

	out := runRoundCmd(t, exitError, "sample", "-rules", "claim", "-seed", "6", "-out", dir, src)
	if !strings.Contains(out, "a round is drawn once") {
		t.Errorf("a second draw into %s: %s", dir, out)
	}
}

func TestRoundSampleRefusesAnUnusableCall(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "s.jsonl")
	writeJSONL(t, src, map[string]any{"text": "The cache warms in 40ms now."})

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no seed", []string{"-rules", "claim", "-out", filepath.Join(tmp, "round1"), src}, "-seed"},
		{"unknown rule", []string{"-rules", "nosuch", "-seed", "1", "-out", filepath.Join(tmp, "round1"), src}, "unknown rule"},
		{"unnamed dir", []string{"-rules", "claim", "-seed", "1", "-out", filepath.Join(tmp, "x"), src}, "pass -round"},
		{"no kind", []string{"-rules", "claim", "-seed", "1", "-out", filepath.Join(tmp, "round2"), src}, "no -kind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := runRoundCmd(t, exitError, append([]string{"sample"}, tt.args...)...)
			if !strings.Contains(out, tt.want) {
				t.Errorf("output %q does not name %q", out, tt.want)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Co-authored-by: Ada <ada@example.org>", "Co-authored-by: Ada <[REDACTED]>"},
		{"export GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123456789", "export GITHUB_TOKEN=[REDACTED]"},
		{"key AKIAABCDEFGHIJKLMNOP here", "key [REDACTED] here"},
		{"Authorization: Bearer abcdefghijklmnop.qrstuvwxyz", "Authorization: Bearer [REDACTED]"},
		{"use aB3dE5gH7jK9mN1pQ3sT5vW7yZ9bC1dE2 here", "use [REDACTED] here"},
		{"fixed in 4762d2e2d0a1b2c3d4e5f60718293a4b5c6d7e8f", "fixed in 4762d2e2d0a1b2c3d4e5f60718293a4b5c6d7e8f"},
		{"TestCalibrateWritesATableFromACasesDir runs", "TestCalibrateWritesATableFromACasesDir runs"},
	}

	for _, tt := range tests {
		if got := redact(tt.in); got != tt.want {
			t.Errorf("redact(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSentenceAround(t *testing.T) {
	text := "Title line\n\nFirst one. The cache warms in 40ms now. Last one.\n- a list item, fast.\n## Heading"

	tests := []struct{ match, want string }{
		{"40ms", "The cache warms in 40ms now."},
		{"fast", "- a list item, fast."},
		{"Heading", "## Heading"},
		{"Title", "Title line"},
	}

	for _, tt := range tests {
		i := strings.Index(text, tt.match)

		s, e := sentenceAround(text, i, i+len(tt.match))
		if got := text[s:e]; got != tt.want {
			t.Errorf("sentence around %q = %q, want %q", tt.match, got, tt.want)
		}
	}
}

// labeled is one candidate of a hand-built round: its source and the labels
// A, B and the adjudicator gave.
type labeled struct {
	source, a, b, adjudicated string
}

func writeLabeledRound(t *testing.T, dir string, rows []labeled) {
	t.Helper()

	var cands, a, b, adj []any

	for i, r := range rows {
		id := fmt.Sprintf("r9-%04d", i+1)
		cands = append(cands, candidate{ID: id, Rule: "claim", Kind: "review-reply", Source: r.source})
		note := ""

		if r.a != r.b {
			note = "sense: a note"
		}

		a = append(a, labelRow{ID: id, Label: r.a, Note: note})
		b = append(b, labelRow{ID: id, Label: r.b, Note: note})

		if r.adjudicated != "" {
			adj = append(adj, adjudicatedRow{ID: id, Label: r.adjudicated, Reason: "by the guideline"})
		}
	}

	writeJSONL(t, filepath.Join(dir, candidatesFile), cands...)
	writeJSONL(t, filepath.Join(dir, labelsAFile), a...)
	writeJSONL(t, filepath.Join(dir, labelsBFile), b...)
	writeJSONL(t, filepath.Join(dir, adjudicatedFile), adj...)
}

func TestRoundScoreTalliesAdjudicatedLabels(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "round9")

	// 40 firings: 30 from "big", 34 of them CORRECT in the end. In big, 28
	// agreed CORRECT, one split adjudicated CORRECT and one agreed WRONG. In
	// small, 5 agreed CORRECT, one agreed UNSURE (counted WRONG), one split
	// adjudicated WRONG, and three agreed WRONG.
	var rows []labeled
	for range 28 {
		rows = append(rows, labeled{"big", "CORRECT", "CORRECT", ""})
	}

	rows = append(rows, labeled{"big", "CORRECT", "WRONG", "CORRECT"}, labeled{"big", "wrong", "wrong", ""})

	for range 5 {
		rows = append(rows, labeled{"small", "CORRECT", "CORRECT", ""})
	}

	rows = append(rows, labeled{"small", "UNSURE", "UNSURE", ""}, labeled{"small", "CORRECT", "WRONG", "WRONG"})

	for range 3 {
		rows = append(rows, labeled{"small", "WRONG", "WRONG", ""})
	}

	writeLabeledRound(t, dir, rows)

	freeze := map[string]any{
		"round": 9, "rule_commit": "abc123", "sampler_seed": 41, "rules": []string{"claim", "absolute"},
		"gates": defaultGates(),
	}
	if err := writeJSONFile(filepath.Join(dir, freezeFile), freeze); err != nil {
		t.Fatal(err)
	}

	table := runRoundCmd(t, 0, "score", "-dir", dir, "-replay")

	var rec scoresRecord
	if err := readJSONFile(filepath.Join(dir, scoresFile), &rec); err != nil {
		t.Fatal(err)
	}

	claim := rec.Rules["claim"]
	got := []any{claim.N, claim.Correct, *claim.Precision, *claim.WilsonLB, claim.Disagreements, *claim.RawAgreement, *claim.Alpha,
		claim.PrecisionGate, claim.AgreementGate, claim.WorstSource.Source, claim.WorstSource.Pass, claim.AdviseGate}
	// Pooled, the rule passes precision and agreement; small, the worst source
	// with 10 firings, holds it back from advise.
	want := []any{40, 34, 0.85, 0.7093, 2, 0.95, 0.811, true, true, "small", false, false}

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("claim = %v, want %v", got, want)
	}

	small := claim.BySource["small"]
	if got := []any{small.N, small.Correct, *small.WilsonLB, small.Disagreements, *small.Alpha, small.AgreementGate, small.PrecisionGate}; fmt.Sprint(got) !=
		fmt.Sprint([]any{10, 5, 0.2366, 1, 0.8319, true, false}) {
		t.Errorf("small = %v", got)
	}

	if len(claim.SpotCheck) != 0 {
		t.Errorf("spot check = %v, want none for a rule that fails advise", claim.SpotCheck)
	}

	// absolute was frozen but drew nothing: it is reported, and fails.
	if abs := rec.Rules["absolute"]; abs == nil || abs.N != 0 || abs.AgreementGate || abs.AdviseGate || abs.Precision != nil {
		t.Errorf("absolute = %+v, want an empty failing tally", abs)
	}

	if !rec.Replay || !strings.Contains(table, "fail (worst small fail)") {
		t.Errorf("replay %v, table:\n%s", rec.Replay, table)
	}

	// With the source minimum past small's 10, big is the worst source and
	// the rule passes, drawing the same 10 agreed labels on every score.
	g := defaultGates()
	g.Source.MinFirings = 11
	freeze["gates"] = g

	if err := writeJSONFile(filepath.Join(dir, freezeFile), freeze); err != nil {
		t.Fatal(err)
	}

	spot := func() []string {
		runRoundCmd(t, 0, "score", "-dir", dir, "-replay")

		var again scoresRecord
		if err := readJSONFile(filepath.Join(dir, scoresFile), &again); err != nil {
			t.Fatal(err)
		}

		c := again.Rules["claim"]
		if !c.AdviseGate || c.WorstSource == nil || c.WorstSource.Source != "big" {
			t.Fatalf("claim = %+v, want advise passing on big", c)
		}

		return c.SpotCheck
	}

	first := spot()
	if len(first) != 10 || !slices.Equal(first, spot()) {
		t.Errorf("spot check = %v, want the same 10 ids each time", first)
	}

	for _, id := range first {
		var n int
		if _, err := fmt.Sscanf(id, "r9-%04d", &n); err != nil || n > 35 || n == 29 || n == 30 {
			t.Errorf("spot check holds %s, which both labelers did not mark CORRECT", id)
		}
	}
}

func TestRoundScoreRefusesAnOpenOrUnfrozenRound(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "round9")
	writeLabeledRound(t, dir, []labeled{{"s", "CORRECT", "WRONG", ""}, {"s", "CORRECT", "CORRECT", ""}})

	if out := runRoundCmd(t, exitError, "score", "-dir", dir, "-replay"); !strings.Contains(out, "only once frozen") {
		t.Errorf("an unfrozen round: %s", out)
	}

	if err := writeJSONFile(filepath.Join(dir, freezeFile), map[string]any{"round": 9, "rule_commit": "abc", "rules": []string{"claim"}}); err != nil {
		t.Fatal(err)
	}

	if out := runRoundCmd(t, exitError, "score", "-dir", dir, "-replay"); !strings.Contains(out, "1 disagreements have no row") {
		t.Errorf("an unadjudicated disagreement: %s", out)
	}
}

func TestRoundScoreFailsAgreementOnAnUnnotedDisagreement(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "round9")

	// One split in 40 is inside n/10, so only the note can fail the gate.
	rows := []labeled{{"s", "CORRECT", "WRONG", "CORRECT"}}
	for range 39 {
		rows = append(rows, labeled{"s", "CORRECT", "CORRECT", ""})
	}

	writeLabeledRound(t, dir, rows)

	if err := writeJSONFile(filepath.Join(dir, freezeFile), map[string]any{
		"round": 9, "rule_commit": "abc123", "rules": []string{"claim"}, "gates": defaultGates(),
	}); err != nil {
		t.Fatal(err)
	}

	score := func() *ruleScore {
		runRoundCmd(t, 0, "score", "-dir", dir, "-replay")

		var rec scoresRecord
		if err := readJSONFile(filepath.Join(dir, scoresFile), &rec); err != nil {
			t.Fatal(err)
		}

		return rec.Rules["claim"]
	}

	if c := score(); !c.AgreementGate || c.Unnoted != 0 || len(c.UnnotedIDs) != 0 {
		t.Fatalf("a noted disagreement: gate %v, unnoted %d %v, want a pass", c.AgreementGate, c.Unnoted, c.UnnotedIDs)
	}

	bare := []any{labelRow{ID: "r9-0001", Label: "WRONG"}}
	for i := 2; i <= 40; i++ {
		bare = append(bare, labelRow{ID: fmt.Sprintf("r9-%04d", i), Label: "CORRECT"})
	}

	writeJSONL(t, filepath.Join(dir, labelsBFile), bare...)

	c := score()
	if c.AgreementGate || c.Unnoted != 1 || !slices.Equal(c.UnnotedIDs, []string{"r9-0001"}) {
		t.Errorf("an unnoted disagreement: gate %v, unnoted %d %v, want a fail naming r9-0001", c.AgreementGate, c.Unnoted, c.UnnotedIDs)
	}

	if c.Disagreements*10 > c.N {
		t.Errorf("disagreements %d of %d: the n/10 bar should hold so the note alone fails the gate", c.Disagreements, c.N)
	}
}

// git runs git in the current directory, failing the test on an error.
func git(t *testing.T, args ...string) string {
	t.Helper()

	out, err := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t.invalid", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

func TestRoundFreezeSealsTheRuleCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	repo := t.TempDir()
	t.Chdir(repo)

	ruleFile := filepath.Join(repo, "libs", "conventions", "proofread", "rule.go")
	if err := os.MkdirAll(filepath.Dir(ruleFile), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(ruleFile, []byte("package proofread\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	git(t, "init", "-q")
	git(t, "add", ".")
	git(t, "commit", "-q", "-m", "add a rule")
	head := git(t, "rev-parse", "HEAD")

	data := filepath.Join(t.TempDir(), "replies.jsonl")
	writeJSONL(t, data, replies(claims[:3]...)...)

	guideline := filepath.Join(t.TempDir(), "guideline.md")
	if err := os.WriteFile(guideline, []byte("# guideline\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "round5")
	runRoundCmd(t, 0, "sample", "-rules", "claim", "-seed", "3", "-out", dir, data)

	// A dirty rule file refuses the freeze.
	if err := os.WriteFile(ruleFile, []byte("package proofread\n\nvar x = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if out := runRoundCmd(t, exitError, "freeze", "-dir", dir, "-guideline", guideline); !strings.Contains(out, "not committed") {
		t.Errorf("a dirty freeze: %s", out)
	}

	git(t, "checkout", "--", ".")
	runRoundCmd(t, 0, "freeze", "-dir", dir, "-guideline", guideline, "-gates", "alpha=0.9,source-firings=5")

	fr, err := readFreeze(filepath.Join(dir, freezeFile))
	if err != nil {
		t.Fatal(err)
	}

	if fr.RuleCommit != head || fr.SamplerSeed != 3 || !slices.Equal(fr.Rules, []string{"claim"}) || fr.Gates.Agreement.Alpha != 0.9 ||
		fr.Gates.Source.MinFirings != 5 || fr.Gates.Advise.LowerBound != 0.70 || fr.Guideline == nil || len(fr.Sources) != 1 ||
		fr.Sources[0].SHA256 != sha256Hex(mustRead(t, data)) || fr.ShortOfMin["claim"] != 3 {
		t.Fatalf("freeze = %+v", fr)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	if fr.ProofreadSHA256 != sha256Hex(mustRead(t, self)) {
		t.Errorf("proofread_sha256 = %q, want the digest of the running binary", fr.ProofreadSHA256)
	}

	var rows []any
	for _, c := range readCandidates(t, dir) {
		rows = append(rows, labelRow{ID: c.ID, Label: "CORRECT"})
	}

	writeJSONL(t, filepath.Join(dir, labelsAFile), rows...)
	writeJSONL(t, filepath.Join(dir, labelsBFile), rows...)
	runRoundCmd(t, 0, "score", "-dir", dir)

	// A committed rule change after the freeze voids the round.
	if err := os.WriteFile(ruleFile, []byte("package proofread\n\nvar y = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	git(t, "commit", "-q", "-am", "tune the rule")

	if out := runRoundCmd(t, exitError, "score", "-dir", dir); !strings.Contains(out, "is void") || !strings.Contains(out, "rule.go") {
		t.Errorf("a score after a rule change: %s", out)
	}

	runRoundCmd(t, 0, "score", "-dir", dir, "-replay")
}

// poolTestRound is a scored round as pooling reads it: each rule's n and
// correct count, drawn from the one source.
func poolTestRound(round int, source, sha string, rules map[string][2]int) *poolRound {
	score := &scoresRecord{Round: round, Rules: map[string]*ruleScore{}}

	for name, nc := range rules {
		score.Rules[name] = &ruleScore{tally: tally{N: nc[0], Correct: nc[1], AgreementGate: true}}
	}

	g := defaultGates()

	return &poolRound{Round: round, RuleCommit: "abc", Sources: []poolSource{{source, sha}}, Gates: g, Score: score}
}

func TestPoolRoundsDeniesNothingARoundDisagreedOn(t *testing.T) {
	a := poolTestRound(5, "aidev-pr", "sha-a", map[string][2]int{"r": {40, 40}})
	b := poolTestRound(6, "git-commit", "sha-b", map[string][2]int{"r": {30, 30}})
	b.Score.Rules["r"].AgreementGate = false

	rec, err := poolRounds([]*poolRound{a, b})
	if err != nil {
		t.Fatal(err)
	}

	if pr := rec.Rules["r"]; pr.DenyGate == nil || *pr.DenyGate || pr.Why != "round 6 fails its agreement gate" {
		t.Errorf("pooled = %+v; want a refused deny naming round 6's agreement gate", pr)
	}
}

func TestPoolRoundsDecidesDenyFromPooledFirings(t *testing.T) {
	a := poolTestRound(5, "aidev-pr", "sha-a", map[string][2]int{
		"passes": {40, 40}, "one-wrong": {40, 39}, "two-wrong": {40, 38}, "short": {30, 30}, "lone": {70, 70}, "quiet": {0, 0},
	})
	b := poolTestRound(6, "git-commit", "sha-b", map[string][2]int{
		"passes": {30, 30}, "one-wrong": {30, 30}, "two-wrong": {30, 30}, "short": {20, 20}, "quiet": {45, 45},
	})

	rec, err := poolRounds([]*poolRound{a, b})
	if err != nil {
		t.Fatal(err)
	}

	type row struct {
		n, wrong int
		deny     string
		why      string
	}

	want := map[string]row{
		"passes":    {70, 0, "true", ""},
		"one-wrong": {70, 1, "true", ""},
		"two-wrong": {70, 2, "false", "wrong 2 > 1"},
		"short":     {50, 0, "false", "pooled n 50 < 60"},
		// A rule that fired in one round has no verdict, however clean.
		"lone":  {70, 0, "nil", "fired in round 5 only, and one round never decides a deny"},
		"quiet": {45, 0, "nil", "fired in round 6 only, and one round never decides a deny"},
	}

	got := map[string]row{}

	for name, pr := range rec.Rules {
		deny := "nil"
		if pr.DenyGate != nil {
			deny = fmt.Sprint(*pr.DenyGate)
		}

		got[name] = row{pr.N, pr.Wrong, deny, pr.Why}
	}

	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("pool = %v, want %v", got, want)
	}

	if pr := rec.Rules["one-wrong"]; *pr.WilsonLB != 0.9234 || len(pr.Rounds) != 2 || pr.Rounds[0].Wrong != 1 {
		t.Errorf("one-wrong = %+v, want lower bound 0.9234 and its wrong in round 5", pr)
	}
}

func TestPoolRoundsRefusesSharedSourcesAndDifferentGates(t *testing.T) {
	rules := map[string][2]int{"claim": {40, 40}}

	otherGates := poolTestRound(6, "git-commit", "sha-b", rules)
	otherGates.Gates.Deny.PooledFirings = 30

	tests := []struct {
		name string
		b    *poolRound
		want string
	}{
		{"same source name", poolTestRound(6, "aidev-pr", "sha-b", rules), "both draw from source aidev-pr"},
		{"same data under another name", poolTestRound(6, "renamed", "sha-a", rules), "share sha256 sha-a"},
		{"different deny gates", otherGates, "different deny gates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := poolRounds([]*poolRound{poolTestRound(5, "aidev-pr", "sha-a", rules), tt.b})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one naming %q", err, tt.want)
			}
		})
	}
}

func TestRoundPoolReadsTwoFrozenRoundsAndWritesNothingBeside(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	repo := t.TempDir()
	t.Chdir(repo)

	ruleFile := filepath.Join(repo, "libs", "conventions", "proofread", "rule.go")
	if err := os.MkdirAll(filepath.Dir(ruleFile), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(ruleFile, []byte("package proofread\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	git(t, "init", "-q")
	git(t, "add", ".")
	git(t, "commit", "-q", "-m", "add a rule")

	guideline := filepath.Join(t.TempDir(), "guideline.md")
	if err := os.WriteFile(guideline, []byte("# guideline\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Two sources of 6 claims each; the pool's gate is set to 10 firings.
	round := func(n int, source string, texts []string, wrong int) string {
		data := filepath.Join(t.TempDir(), source+".jsonl")
		writeJSONL(t, data, replies(texts...)...)

		dir := filepath.Join(t.TempDir(), fmt.Sprintf("round%d", n))
		runRoundCmd(t, 0, "sample", "-rules", "claim", "-seed", "3", "-out", dir, source+"="+data)
		runRoundCmd(t, 0, "freeze", "-dir", dir, "-guideline", guideline, "-gates", "deny-firings=10")

		var rows []any

		for i, c := range readCandidates(t, dir) {
			label := "CORRECT"
			if i < wrong {
				label = "WRONG"
			}

			rows = append(rows, labelRow{ID: c.ID, Label: label})
		}

		writeJSONL(t, filepath.Join(dir, labelsAFile), rows...)
		writeJSONL(t, filepath.Join(dir, labelsBFile), rows...)

		return dir
	}

	a := round(5, "alpha", claims[:6], 0)
	b := round(6, "beta", claims[6:], 1)

	var pooled poolRecord
	if err := json.Unmarshal([]byte(runRoundCmd(t, 0, "pool", "-dir", a, "-dir", b)), &pooled); err != nil {
		t.Fatal(err)
	}

	claim := pooled.Rules["claim"]
	if claim == nil || claim.N != 12 || claim.Wrong != 1 || claim.DenyGate == nil || !*claim.DenyGate || len(pooled.Rounds) != 2 {
		t.Fatalf("pool = %+v, want claim n 12 with 1 wrong passing", pooled)
	}

	out := filepath.Join(t.TempDir(), "pool.json")
	if table := runRoundCmd(t, 0, "pool", "-dir", a, "-dir", b, "-out", out); !strings.Contains(table, "pass") {
		t.Errorf("table:\n%s", table)
	}

	for _, d := range []string{a, b} {
		if _, err := os.Stat(filepath.Join(d, "pool.json")); err == nil {
			t.Errorf("pool wrote into %s", d)
		}
	}

	if got := runRoundCmd(t, exitError, "pool", "-dir", a, "-dir", b, "-out", filepath.Join(a, "pool.json")); !strings.Contains(got, "inside the round directory") {
		t.Errorf("an -out inside a round: %s", got)
	}

	if got := runRoundCmd(t, exitError, "pool", "-dir", a); !strings.Contains(got, "twice") {
		t.Errorf("one -dir: %s", got)
	}

	if got := runRoundCmd(t, exitError, "pool", "-dir", a, "-dir", a); !strings.Contains(got, "both draw from source alpha") {
		t.Errorf("the same round twice: %s", got)
	}

	// A committed rule change after the freeze voids both rounds.
	if err := os.WriteFile(ruleFile, []byte("package proofread\n\nvar y = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	git(t, "commit", "-q", "-am", "tune the rule")

	if got := runRoundCmd(t, exitError, "pool", "-dir", a, "-dir", b); !strings.Contains(got, "is void") {
		t.Errorf("a pool after a rule change: %s", got)
	}

	// A round with no freeze is refused.
	if err := os.Remove(filepath.Join(b, freezeFile)); err != nil {
		t.Fatal(err)
	}

	if got := runRoundCmd(t, exitError, "pool", "-dir", b, "-dir", a); !strings.Contains(got, "only once frozen") {
		t.Errorf("an unfrozen round: %s", got)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return data
}
