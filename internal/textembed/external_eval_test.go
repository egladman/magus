package textembed_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/klauspost/compress/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rankeval"
	"github.com/egladman/magus/internal/textrank"
	"github.com/egladman/magus/libs/xdg"
)

// externalSubsets is the one subset list. cosqa-dedup is cosqa with its corpus
// deduplicated before indexing; its column is never compared with cosqa's.
var externalSubsets = []string{"cosqa", "cosqa-dedup", "stackoverflow-qa", "csn-python", "csn-go", "ccr-go"}

// externalDenseSystems are the runs `embed bench` writes. nomic-embed-text-v1.5 stays
// out until its port matches the reference model and its SO-QA gap is reconciled.
var externalDenseSystems = []string{"potion-code-16M-v2", "CodeRankEmbed-query-prompt", "CodeRankEmbed-symmetric"}

const (
	externalDepth = 100
	rrfK          = 60
	bm25K1        = 1.2
	bm25B         = 0.75
)

type externalText struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type externalSubset struct {
	name    string
	queries []externalText
	docs    []externalText
	qrels   map[string]map[string]int
	notes   []string
}

// TestExternalEval is the external benchmark: one harness and one subset list for
// every system, each scored from its TREC run by rankeval.ReadRun, never against a
// model card's numbers. It exports every subset under the cache root's bench/<subset>/,
// runs the lexical arms, fuses each lexical arm with every dense run `embed bench`
// left there, and writes bench/eval.md. MAGUS_EXTERNAL_EVAL names the subsets to run,
// comma separated, or "all"; unset, the test skips, since it reads gigabyte-scale
// downloads no fixture can stand in for.
func TestExternalEval(t *testing.T) {
	want := os.Getenv("MAGUS_EXTERNAL_EVAL")
	if want == "" {
		t.Skip("MAGUS_EXTERNAL_EVAL is unset; set it to \"all\" or a comma-separated subset list to run the external benchmark")
	}
	subsets := externalSubsets
	if want != "all" {
		subsets = strings.Split(want, ",")
		for _, s := range subsets {
			require.Contains(t, externalSubsets, s, "MAGUS_EXTERNAL_EVAL names an unknown subset")
		}
	}
	cache, err := xdg.CacheDir()
	require.NoError(t, err)
	root := filepath.Join(cache, "magus-embed")
	data, bench := filepath.Join(root, "data"), filepath.Join(root, "bench")

	var results []externalResult
	for _, name := range subsets {
		s, err := loadExternalSubset(data, name)
		require.NoError(t, err, name)
		res, err := runExternalSubset(filepath.Join(bench, name), s)
		require.NoError(t, err, name)
		results = append(results, res)
	}
	report := externalReport(bench, results)
	require.NoError(t, os.WriteFile(filepath.Join(bench, "eval.md"), []byte(report), 0o644))
	t.Log("\n" + report)
}

type externalResult struct {
	subset  externalSubset
	digest  string
	scores  map[string]externalScore
	missing []string
}

// runExternalSubset exports s under dir, writes the lexical and fused runs into
// dir/runs/<digest>/ and scores every run there. The digest is the first 16 hex of the
// sha256 of queries.jsonl followed by corpus.jsonl, which `embed bench` computes the
// same way, so a run of an older export is never scored against this one.
func runExternalSubset(dir string, s externalSubset) (externalResult, error) {
	digest, err := exportExternalSubset(dir, s)
	if err != nil {
		return externalResult{}, err
	}
	runs := filepath.Join(dir, "runs", digest)
	if err := os.MkdirAll(runs, 0o755); err != nil {
		return externalResult{}, err
	}
	lexical := map[string]func(string) []string{"bm25": luceneTokens, "bm25-magustok": textrank.Tokens}
	present := map[string]rankeval.TRECRun{}
	for _, name := range slices.Sorted(maps.Keys(lexical)) {
		run := runBM25(name, s, lexical[name])
		if err := writeExternalRun(runs, run); err != nil {
			return externalResult{}, err
		}
		present[name] = run
	}
	res := externalResult{subset: s, digest: digest, scores: map[string]externalScore{}}
	for _, name := range externalDenseSystems {
		run, err := readExternalRun(filepath.Join(runs, name+".trec"))
		if errors.Is(err, os.ErrNotExist) {
			res.missing = append(res.missing, name)
			continue
		}
		if err != nil {
			return externalResult{}, err
		}
		present[name] = run
		for _, lex := range slices.Sorted(maps.Keys(lexical)) {
			fused := fuseReciprocalRank("rrf-"+lex+"+"+name, present[lex], run)
			if err := writeExternalRun(runs, fused); err != nil {
				return externalResult{}, err
			}
			present[fused.Tag] = fused
		}
	}
	for name := range present {
		back, err := readExternalRun(filepath.Join(runs, name+".trec"))
		if err != nil {
			return externalResult{}, err
		}
		if res.scores[name], err = scoreExternalRun(s, back); err != nil {
			return externalResult{}, fmt.Errorf("%s: %w", name, err)
		}
	}
	return res, nil
}

func exportExternalSubset(dir string, s externalSubset) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	sum := sha256.New()
	for _, f := range []struct {
		name  string
		texts []externalText
	}{{"queries.jsonl", s.queries}, {"corpus.jsonl", s.docs}} {
		var b bytes.Buffer
		for _, t := range f.texts {
			line, err := json.Marshal(t)
			if err != nil {
				return "", err
			}
			b.Write(line)
			b.WriteByte('\n')
		}
		sum.Write(b.Bytes())
		path := filepath.Join(dir, f.name)
		if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, b.Bytes()) {
			continue
		}
		if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(sum.Sum(nil))[:16], nil
}

func writeExternalRun(dir string, run rankeval.TRECRun) error {
	var b bytes.Buffer
	if err := rankeval.WriteRun(&b, run); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, run.Tag+".trec"), b.Bytes(), 0o644)
}

func readExternalRun(path string) (rankeval.TRECRun, error) {
	f, err := os.Open(path)
	if err != nil {
		return rankeval.TRECRun{}, err
	}
	defer f.Close()
	run, err := rankeval.ReadRun(f)
	if err != nil {
		return rankeval.TRECRun{}, fmt.Errorf("%s: %w", path, err)
	}
	return run, nil
}

type externalScore struct {
	ndcg, ndcgLow, ndcgHigh     float64
	recall, recallLow, recallHi float64
	success                     bool
}

