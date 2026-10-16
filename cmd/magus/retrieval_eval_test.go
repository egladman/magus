//go:build retrievaleval

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rankeval"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// A holdout read happens only when MAGUS_EVAL_CANDIDATE names the frozen candidate it
// scores, and is recorded in the ledger before any score is computed.
// MAGUS_EVAL_REPORT_ONLY=1 permits a further read of a set already read, logged as
// one that chooses nothing.
const (
	envCandidate  = "MAGUS_EVAL_CANDIDATE"
	envReportOnly = "MAGUS_EVAL_REPORT_ONLY"
)

func TestRetrievalEval(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	root, err := workspace.FindRoot("")
	require.NoError(t, err)
	data := filepath.Join(root, "testdata", "retrieval")
	agreement := readRetrievalFile(t, filepath.Join(data, "judgments.jsonl"), rankeval.ReadJudgments,
		"harvest it with ./magus buzz hack/retrieval/harvest.buzz")
	queries, qrels, judgedAbsent := readJudged(t, data)

	cfg, err := config.LoadWithRoot("", root)
	require.NoError(t, err)
	require.NoError(t, configgen.ApplyEnv(&cfg, os.Getenv))
	// A running daemon may be another build; every read here measures this one.
	cfg.Server.Enabled = false
	globalCfg = cfg

	ctx := context.Background()
	m, err := magus.Open(ctx, root, workspace.WithLoadedConfig(cfg), magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	ctx = withMagus(ctx, m)

	r := replayer{root: root, m: m, cfg: cfg, next: newNextGate(root), coverage: memoCoverage(ctx, m, cfg)}
	r.domain, err = loadWorkspaceGraph(ctx, m, cfg, false, false, false)
	require.NoError(t, err)
	r.symbols, err = loadWorkspaceGraph(ctx, m, cfg, false, false, true)
	require.NoError(t, err)
	r.known = make(map[string]string)
	for _, n := range r.symbols.Nodes() {
		r.known[n.ID] = n.Source
	}

	bySet := map[rankeval.EvalSet][]rankeval.Judgment{rankeval.SetAgentAgreement: agreement}
	absent := map[rankeval.EvalSet]string{
		rankeval.SetDocmaskGo:   "not built: plan row 6 (docmask-eval) builds it",
		rankeval.SetDocmaskBuzz: "not built: plan row 6 (docmask-eval) builds it",
		rankeval.SetBuzzIdent:   "not built: plan row 6 (docmask-eval) builds it",
		rankeval.SetFigures:     "not built: the figure-nodes row builds it",
	}
	reportOnlyRead := false
	if judgedAbsent != "" {
		absent[rankeval.SetJudgedDev] = judgedAbsent
		absent[rankeval.SetJudgedHoldout] = judgedAbsent
	} else {
		gold, err := rankeval.JudgedGold(qrels)
		require.NoError(t, err)
		bySet[rankeval.SetJudgedDev] = rankeval.JudgedJudgments(queries, gold, rankeval.SetJudgedDev)
		holdout := rankeval.JudgedJudgments(queries, gold, rankeval.SetJudgedHoldout)
		switch candidate := os.Getenv(envCandidate); {
		case len(holdout) == 0:
			absent[rankeval.SetJudgedHoldout] = "queries.jsonl holds no judged-holdout query"
		case candidate == "":
			absent[rankeval.SetJudgedHoldout] = fmt.Sprintf("sealed, %d queries; %s=<candidate> reads it once", len(holdout), envCandidate)
		default:
			reportOnlyRead = os.Getenv(envReportOnly) == "1"
			readHoldout(t, filepath.Join(data, "holdout-reads.jsonl"), queries, candidate, reportOnlyRead)
			bySet[rankeval.SetJudgedHoldout] = holdout
		}
	}
	lookup, skipped := definitionLookup(agreement, queries, r.symbols, r.known)
	bySet[rankeval.SetDefinitionLookup] = lookup
	fmt.Printf("definition-lookup: %d identifiers from agent-agreement; left out: %s\n", len(lookup), skipped)

	all := make([]rankeval.Judgment, 0, len(agreement)+len(queries)+len(lookup))
	for _, set := range rankeval.EvalSets {
		all = append(all, bySet[set]...)
	}
	workers := runtime.GOMAXPROCS(0)
	start := time.Now()
	runs, err := rankeval.Replay(ctx, all, workers, r.replay)
	require.NoError(t, err)
	elapsed := time.Since(start)

	runsBySet := map[rankeval.EvalSet][]rankeval.Run{}
	for _, run := range runs {
		runsBySet[run.Judgment.Set] = append(runsBySet[run.Judgment.Set], run)
	}
	report := rankeval.Report{Sets: make([]rankeval.SetReport, 0, len(rankeval.EvalSets))}
	for _, set := range rankeval.EvalSets {
		if why, ok := absent[set]; ok {
			report.Sets = append(report.Sets, rankeval.SetReport{Set: set, Absent: why})
			continue
		}
		report.Sets = append(report.Sets, rankeval.Summarize(set, runsBySet[set]))
	}
	fmt.Print(report.Text())
	if reportOnlyRead {
		fmt.Printf("%s was read report-only: its scores above inform no choice\n", rankeval.SetJudgedHoldout)
	}
	fmt.Printf("replayed %d judgments in %s on %d workers\n", len(runs), elapsed.Round(time.Millisecond), workers)
	printComparisons(t, filepath.Join(data, "baseline.json"), report)
	if dest := os.Getenv("MAGUS_RETRIEVAL_EVAL_BASELINE"); dest != "" {
		writeRetrievalBaseline(t, root, dest, report)
	}
}

func readRetrievalFile[T any](t *testing.T, file string, read func(io.Reader) ([]T, error), howToMake string) []T {
	t.Helper()
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s does not exist; %s", file, howToMake)
	}
	require.NoError(t, err)
	defer f.Close()
	rows, err := read(f)
	require.NoError(t, err)
	return rows
}

