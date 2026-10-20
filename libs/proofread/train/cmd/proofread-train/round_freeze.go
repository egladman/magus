package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

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
	fset := flag.NewFlagSet("proofread-train calibrate round freeze", flag.ContinueOnError)
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
		return failure(stderr, errors.New("round freeze takes --dir DIR --guideline FILE and no operand"))
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

// checkFrozen refuses a round the freeze no longer describes: rule files
// changed since its rule commit, or a guideline changed since the freeze.
func checkFrozen(dir string, fr *freezeRecord) error {
	changed, err := ruleFilesChanged(fr.RuleCommit)
	if err != nil {
		return err
	}

	if len(changed) > 0 {
		return fmt.Errorf("round %d is void, since the rule files changed after its rule commit %s (%s), "+
			"and --replay only reproduces its numbers", fr.Round, fr.RuleCommit, strings.Join(changed, ", "))
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