// scoreExternalRun scores run against s: nDCG@10 with its t interval, and recall@100,
// which is success@100 with a Wilson interval when every query has one relevant doc. A
// query the run leaves out scores 0. A query or doc id s does not hold is an error: the
// run belongs to another export.
func scoreExternalRun(s externalSubset, run rankeval.TRECRun) (externalScore, error) {
	docs := make(map[string]bool, len(s.docs))
	for _, d := range s.docs {
		docs[d.ID] = true
	}
	for q, hits := range run.Queries {
		if s.qrels[q] == nil {
			return externalScore{}, fmt.Errorf("run ranks query %s, which %s does not hold", q, s.name)
		}
		for _, h := range hits {
			if !docs[h.Doc] {
				return externalScore{}, fmt.Errorf("run ranks doc %s, which %s does not hold", h.Doc, s.name)
			}
		}
	}
	single := true
	var ndcg, recall []float64
	found := 0
	for _, q := range s.queries {
		gold := s.qrels[q.ID]
		single = single && len(gold) == 1
		ranked := make([]string, 0, len(run.Queries[q.ID]))
		for _, h := range run.Queries[q.ID] {
			ranked = append(ranked, h.Doc)
		}
		ndcg = append(ndcg, rankeval.NDCGAt(ranked, gold, 10))
		r := rankeval.RecallAt(ranked, gold, externalDepth)
		recall = append(recall, r)
		if r == 1 {
			found++
		}
	}
	var sc externalScore
	sc.ndcg, sc.ndcgLow, sc.ndcgHigh = rankeval.MeanInterval(ndcg)
	if single {
		sc.success = true
		sc.recall = float64(found) / float64(len(recall))
		sc.recallLow, sc.recallHi = rankeval.WilsonInterval(found, len(recall))
	} else {
		sc.recall, sc.recallLow, sc.recallHi = rankeval.MeanInterval(recall)
	}
	return sc, nil
}

// runBM25 ranks every query of s with Okapi BM25 (Robertson and Zaragoza 2009) at k1
// 1.2 and b 0.75, idf ln(1 + (N-df+0.5)/(df+0.5)), each distinct query term once, in
// float64 and by score alone: no matched-term ordering, which the runtime ranker has
// dropped. Docs sharing no term with the query are not ranked.
func runBM25(tag string, s externalSubset, analyze func(string) []string) rankeval.TRECRun {
	ix := newBM25Index(s.docs, analyze)
	run := rankeval.TRECRun{Tag: tag, Queries: make(map[string][]rankeval.Hit, len(s.queries))}
	hits := make([][]rankeval.Hit, len(s.queries))
	var wg sync.WaitGroup
	next := make(chan int)
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			acc := make([]float64, len(s.docs))
			for i := range next {
				hits[i] = ix.search(s.queries[i].Text, analyze, acc)
			}
		})
	}
	for i := range s.queries {
		next <- i
	}
	close(next)
	wg.Wait()
	for i, q := range s.queries {
		if len(hits[i]) > 0 {
			run.Queries[q.ID] = hits[i]
		}
	}
	return run
}

type bm25Posting struct {
	doc    int32
	weight float64
}

type bm25Index struct {
	ids      []string
	postings map[string][]bm25Posting
}

func newBM25Index(docs []externalText, analyze func(string) []string) *bm25Index {
	ix := &bm25Index{ids: make([]string, len(docs)), postings: map[string][]bm25Posting{}}
	lengths := make([]float64, len(docs))
	counts := make([]map[string]int, len(docs))
	var total float64
	for i, d := range docs {
		ix.ids[i] = d.ID
		tokens := analyze(d.Text)
		lengths[i] = float64(len(tokens))
		total += lengths[i]
		counts[i] = map[string]int{}
		for _, tok := range tokens {
			counts[i][tok]++
		}
	}
	avg := total / float64(max(1, len(docs)))
	df := map[string]int{}
	for _, c := range counts {
		for tok := range c {
			df[tok]++
		}
	}
	n := float64(len(docs))
	for i, c := range counts {
		norm := 1 - bm25B + bm25B*lengths[i]/avg
		for _, tok := range slices.Sorted(maps.Keys(c)) {
			tf, idf := float64(c[tok]), math.Log1p((n-float64(df[tok])+0.5)/(float64(df[tok])+0.5))
			ix.postings[tok] = append(ix.postings[tok], bm25Posting{doc: int32(i), weight: idf * tf / (tf + bm25K1*norm)})
		}
	}
	return ix
}

// search scores query into acc, which is all zeros on entry and on return.
func (ix *bm25Index) search(query string, analyze func(string) []string, acc []float64) []rankeval.Hit {
	seen := map[string]bool{}
	var touched []int32
	for _, tok := range analyze(query) {
		if seen[tok] {
			continue
		}
		seen[tok] = true
		for _, p := range ix.postings[tok] {
			if acc[p.doc] == 0 {
				touched = append(touched, p.doc)
			}
			acc[p.doc] += p.weight
		}
	}
	top := rankeval.Top(func(yield func(rankeval.Hit) bool) {
		for _, d := range touched {
			if !yield(rankeval.Hit{Doc: ix.ids[d], Score: acc[d]}) {
				return
			}
		}
	}, externalDepth)
	for _, d := range touched {
		acc[d] = 0
	}
	return top
}

// luceneStopWords is Lucene's English stop set (EnglishAnalyzer.ENGLISH_STOP_WORDS_SET).
var luceneStopWords = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an and are as at be but by for if in into is it no not of on or such that the their then there these they this to was will with") {
		luceneStopWords[w] = true
	}
}

// luceneTokens lowercases the runs of letters, digits and underscores in text and drops
// Lucene's English stop words; nothing is stemmed.
func luceneTokens(text string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !isWordRune(r) }) {
		if w = strings.ToLower(w); !luceneStopWords[w] {
			out = append(out, w)
		}
	}
	return out
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// fuseReciprocalRank fuses runs by reciprocal rank (Cormack, Clarke and Buettcher,
// SIGIR 2009): each doc scores the sum of 1/(60+rank) over the runs that rank it, rank
// from 1, and the fused run keeps the best 100 by rankeval.CompareHits.
func fuseReciprocalRank(tag string, runs ...rankeval.TRECRun) rankeval.TRECRun {
	fused := rankeval.TRECRun{Tag: tag, Queries: map[string][]rankeval.Hit{}}
	scores := map[string]map[string]float64{}
	for _, run := range runs {
		for q, hits := range run.Queries {
			if scores[q] == nil {
				scores[q] = map[string]float64{}
			}
			for i, h := range hits[:min(externalDepth, len(hits))] {
				scores[q][h.Doc] += 1 / float64(rrfK+i+1)
			}
		}
	}
	for q, docs := range scores {
		hits := make([]rankeval.Hit, 0, len(docs))
		for d, s := range docs {
			hits = append(hits, rankeval.Hit{Doc: d, Score: s})
		}
		fused.Queries[q] = rankeval.Rank(hits, externalDepth)
	}
	return fused
}