// readJudged reads queries.jsonl and qrels.jsonl, or says why the judged sets are
// absent when neither exists yet. One without the other is a broken checkout.
func readJudged(t *testing.T, data string) (queries []rankeval.Query, qrels []rankeval.Qrel, absent string) {
	t.Helper()
	qFile, rFile := filepath.Join(data, "queries.jsonl"), filepath.Join(data, "qrels.jsonl")
	_, qErr := os.Stat(qFile)
	_, rErr := os.Stat(rFile)
	switch {
	case errors.Is(qErr, fs.ErrNotExist) && errors.Is(rErr, fs.ErrNotExist):
		return nil, nil, "not built: plan rows 3 and 4 write queries.jsonl and qrels.jsonl"
	case errors.Is(rErr, fs.ErrNotExist):
		return readRetrievalFile(t, qFile, rankeval.ReadQueries, ""), nil, "not judged: queries.jsonl exists and qrels.jsonl does not"
	}
	queries = readRetrievalFile(t, qFile, rankeval.ReadQueries, "plan row 3 writes it beside qrels.jsonl")
	qrels = readRetrievalFile(t, rFile, rankeval.ReadQrels, "")
	return queries, qrels, ""
}

// readHoldout records a read of the judged-holdout topic set before anything scores
// it, and fails the eval when the ledger refuses.
func readHoldout(t *testing.T, ledger string, queries []rankeval.Query, candidate string, reportOnly bool) {
	t.Helper()
	digest, err := rankeval.DigestQueries(queries, rankeval.SetJudgedHoldout)
	require.NoError(t, err)
	prior, err := rankeval.RecordHoldoutRead(ledger, rankeval.HoldoutRead{
		Set: rankeval.SetJudgedHoldout, Digest: digest, Candidate: candidate, At: time.Now().UTC(), ReportOnly: reportOnly,
	})
	if err != nil {
		t.Fatalf("holdout ledger %s: %v", ledger, err)
	}
	fmt.Printf("holdout ledger %s: recorded a read of %s for %s (%d before it)\n", ledger, digest, candidate, len(prior))
}

// definitionLookup builds the definition-lookup set: each distinct identifier an
// agent-agreement row asked, unless queries.jsonl asks it too, with gold the one file
// the symbol index says defines it, asked through query as definitionQuery words it.
// skipped counts what it left out and why. A dotted identifier names its symbol by the
// last part; every earlier part must appear in the symbol's id.
func definitionLookup(agreement []rankeval.Judgment, judged []rankeval.Query, symbols *knowledge.Graph, known map[string]string) (out []rankeval.Judgment, skipped string) {
	asked := map[string]bool{}
	for _, q := range judged {
		asked[q.Query] = true
	}
	byLabel := map[string][]types.KnowledgeNode{}
	for _, n := range symbols.Nodes() {
		if n.Kind == types.KindSymbol {
			byLabel[n.Label] = append(byLabel[n.Label], n)
		}
	}
	first := map[string]rankeval.Judgment{}
	for _, j := range agreement {
		if j.Shape != rankeval.ShapeIdentifier || asked[j.Query] {
			continue
		}
		if prev, ok := first[j.Query]; !ok || j.Date < prev.Date {
			first[j.Query] = j
		}
	}
	var unresolved, ambiguous int
	for _, ident := range slices.Sorted(maps.Keys(first)) {
		parts := strings.Split(ident, ".")
		files := map[string]bool{}
		for _, n := range byLabel[parts[len(parts)-1]] {
			if !slices.ContainsFunc(parts[:len(parts)-1], func(p string) bool { return !strings.Contains(n.ID, p) }) {
				if file := sourceFile(n.Source); file != "" {
					if _, ok := known[types.KindFile+":"+file]; ok {
						files[types.KindFile+":"+file] = true
					}
				}
			}
		}
		switch len(files) {
		case 0:
			unresolved++
			continue
		case 1:
		default:
			ambiguous++
			continue
		}
		out = append(out, rankeval.Judgment{
			ID: "definition-lookup/" + ident, Query: definitionQuery(ident), Verb: readQuery, Date: first[ident].Date, Source: "symbol-index",
			Set: rankeval.SetDefinitionLookup, Shape: rankeval.ShapeIdentifier, Gold: map[string]int{slices.Collect(maps.Keys(files))[0]: 2},
		})
	}
	return out, fmt.Sprintf("%d with no defining file in the symbol index, %d defined in more than one file", unresolved, ambiguous)
}

