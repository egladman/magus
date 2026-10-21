package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/egladman/magus/libs/proofread/internal/calibrate"
)

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
	fset := flag.NewFlagSet("proofread-train calibrate round pool", flag.ContinueOnError)
	fset.SetOutput(stderr)

	var dirs repeated

	fset.Var(&dirs, "dir", "a scored round `dir`, given twice")
	out := fset.String("out", "", "write the pool to this `file` and print a table, instead of printing the JSON")

	if err := fset.Parse(args); err != nil {
		return exitError
	}

	if len(dirs) != 2 || fset.NArg() > 0 {
		return failure(stderr, errors.New("round pool takes --dir DIR twice and no operand"))
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
		pr.WilsonLB = rounded(calibrate.WilsonLower(pr.Correct, pr.N, z))

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