func externalReport(bench string, results []externalResult) string {
	var b strings.Builder
	b.WriteString("# External retrieval benchmark\n\n")
	b.WriteString("Every system runs in one harness over one subset list and is scored from its TREC run by `rankeval.ReadRun`; no number here is compared with a model card's.\n\n")
	b.WriteString("- Ties go by score, then the FNV-1a hash of the doc id (`rankeval.CompareHits`), in every arm.\n")
	b.WriteString("- BM25: k1 1.2, b 0.75, score order with no matched-term ordering, float64. `bm25` uses Lucene's English analyzer without stemming; `bm25-magustok` uses magus's `textrank.Tokens`.\n")
	b.WriteString("- Fusion: reciprocal rank, k = 60, over the top 100 of each list.\n")
	b.WriteString("- cosqa-dedup deduplicates cosqa's corpus by exact text before indexing. Read its column apart from cosqa's, never against it.\n")
	b.WriteString("- csn-go queries lose the declared name they start with. csn-python and csn-go run text to code; ccr-go keeps CoIR's code-to-code direction over the test partition.\n")
	b.WriteString("- nomic-embed-text-v1.5 is left out until its port matches the reference model and its SO-QA gap is reconciled.\n")
	b.WriteString("- Intervals are 95%: Student t for nDCG@10 and recall@100, Wilson for success@100 (recall@100 where every query has one relevant doc).\n\n")

	systems := map[string]bool{}
	for _, r := range results {
		for name := range r.scores {
			systems[name] = true
		}
	}
	names := slices.Sorted(maps.Keys(systems))
	header := "| system |"
	rule := "|---|"
	for _, r := range results {
		header += " " + r.subset.name + " |"
		rule += "---|"
	}
	for _, metric := range []string{"nDCG@10", "success@100 or recall@100"} {
		fmt.Fprintf(&b, "## %s\n\n%s\n%s\n", metric, header, rule)
		for _, name := range names {
			b.WriteString("| " + name + " |")
			for _, r := range results {
				sc, ok := r.scores[name]
				switch {
				case !ok:
					b.WriteString(" not run |")
				case metric == "nDCG@10":
					fmt.Fprintf(&b, " %s |", percentInterval(sc.ndcg, sc.ndcgLow, sc.ndcgHigh))
				default:
					kind := "R"
					if sc.success {
						kind = "S"
					}
					fmt.Fprintf(&b, " %s %s |", kind, percentInterval(sc.recall, sc.recallLow, sc.recallHi))
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("S is success@100, R is recall@100.\n\n")

	b.WriteString("## Ceiling\n\nCodeRankEmbed with its query prompt is the teacher as published; prompt-less and symmetric, as the student is distilled, it is the student's ceiling.\n\n")
	b.WriteString("| subset | CodeRankEmbed-query-prompt nDCG@10 | CodeRankEmbed-symmetric nDCG@10 |\n|---|---|---|\n")
	for _, r := range results {
		b.WriteString("| " + r.subset.name + " |")
		for _, name := range []string{"CodeRankEmbed-query-prompt", "CodeRankEmbed-symmetric"} {
			if sc, ok := r.scores[name]; ok {
				fmt.Fprintf(&b, " %s |", percentInterval(sc.ndcg, sc.ndcgLow, sc.ndcgHigh))
			} else {
				b.WriteString(" not run |")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Subsets\n\n")
	for _, r := range results {
		fmt.Fprintf(&b, "- %s: %d queries, %d docs, export %s.", r.subset.name, len(r.subset.queries), len(r.subset.docs), r.digest)
		for _, n := range r.subset.notes {
			b.WriteString(" " + n)
		}
		b.WriteString("\n")
	}
	var missing []string
	for _, r := range results {
		for _, name := range r.missing {
			model := name
			if strings.HasPrefix(name, "CodeRankEmbed") {
				model = "CodeRankEmbed"
			}
			missing = append(missing, fmt.Sprintf("- %s %s: `embed bench -subset %s -model %s`", r.subset.name, name, filepath.Join(bench, r.subset.name), model))
		}
	}
	if len(missing) > 0 {
		b.WriteString("\n## Not run\n\n" + strings.Join(slices.Compact(missing), "\n") + "\n")
	}
	return b.String()
}

func percentInterval(v, lo, hi float64) string {
	return fmt.Sprintf("%.2f [%.2f, %.2f]", 100*v, 100*lo, 100*hi)
}

// loadExternalSubset reads one subset from the CoIR parquet files under data. The CSN
// subsets run CoIR's code-to-text task the other way, a docstring's first paragraph to
// the code with its docstring removed, the direction magus queries go.
func loadExternalSubset(data, name string) (externalSubset, error) {
	switch name {
	case "cosqa", "stackoverflow-qa":
		return loadCoIRSubset(data, name)
	case "cosqa-dedup":
		s, err := loadCoIRSubset(data, "cosqa")
		if err != nil {
			return externalSubset{}, err
		}
		return dedupeExternalCorpus(s), nil
	case "csn-python":
		return loadCodeSearchNet(data, "python")
	case "csn-go":
		return loadCodeSearchNet(data, "go")
	case "ccr-go":
		return loadCodeToCode(data, "go")
	}
	return externalSubset{}, fmt.Errorf("unknown external subset %q", name)
}

func loadCoIRSubset(data, name string) (externalSubset, error) {
	corpus, err := readParquetGlob(filepath.Join(data, name, "corpus", "*.parquet"), "_id", "text")
	if err != nil {
		return externalSubset{}, err
	}
	queries, err := readParquetGlob(filepath.Join(data, name, "queries", "*.parquet"), "_id", "text")
	if err != nil {
		return externalSubset{}, err
	}
	qrels, err := readParquetGlob(filepath.Join(data, name, "data", "test-*.parquet"), "query-id", "corpus-id", "score")
	if err != nil {
		return externalSubset{}, err
	}
	s := externalSubset{name: name, qrels: map[string]map[string]int{}}
	for i := range qrels[0] {
		grade, err := strconv.Atoi(qrels[2][i])
		if err != nil {
			return externalSubset{}, fmt.Errorf("%s qrels row %d: score %q", name, i, qrels[2][i])
		}
		addQrel(s.qrels, qrels[0][i], qrels[1][i], grade)
	}
	s.docs = pairTexts(corpus[0], corpus[1], nil)
	s.queries = pairTexts(queries[0], queries[1], func(id string) bool { return s.qrels[id] != nil })
	return s, checkQrels(s)
}

func loadCodeSearchNet(data, lang string) (externalSubset, error) {
	dir := filepath.Join(data, "CodeSearchNet")
	code, err := readParquetGlob(filepath.Join(dir, lang+"-queries", "*.parquet"), "_id", "text")
	if err != nil {
		return externalSubset{}, err
	}
	text, err := readParquetGlob(filepath.Join(dir, lang+"-corpus", "*.parquet"), "_id", "text")
	if err != nil {
		return externalSubset{}, err
	}
	qrels, err := readParquetGlob(filepath.Join(dir, lang+"-qrels", "test-*.parquet"), "query-id", "corpus-id")
	if err != nil {
		return externalSubset{}, err
	}
	codeOf, textOf := columnMap(code), columnMap(text)
	s := externalSubset{name: "csn-" + lang, qrels: map[string]map[string]int{}}
	queries, docs := map[string]string{}, map[string]string{}
	var empty, stripped, named int
	for i := range qrels[0] {
		cq, td := qrels[0][i], qrels[1][i]
		body, ok := codeOf[cq]
		doc, ok2 := textOf[td]
		if !ok || !ok2 {
			return externalSubset{}, fmt.Errorf("%s qrels row %d names %s or %s, which the files do not hold", s.name, i, cq, td)
		}
		q := firstParagraph(doc, lang)
		switch lang {
		case "python":
			body = stripPythonDocstring(body)
		case "go":
			var cut bool
			var parts []string
			q, cut, parts = stripGoName(q, body)
			if cut {
				stripped++
			}
			if holdsNameToken(q, parts) {
				named++
			}
		}
		if q == "" {
			empty++
			continue
		}
		queries[td], docs[cq] = q, body
		addQrel(s.qrels, td, cq, 1)
	}
	s.queries = sortedTexts(queries)
	s.docs = sortedTexts(docs)
	s.notes = append(s.notes, fmt.Sprintf("%d rows dropped for an empty query.", empty))
	if lang == "go" {
		s.notes = append(s.notes, fmt.Sprintf("The leading declared name was stripped from %d queries; %d of %d queries (%.1f%%) still hold a token of the gold function's name.",
			stripped, named, len(s.queries), 100*float64(named)/float64(len(s.queries))))
	}
	return s, checkQrels(s)
}

func loadCodeToCode(data, lang string) (externalSubset, error) {
	dir := filepath.Join(data, "CodeSearchNet-ccr")
	queries, err := readParquetGlob(filepath.Join(dir, lang+"-queries", "*.parquet"), "_id", "text")
	if err != nil {
		return externalSubset{}, err
	}
	corpus, err := readParquetGlob(filepath.Join(dir, lang+"-corpus", "*.parquet"), "_id", "text", "partition")
	if err != nil {
		return externalSubset{}, err
	}
	qrels, err := readParquetGlob(filepath.Join(dir, lang+"-qrels", "test-*.parquet"), "query-id", "corpus-id", "score")
	if err != nil {
		return externalSubset{}, err
	}
	s := externalSubset{name: "ccr-" + lang, qrels: map[string]map[string]int{}}
	gold := map[string]bool{}
	for i := range qrels[0] {
		grade, err := strconv.Atoi(qrels[2][i])
		if err != nil {
			return externalSubset{}, fmt.Errorf("%s qrels row %d: score %q", s.name, i, qrels[2][i])
		}
		addQrel(s.qrels, qrels[0][i], qrels[1][i], grade)
		gold[qrels[1][i]] = true
	}
	test := map[string]bool{}
	for i, id := range corpus[0] {
		if corpus[2][i] == "test" || gold[id] {
			test[id] = true
		}
	}
	s.docs = pairTexts(corpus[0], corpus[1], func(id string) bool { return test[id] })
	s.queries = pairTexts(queries[0], queries[1], func(id string) bool { return s.qrels[id] != nil })
	s.notes = append(s.notes, "The corpus is the test partition plus every gold doc.")
	return s, checkQrels(s)
}

func addQrel(qrels map[string]map[string]int, q, d string, grade int) {
	if grade <= 0 {
		return
	}
	if qrels[q] == nil {
		qrels[q] = map[string]int{}
	}
	qrels[q][d] = max(qrels[q][d], grade)
}

// checkQrels refuses a subset whose qrels name a query or doc it does not hold.
func checkQrels(s externalSubset) error {
	queries, docs := map[string]bool{}, map[string]bool{}
	for _, q := range s.queries {
		queries[q.ID] = true
	}
	for _, d := range s.docs {
		docs[d.ID] = true
	}
	for q, gold := range s.qrels {
		if !queries[q] {
			return fmt.Errorf("%s: qrels name query %s, which has no text", s.name, q)
		}
		for d := range gold {
			if !docs[d] {
				return fmt.Errorf("%s: qrels name doc %s, which is not in the corpus", s.name, d)
			}
		}
	}
	if len(s.queries) < 2 {
		return fmt.Errorf("%s holds %d scored queries", s.name, len(s.queries))
	}
	return nil
}

// pairTexts zips ids with texts, keeping the ids keep accepts (all when nil), sorted by
// id with the first text of a repeated id.
func pairTexts(ids, texts []string, keep func(string) bool) []externalText {
	m := map[string]string{}
	for i, id := range ids {
		if _, dup := m[id]; !dup && (keep == nil || keep(id)) {
			m[id] = texts[i]
		}
	}
	return sortedTexts(m)
}

func sortedTexts(m map[string]string) []externalText {
	out := make([]externalText, 0, len(m))
	for _, id := range slices.Sorted(maps.Keys(m)) {
		out = append(out, externalText{ID: id, Text: m[id]})
	}
	return out
}

func columnMap(cols [][]string) map[string]string {
	m := make(map[string]string, len(cols[0]))
	for i, id := range cols[0] {
		m[id] = cols[1][i]
	}
	return m
}

// dedupeExternalCorpus collapses docs with identical text onto the smallest id among
// them before indexing, and maps the qrels onto it at the highest grade. Collapsing
// after ranking instead would shrink a top-100 list below 100 unique docs.
func dedupeExternalCorpus(s externalSubset) externalSubset {
	canonical, first := map[string]string{}, map[string]string{}
	var docs []externalText
	for _, d := range s.docs {
		if id, ok := first[d.Text]; ok {
			canonical[d.ID] = id
			continue
		}
		first[d.Text], canonical[d.ID] = d.ID, d.ID
		docs = append(docs, d)
	}
	qrels := map[string]map[string]int{}
	for q, gold := range s.qrels {
		for d, g := range gold {
			addQrel(qrels, q, canonical[d], g)
		}
	}
	return externalSubset{
		name: s.name + "-dedup", queries: s.queries, docs: docs, qrels: qrels,
		notes: append(slices.Clone(s.notes), fmt.Sprintf("%d docs collapsed onto %d distinct texts.", len(s.docs), len(docs))),
	}
}

var (
	goLineComment = regexp.MustCompile(`^\s*//\s?`)
	blankLine     = regexp.MustCompile(`\n\s*\n`)
	goFuncName    = regexp.MustCompile(`(?m)^\s*func\s*(?:\([^)]*\)\s*)?([\pL_][\pL\pN_]*)`)
	docstringOpen = regexp.MustCompile(`"""|'''`)
)

// firstParagraph is a doc's first paragraph with white space collapsed; Go's // line
// markers are removed first.
func firstParagraph(doc, lang string) string {
	lines := strings.Split(strings.TrimSpace(doc), "\n")
	if lang == "go" {
		for i, l := range lines {
			lines[i] = goLineComment.ReplaceAllString(l, "")
		}
	}
	para := blankLine.Split(strings.TrimSpace(strings.Join(lines, "\n")), 2)[0]
	return strings.Join(strings.Fields(para), " ")
}

// stripPythonDocstring removes a function's docstring: the first triple-quoted string,
// when the code before it ends in the def's colon.
func stripPythonDocstring(code string) string {
	m := docstringOpen.FindStringIndex(code)
	if m == nil {
		return code
	}
	head := strings.TrimRightFunc(strings.TrimRight(code[:m[0]], "rRuUbB"), unicode.IsSpace)
	if !strings.HasSuffix(head, ":") {
		return code
	}
	end := strings.Index(code[m[1]:], code[m[0]:m[1]])
	if end < 0 {
		return code
	}
	return head + "\n" + strings.TrimLeft(code[m[1]+end+3:], "\n")
}

// stripGoName removes the declared name a Go doc comment starts with, by convention the
// function's own name, so the query is not an identifier lookup: either the name itself
// or its words in order, matched without case. It returns the query, whether it cut,
// and the name's lowercased words.
func stripGoName(query, code string) (string, bool, []string) {
	m := goFuncName.FindStringSubmatch(code)
	if m == nil {
		return query, false, nil
	}
	name := m[1]
	parts := textrank.SplitIdentifier(name)
	words := strings.Fields(query)
	bare := func(w string) string {
		return strings.TrimFunc(w, func(r rune) bool { return !isWordRune(r) })
	}
	cut := 0
	switch {
	case len(words) > 0 && strings.EqualFold(bare(words[0]), name):
		cut = 1
	case len(parts) > 1 && len(words) >= len(parts):
		cut = len(parts)
		for i, p := range parts {
			if !strings.EqualFold(bare(words[i]), p) {
				cut = 0
				break
			}
		}
	}
	return strings.Join(words[cut:], " "), cut > 0, append(parts, strings.ToLower(name))
}

// holdsNameToken reports whether any of query's tokens is one of name's words.
func holdsNameToken(query string, name []string) bool {
	for _, tok := range textrank.Tokens(query) {
		if slices.Contains(name, tok) {
			return true
		}
	}
	return false
}

// readParquetGlob reads the named top-level columns from every parquet file matching
// pattern, in file name order, as strings: byte arrays verbatim, integers in decimal.
// No file, a missing or nested column, or a null value is an error.
func readParquetGlob(pattern string, columns ...string) ([][]string, error) {
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no parquet file matches %s", pattern)
	}
	slices.Sort(files)
	out := make([][]string, len(columns))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		cols, err := readParquet(b, columns)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for i := range out {
			out[i] = append(out[i], cols[i]...)
		}
	}
	return out, nil
}

// The parquet reader below covers what the CoIR files use (Apache Parquet format
// 2.x, as written by parquet-cpp-arrow): flat required or optional columns of
// BYTE_ARRAY, INT32, INT64 or DOUBLE; PLAIN and dictionary encodings; data pages v1
// and v2; uncompressed, Snappy or Zstandard pages. Anything else is an error.

type parquetLeaf struct {
	name         string
	physical     int
	maxDef       int
	nestedOrList bool
}

type parquetChunk struct {
	path       string
	physical   int
	codec      int
	values     int64
	dataOffset int64
	dictOffset int64
}

func readParquet(b []byte, columns []string) ([][]string, error) {
	if len(b) < 12 || string(b[:4]) != "PAR1" || string(b[len(b)-4:]) != "PAR1" {
		return nil, errors.New("not a parquet file")
	}
	metaLen := int(binary.LittleEndian.Uint32(b[len(b)-8:]))
	if metaLen > len(b)-12 {
		return nil, errors.New("footer length past the file")
	}
	leaves, groups, err := parseParquetFooter(b[len(b)-8-metaLen : len(b)-8])
	if err != nil {
		return nil, fmt.Errorf("footer: %w", err)
	}
	out := make([][]string, len(columns))
	for i, name := range columns {
		leaf, ok := leaves[name]
		if !ok || leaf.nestedOrList {
			return nil, fmt.Errorf("no flat column %q", name)
		}
		for _, chunks := range groups {
			c, ok := chunks[name]
			if !ok {
				return nil, fmt.Errorf("a row group lacks column %q", name)
			}
			vals, err := readParquetChunk(b, c, leaf.maxDef)
			if err != nil {
				return nil, fmt.Errorf("column %q: %w", name, err)
			}
			out[i] = append(out[i], vals...)
		}
	}
	for i := range out {
		if len(out[i]) != len(out[0]) {
			return nil, fmt.Errorf("column %q holds %d values, %q %d", columns[i], len(out[i]), columns[0], len(out[0]))
		}
	}
	return out, nil
}

// parseParquetFooter decodes FileMetaData: the leaf columns by dotted path, and per
// row group its column chunks by the same path.
func parseParquetFooter(meta []byte) (map[string]parquetLeaf, []map[string]parquetChunk, error) {
	r := &thriftReader{b: meta}
	type element struct {
		name                       string
		physical, repetition, kids int
	}
	var schema []element
	var groups []map[string]parquetChunk
	r.structure(func(id int16, typ byte) {
		switch id {
		case 2:
			r.list(func(byte) {
				e := element{physical: -1}
				r.structure(func(id int16, typ byte) {
					switch id {
					case 1:
						e.physical = int(r.integer())
					case 3:
						e.repetition = int(r.integer())
					case 4:
						e.name = string(r.binary())
					case 5:
						e.kids = int(r.integer())
					default:
						r.skip(typ)
					}
				})
				schema = append(schema, e)
			})
		case 4:
			r.list(func(byte) {
				chunks := map[string]parquetChunk{}
				r.structure(func(id int16, typ byte) {
					if id != 1 {
						r.skip(typ)
						return
					}
					r.list(func(byte) {
						r.structure(func(id int16, typ byte) {
							if id != 3 {
								r.skip(typ)
								return
							}
							c := parseParquetColumnMeta(r)
							chunks[c.path] = c
						})
					})
				})
				groups = append(groups, chunks)
			})
		default:
			r.skip(typ)
		}
	})
	if r.err != nil {
		return nil, nil, r.err
	}
	if len(schema) == 0 {
		return nil, nil, errors.New("no schema")
	}
	leaves := map[string]parquetLeaf{}
	next := 1
	var walk func(prefix string, kids, def int, nested bool) error
	walk = func(prefix string, kids, def int, nested bool) error {
		for range kids {
			if next >= len(schema) {
				return errors.New("schema ends inside a group")
			}
			e := schema[next]
			next++
			path, d := e.name, def
			if prefix != "" {
				path = prefix + "." + e.name
			}
			if e.repetition == 1 {
				d++
			}
			if e.kids > 0 {
				if err := walk(path, e.kids, d, true); err != nil {
					return err
				}
				continue
			}
			leaves[path] = parquetLeaf{name: path, physical: e.physical, maxDef: d, nestedOrList: nested || e.repetition == 2}
		}
		return nil
	}
	return leaves, groups, walk("", schema[0].kids, 0, false)
}

func parseParquetColumnMeta(r *thriftReader) parquetChunk {
	c := parquetChunk{}
	var path []string
	r.structure(func(id int16, typ byte) {
		switch id {
		case 1:
			c.physical = int(r.integer())
		case 3:
			r.list(func(byte) { path = append(path, string(r.binary())) })
		case 4:
			c.codec = int(r.integer())
		case 5:
			c.values = r.integer()
		case 9:
			c.dataOffset = r.integer()
		case 11:
			c.dictOffset = r.integer()
		default:
			r.skip(typ)
		}
	})
	c.path = strings.Join(path, ".")
	return c
}

func readParquetChunk(b []byte, c parquetChunk, maxDef int) ([]string, error) {
	pos := c.dataOffset
	if c.dictOffset > 0 && c.dictOffset < pos {
		pos = c.dictOffset
	}
	var dict, out []string
	for read := int64(0); read < c.values; {
		if pos < 0 || pos >= int64(len(b)) {
			return nil, fmt.Errorf("page at %d is past the file", pos)
		}
		r := &thriftReader{b: b[pos:]}
		h := parsePageHeader(r)
		if r.err != nil {
			return nil, fmt.Errorf("page header at %d: %w", pos, r.err)
		}
		start := pos + int64(r.pos)
		end := start + int64(h.compressed)
		if end > int64(len(b)) || h.compressed < 0 {
			return nil, fmt.Errorf("page at %d runs past the file", pos)
		}
		body := b[start:end]
		pos = end
		switch h.kind {
		case 2:
			raw, err := decompressPage(c.codec, body, h.uncompressed)
			if err != nil {
				return nil, err
			}
			if dict, _, err = decodePlain(raw, c.physical, h.values); err != nil {
				return nil, fmt.Errorf("dictionary page: %w", err)
			}
		case 0, 3:
			vals, err := decodeDataPage(h, body, c, maxDef, dict)
			if err != nil {
				return nil, err
			}
			out = append(out, vals...)
			read += int64(h.values)
		case 1:
		default:
			return nil, fmt.Errorf("page type %d", h.kind)
		}
	}
	return out, nil
}

type pageHeader struct {
	kind, uncompressed, compressed int
	values, encoding               int
	defBytes, repBytes             int
	compressedV2                   bool
}

func parsePageHeader(r *thriftReader) pageHeader {
	h := pageHeader{compressedV2: true}
	r.structure(func(id int16, typ byte) {
		switch id {
		case 1:
			h.kind = int(r.integer())
		case 2:
			h.uncompressed = int(r.integer())
		case 3:
			h.compressed = int(r.integer())
		case 5, 7:
			r.structure(func(id int16, typ byte) {
				switch id {
				case 1:
					h.values = int(r.integer())
				case 2:
					h.encoding = int(r.integer())
				default:
					r.skip(typ)
				}
			})
		case 8:
			r.structure(func(id int16, typ byte) {
				switch id {
				case 1:
					h.values = int(r.integer())
				case 4:
					h.encoding = int(r.integer())
				case 5:
					h.defBytes = int(r.integer())
				case 6:
					h.repBytes = int(r.integer())
				case 7:
					h.compressedV2 = typ == thriftTrue
				default:
					r.skip(typ)
				}
			})
		default:
			r.skip(typ)
		}
	})
	return h
}

func decodeDataPage(h pageHeader, body []byte, c parquetChunk, maxDef int, dict []string) ([]string, error) {
	var levels, values []byte
	if h.kind == 3 {
		if h.repBytes != 0 {
			return nil, errors.New("repetition levels in a flat column")
		}
		if h.defBytes > len(body) {
			return nil, errors.New("definition levels past the page")
		}
		levels, values = body[:h.defBytes], body[h.defBytes:]
		if h.compressedV2 {
			raw, err := decompressPage(c.codec, values, h.uncompressed-h.defBytes)
			if err != nil {
				return nil, err
			}
			values = raw
		}
	} else {
		raw, err := decompressPage(c.codec, body, h.uncompressed)
		if err != nil {
			return nil, err
		}
		values = raw
		if maxDef > 0 {
			if len(raw) < 4 {
				return nil, errors.New("definition levels past the page")
			}
			n := int(binary.LittleEndian.Uint32(raw))
			if 4+n > len(raw) {
				return nil, errors.New("definition levels past the page")
			}
			levels, values = raw[4:4+n], raw[4+n:]
		}
	}
	present := h.values
	if maxDef > 0 {
		defs, _, err := decodeHybrid(levels, bitWidth(maxDef), h.values)
		if err != nil {
			return nil, fmt.Errorf("definition levels: %w", err)
		}
		for i, d := range defs {
			if int(d) != maxDef {
				return nil, fmt.Errorf("null at value %d", i)
			}
		}
	}
	switch h.encoding {
	case 0:
		vals, _, err := decodePlain(values, c.physical, present)
		return vals, err
	case 2, 8:
		if len(values) == 0 {
			return nil, errors.New("dictionary indexes missing")
		}
		idx, _, err := decodeHybrid(values[1:], int(values[0]), present)
		if err != nil {
			return nil, fmt.Errorf("dictionary indexes: %w", err)
		}
		out := make([]string, len(idx))
		for i, k := range idx {
			if int(k) >= len(dict) {
				return nil, fmt.Errorf("dictionary index %d of %d entries", k, len(dict))
			}
			out[i] = dict[k]
		}
		return out, nil
	}
	return nil, fmt.Errorf("encoding %d", h.encoding)
}

var zstdDecoder, _ = zstd.NewReader(nil)

func decompressPage(codec int, body []byte, size int) ([]byte, error) {
	var out []byte
	var err error
	switch codec {
	case 0:
		out = body
	case 1:
		out, err = snappy.Decode(nil, body)
	case 6:
		out, err = zstdDecoder.DecodeAll(body, nil)
	default:
		return nil, fmt.Errorf("compression codec %d", codec)
	}
	if err != nil {
		return nil, err
	}
	if len(out) != size {
		return nil, fmt.Errorf("page decompressed to %d bytes, its header says %d", len(out), size)
	}
	return out, nil
}

// decodePlain decodes n PLAIN values and returns them with the bytes consumed.
func decodePlain(b []byte, physical, n int) ([]string, int, error) {
	out := make([]string, 0, n)
	pos := 0
	for range n {
		switch physical {
		case 1:
			if pos+4 > len(b) {
				return nil, 0, errors.New("INT32 past the page")
			}
			out = append(out, strconv.FormatInt(int64(int32(binary.LittleEndian.Uint32(b[pos:]))), 10))
			pos += 4
		case 2:
			if pos+8 > len(b) {
				return nil, 0, errors.New("INT64 past the page")
			}
			out = append(out, strconv.FormatInt(int64(binary.LittleEndian.Uint64(b[pos:])), 10))
			pos += 8
		case 5:
			if pos+8 > len(b) {
				return nil, 0, errors.New("DOUBLE past the page")
			}
			out = append(out, strconv.FormatFloat(math.Float64frombits(binary.LittleEndian.Uint64(b[pos:])), 'g', -1, 64))
			pos += 8
		case 6:
			if pos+4 > len(b) {
				return nil, 0, errors.New("BYTE_ARRAY length past the page")
			}
			l := int(binary.LittleEndian.Uint32(b[pos:]))
			pos += 4
			if l < 0 || pos+l > len(b) {
				return nil, 0, errors.New("BYTE_ARRAY past the page")
			}
			out = append(out, string(b[pos:pos+l]))
			pos += l
		default:
			return nil, 0, fmt.Errorf("physical type %d", physical)
		}
	}
	return out, pos, nil
}

// decodeHybrid decodes n values of the RLE/bit-packed hybrid encoding at width bits,
// returning them with the bytes consumed.
func decodeHybrid(b []byte, width, n int) ([]int32, int, error) {
	if width < 0 || width > 32 {
		return nil, 0, fmt.Errorf("bit width %d", width)
	}
	out := make([]int32, 0, n)
	pos, byteWidth := 0, (width+7)/8
	for len(out) < n {
		h, k := binary.Uvarint(b[pos:])
		if k <= 0 {
			return nil, 0, errors.New("run header past the data")
		}
		pos += k
		if h&1 == 0 {
			if pos+byteWidth > len(b) {
				return nil, 0, errors.New("run value past the data")
			}
			var v uint32
			for i := range byteWidth {
				v |= uint32(b[pos+i]) << (8 * i)
			}
			pos += byteWidth
			for range min(int(h>>1), n-len(out)) {
				out = append(out, int32(v))
			}
			continue
		}
		groups := int(h >> 1)
		if pos+groups*width > len(b) {
			return nil, 0, errors.New("bit-packed run past the data")
		}
		bits := b[pos : pos+groups*width]
		pos += groups * width
		for i := 0; i < groups*8 && len(out) < n; i++ {
			var v uint32
			for j := range width {
				bit := i*width + j
				v |= uint32(bits[bit/8]>>(bit%8)&1) << j
			}
			out = append(out, int32(v))
		}
	}
	return out, pos, nil
}

func bitWidth(v int) int {
	w := 0
	for ; v > 0; v >>= 1 {
		w++
	}
	return w
}

const (
	thriftTrue  = 1
	thriftFalse = 2
)

// thriftReader decodes the Thrift compact protocol parquet's metadata is written in.
// The first error sticks and every later read returns zero.
type thriftReader struct {
	b   []byte
	pos int
	err error
}

func (r *thriftReader) fail(format string, args ...any) {
	if r.err == nil {
		r.err = fmt.Errorf(format, args...)
	}
}

func (r *thriftReader) byte() byte {
	if r.err != nil || r.pos >= len(r.b) {
		r.fail("thrift data ends at %d", r.pos)
		return 0
	}
	r.pos++
	return r.b[r.pos-1]
}

func (r *thriftReader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.b[r.pos:])
	if n <= 0 {
		r.fail("bad varint at %d", r.pos)
		return 0
	}
	r.pos += n
	return v
}

// integer reads an i16, i32 or i64: a zigzag varint.
func (r *thriftReader) integer() int64 {
	u := r.uvarint()
	return int64(u>>1) ^ -int64(u&1)
}

func (r *thriftReader) binary() []byte {
	n := r.uvarint()
	if r.err != nil || n > uint64(len(r.b)-r.pos) {
		r.fail("binary of %d bytes past the data at %d", n, r.pos)
		return nil
	}
	r.pos += int(n)
	return r.b[r.pos-int(n) : r.pos]
}

// structure calls field for each field of a struct; field reads the value or skips it.
func (r *thriftReader) structure(field func(id int16, typ byte)) {
	var last int16
	for r.err == nil {
		h := r.byte()
		if h == 0 {
			return
		}
		id := last + int16(h>>4)
		if h>>4 == 0 {
			id = int16(r.integer())
		}
		last = id
		field(id, h&0x0f)
	}
}

// list calls elem once per element of a list or set.
func (r *thriftReader) list(elem func(typ byte)) {
	h := r.byte()
	n := int(h >> 4)
	if n == 15 {
		n = int(r.uvarint())
	}
	for i := 0; i < n && r.err == nil; i++ {
		elem(h & 0x0f)
	}
}

func (r *thriftReader) skip(typ byte) {
	switch typ {
	case thriftTrue, thriftFalse:
	case 3:
		r.byte()
	case 4, 5, 6:
		r.uvarint()
	case 7:
		if r.pos+8 > len(r.b) {
			r.fail("double past the data at %d", r.pos)
			return
		}
		r.pos += 8
	case 8:
		r.binary()
	case 9, 10:
		r.list(r.skipElement)
	case 11:
		n := r.uvarint()
		if n == 0 {
			return
		}
		kv := r.byte()
		for i := uint64(0); i < n && r.err == nil; i++ {
			r.skipElement(kv >> 4)
			r.skipElement(kv & 0x0f)
		}
	case 12:
		r.structure(func(_ int16, t byte) { r.skip(t) })
	default:
		r.fail("thrift type %d at %d", typ, r.pos)
	}
}

// skipElement skips a list, set or map element, where a bool takes a byte.
func (r *thriftReader) skipElement(typ byte) {
	if typ == thriftTrue || typ == thriftFalse {
		r.byte()
		return
	}
	r.skip(typ)
}

func TestExternalHarnessHybridDecoding(t *testing.T) {
	// An RLE run of five 3s, then one bit-packed group of eight 3-bit values 0..7.
	b := []byte{5 << 1, 3, 1<<1 | 1, 0b10001000, 0b11000110, 0b11111010}
	got, n, err := decodeHybrid(b, 3, 13)
	require.NoError(t, err)
	assert.Equal(t, []int32{3, 3, 3, 3, 3, 0, 1, 2, 3, 4, 5, 6, 7}, got)
	assert.Equal(t, len(b), n)
	_, _, err = decodeHybrid(b[:4], 3, 13)
	assert.Error(t, err, "a truncated group")
	got, _, err = decodeHybrid([]byte{4 << 1}, 0, 4)
	require.NoError(t, err)
	assert.Equal(t, []int32{0, 0, 0, 0}, got, "width 0 is a one-entry dictionary")
}

func TestExternalHarnessPlainDecoding(t *testing.T) {
	b := []byte{2, 0, 0, 0, 'h', 'i', 0, 0, 0, 0}
	got, n, err := decodePlain(b, 6, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"hi", ""}, got)
	assert.Equal(t, len(b), n)
	got, _, err = decodePlain([]byte{0xfe, 0xff, 0xff, 0xff}, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, []string{"-2"}, got)
	_, _, err = decodePlain([]byte{9, 0, 0, 0, 'x'}, 6, 1)
	assert.Error(t, err)
}

func TestExternalHarnessThriftSkipsUnknownFields(t *testing.T) {
	// {1: i32 -3, 2: list<binary> ["ab"], 4: struct {1: true}, 20: i64 7}, then STOP.
	b := []byte{0x15, 0x05, 0x19, 0x18, 0x02, 'a', 'b', 0x2c, 0x11, 0x00, 0x06, 0x28, 0x0e, 0x00}
	r := &thriftReader{b: b}
	got := map[int16]int64{}
	r.structure(func(id int16, typ byte) {
		if typ == 5 || typ == 6 {
			got[id] = r.integer()
			return
		}
		r.skip(typ)
	})
	require.NoError(t, r.err)
	assert.Equal(t, map[int16]int64{1: -3, 20: 7}, got)
	assert.Equal(t, len(b), r.pos)
	r = &thriftReader{b: b[:5]}
	r.structure(func(_ int16, typ byte) { r.skip(typ) })
	assert.Error(t, r.err)
}

func TestExternalHarnessFirstParagraph(t *testing.T) {
	assert.Equal(t, "Open opens the named file for reading.",
		firstParagraph("// Open opens the named\n// file for reading.\n//\n// More detail.", "go"))
	assert.Equal(t, "Return the sum.", firstParagraph("  Return the\n   sum.\n\n  Args: x", "python"))
}

func TestExternalHarnessStripPythonDocstring(t *testing.T) {
	assert.Equal(t, "def f(x):\n    return x\n", stripPythonDocstring("def f(x):\n    \"\"\"Doc.\"\"\"\n    return x\n"))
	assert.Equal(t, "def f():\npass", stripPythonDocstring("def f():\n    r'''Doc.'''\npass"))
	assert.Equal(t, "x = \"\"\"s\"\"\"", stripPythonDocstring("x = \"\"\"s\"\"\""), "not a def's docstring")
}

func TestExternalHarnessStripGoName(t *testing.T) {
	for _, tc := range []struct{ query, code, want string }{
		{"Open opens the named file.", "func Open(name string) (*File, error) {", "opens the named file."},
		{"NewClient returns a client.", "func (f *Factory) NewClient() *Client {", "returns a client."},
		{"New client returns a client.", "func NewClient() *Client {", "returns a client."},
		{"`Close` releases it.", "func (c *Conn) Close() error {", "releases it."},
		{"Returns the size.", "func Size() int {", "Returns the size."},
	} {
		got, _, _ := stripGoName(tc.query, tc.code)
		assert.Equal(t, tc.want, got, tc.query)
	}
	q, cut, name := stripGoName("Size returns the size.", "func Size() int {")
	assert.True(t, cut)
	assert.True(t, holdsNameToken(q, name), "the gold name survives in the rest of the query")
}

func TestExternalHarnessDedupeCorpus(t *testing.T) {
	s := externalSubset{
		name:    "toy",
		queries: []externalText{{"q1", "a"}, {"q2", "b"}},
		docs:    []externalText{{"d1", "same"}, {"d2", "other"}, {"d3", "same"}},
		qrels:   map[string]map[string]int{"q1": {"d3": 1}, "q2": {"d2": 1, "d1": 2}},
	}
	got := dedupeExternalCorpus(s)
	assert.Equal(t, "toy-dedup", got.name)
	assert.Equal(t, []externalText{{"d1", "same"}, {"d2", "other"}}, got.docs)
	assert.Equal(t, map[string]map[string]int{"q1": {"d1": 1}, "q2": {"d2": 1, "d1": 2}}, got.qrels)
}

func TestExternalHarnessLuceneTokens(t *testing.T) {
	assert.Equal(t, []string{"open", "file_name", "42", "times", "ünïcode"}, luceneTokens("Open the file_name, 42 times? ÜNÏCODE"))
}

func TestExternalHarnessBM25AndScoring(t *testing.T) {
	s := externalSubset{
		name:    "toy",
		queries: []externalText{{"q1", "parse json"}, {"q2", "open file"}},
		docs:    []externalText{{"d1", "parse the json body"}, {"d2", "open a file"}, {"d3", "json json json"}, {"d4", "nothing"}},
		qrels:   map[string]map[string]int{"q1": {"d1": 1}, "q2": {"d2": 1}},
	}
	run := runBM25("bm25", s, luceneTokens)
	require.Len(t, run.Queries["q1"], 2)
	assert.Equal(t, "d1", run.Queries["q1"][0].Doc, "both terms beat one term repeated")
	assert.Equal(t, []rankeval.Hit{{Doc: "d2", Score: run.Queries["q2"][0].Score}}, run.Queries["q2"])
	sc, err := scoreExternalRun(s, run)
	require.NoError(t, err)
	assert.True(t, sc.success)
	assert.InDelta(t, 1, sc.ndcg, 1e-12)
	assert.InDelta(t, 1, sc.recall, 1e-12)

	stray := rankeval.TRECRun{Tag: "x", Queries: map[string][]rankeval.Hit{"q1": {{Doc: "d9", Score: 1}}}}
	_, err = scoreExternalRun(s, stray)
	assert.ErrorContains(t, err, "doc d9")
}

func TestExternalHarnessFuseReciprocalRank(t *testing.T) {
	a := rankeval.TRECRun{Tag: "a", Queries: map[string][]rankeval.Hit{"q": {{Doc: "x", Score: 9}, {Doc: "y", Score: 8}}}}
	b := rankeval.TRECRun{Tag: "b", Queries: map[string][]rankeval.Hit{"q": {{Doc: "y", Score: 0.9}, {Doc: "z", Score: 0.1}}}}
	got := fuseReciprocalRank("rrf", a, b)
	// Variables, so the sum rounds in float64 as the fusion's does, not exactly as a
	// constant expression would.
	first, second := 1/float64(61), 1/float64(62)
	assert.Equal(t, []rankeval.Hit{
		{Doc: "y", Score: second + first},
		{Doc: "x", Score: first},
		{Doc: "z", Score: second},
	}, got.Queries["q"])
}