// TestDefinitionLookupSeedsTheSymbolLayer pins why definition-lookup once scored
// unknown on every query: asked bare, an identifier is replayed against the domain
// graph, which holds no symbol.
func TestDefinitionLookupSeedsTheSymbolLayer(t *testing.T) {
	symbols := knowledge.NewGraph()
	symbols.AddNode(types.KnowledgeNode{ID: "file:internal/textembed/index.go", Kind: types.KindFile, Source: "internal/textembed/index.go"})
	symbols.AddNode(types.KnowledgeNode{ID: "symbol:readIndex", Kind: types.KindSymbol, Label: "readIndex", Source: "internal/textembed/index.go:131"})
	known := map[string]string{}
	for _, n := range symbols.Nodes() {
		known[n.ID] = n.Source
	}
	agreement := []rankeval.Judgment{{
		ID: "agent/1", Query: "readIndex", Verb: readQuery, Date: "2026-10-01", Source: "harvest",
		Set: rankeval.SetAgentAgreement, Shape: rankeval.ShapeIdentifier, Gold: map[string]int{"file:internal/textembed/index.go": 1},
	}}

	lookup, skipped := definitionLookup(agreement, nil, symbols, known)
	require.Len(t, lookup, 1, skipped)
	require.Equal(t, map[string]int{"file:internal/textembed/index.go": 2}, lookup[0].Gold)
	require.False(t, knowledge.SeedsLazyLayer("readIndex"), "a bare identifier seeds the symbol layer now; ask it bare")
	r := replayer{domain: knowledge.NewGraph(), symbols: symbols}
	require.Same(t, symbols, r.loadedGraph(lookup[0].Query), "%q is replayed against the domain graph", lookup[0].Query)

	probed := func(_ string, seeded bool) knowledge.Coverage {
		return knowledge.Coverage{Seeded: seeded, Probed: true}
	}
	res := searchLoaded(context.Background(), r.loadedGraph(lookup[0].Query), graphRead{Input: lookup[0].Query, Nearest: true}, probed)
	require.Equal(t, types.VerdictFound, res.Out.Answer.Verdict)
	require.Equal(t, "symbol:readIndex", res.Out.Matches[0].ID)
}

// definitionQuery is the query text definition-lookup asks for ident. A bare identifier
// does not seed the symbol layer, so query searches domain entities only and answers
// unknown, pointing at refs; the kind filter routes it to the symbol shards, where the
// ranker under test orders the symbols that match.
func definitionQuery(ident string) string {
	return "kind=" + types.KindSymbol + " " + ident
}

// printComparisons tests now against the committed baseline, set by set and query by
// query, with the randomization test, t-interval and Holm adjustment Compare runs.
func printComparisons(t *testing.T, file string, now rankeval.Report) {
	t.Helper()
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("no baseline at %s; nothing to compare against\n", file)
		return
	}
	require.NoError(t, err)
	var base rankeval.Report
	require.NoError(t, json.UnmarshalStrict(data, &base), "decode %s", file)
	fmt.Printf("against %s:\n%s", file, rankeval.ComparisonText(rankeval.Compare(base, now)))
}

// writeRetrievalBaseline writes now for a person to review and commit as the baseline. A
// relative dest is from the workspace root, since go test runs in the package dir.
func writeRetrievalBaseline(t *testing.T, root, dest string, now rankeval.Report) {
	t.Helper()
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(root, dest)
	}
	data, err := json.MarshalIndent(now, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dest, append(data, '\n'), 0o644))
	fmt.Printf("wrote this report to %s\n", dest)
}
