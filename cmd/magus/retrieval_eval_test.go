//go:build retrievaleval

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rankeval"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)

func TestRetrievalEval(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	root, err := workspace.FindRoot("")
	require.NoError(t, err)
	judgments := readRetrievalJudgments(t, filepath.Join(root, "testdata", "retrieval", "judgments.jsonl"))

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
	for _, n := range r.domain.Nodes() {
		r.known[n.ID] = n.Source
	}

	workers := runtime.GOMAXPROCS(0)
	start := time.Now()
	runs, err := rankeval.Replay(ctx, judgments, workers, r.replay)
	require.NoError(t, err)
	elapsed := time.Since(start)
	dropped := 0
	for _, run := range runs {
		dropped += run.Dropped
	}

	report := rankeval.Summarize(runs)
	fmt.Print(report.Text())
	fmt.Printf("dropped gold: %d\n", dropped)
	fmt.Printf("replayed %d judgments in %s on %d workers\n", len(runs), elapsed.Round(time.Millisecond), workers)
	printBaselineDeltas(t, filepath.Join(root, "testdata", "retrieval", "baseline.json"), report)
	if dest := os.Getenv("MAGUS_RETRIEVAL_EVAL_BASELINE"); dest != "" {
		writeRetrievalBaseline(t, root, dest, report)
	}
}

func readRetrievalJudgments(t *testing.T, file string) []rankeval.Judgment {
	t.Helper()
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s does not exist; harvest it with ./magus buzz hack/retrieval/harvest.buzz <holdout-after date>", file)
	}
	require.NoError(t, err)
	defer f.Close()
	judgments, err := rankeval.ReadJudgments(f)
	require.NoError(t, err)
	return judgments
}

// replayer answers judgments from two graphs loaded once: domain, the default graph,
// and symbols, the default graph with every symbol shard merged, which is what
// searchGraph, explainNode and loadRefsGraph load per call for a read that seeds the
// symbol layer. coverage measures the symbol indexes once rather than per read, which
// walks the tree. known maps each default graph node id to its Source. Safe for
// concurrent use: a knowledge.Graph is safe for concurrent reads.
type replayer struct {
	root     string
	m        *magus.Magus
	cfg      config.Config
	next     nextGate
	coverage coverageFunc
	domain   *knowledge.Graph
	symbols  *knowledge.Graph
	known    map[string]string
}

// replay drops j's gold ids that r.known no longer holds, asks j's verb j's query
// through the helpers the CLI verb calls, and renders the answer as the verb prints it
// in text form to count its bytes. Latency covers the answer, not the render.
func (r replayer) replay(ctx context.Context, j rankeval.Judgment) (rankeval.Run, error) {
	j, dropped := j.Resolved(func(id string) bool {
		_, ok := r.known[id]
		return ok
	})
	run := rankeval.Run{Judgment: j, Dropped: dropped}

	read := graphRead{Input: j.Query}
	sources := map[string]string{}
	var hits []string
	var answer types.KnowledgeAnswer
	var printed bytes.Buffer
	var err error
	start := time.Now()
	switch j.Verb {
	case readQuery:
		read.Nearest = true
		res := searchLoaded(ctx, r.graphFor(read.Input), read, r.coverage)
		run.Latency = time.Since(start)
		for _, n := range res.Out.Nodes {
			sources[n.ID] = n.Source
		}
		for _, match := range res.Out.Matches {
			hits = append(hits, match.ID)
		}
		answer = res.Out.Answer
		err = writeQueryText(ctx, &printed, r.next, r.next.served(hint.NextForQuery(res.Out)), res)
	case readExplain:
		res := explainLoaded(ctx, r.graphFor(read.Input), read, r.coverage)
		run.Latency = time.Since(start)
		answer = res.Answer
		// A miss prints to stderr only.
		if res.Found {
			sources[res.Out.Node.ID] = res.Out.Node.Source
			hits = append(hits, res.Out.Node.ID)
			for _, e := range slices.Concat(res.Out.Out, res.Out.In) {
				hits = append(hits, e.Other)
			}
			err = writeExplainText(&printed, r.root, j.Query, r.next, r.next.served(hint.NextForExplain(res.Out)), res)
		}
	case readRefs:
		res := lookupRefs(ctx, r.m, r.cfg, r.symbols, read, r.coverage)
		run.Latency = time.Since(start)
		answer = res.Answer
		// A miss or an ambiguous name prints to stderr only, so the text search refs
		// runs beside a miss adds nothing here.
		if res.Found {
			for _, site := range slices.Concat(res.Out.Defs, res.Out.Refs) {
				hits = append(hits, types.KindFile+":"+site.File)
			}
			answer = res.Out.Answer
			out := res.Out
			out.Answer = withIndexCause(ctx, r.root, out.Answer)
			err = writeRefsText(&printed, out)
		}
	default:
		return run, fmt.Errorf("no replay for verb %q", j.Verb)
	}
	// A miss, an unknown verdict or a stale index exits nonzero after printing its answer.
	if err != nil && !exit.AlreadyReported(err) {
		return run, fmt.Errorf("magus %s %q: %w", j.Verb, j.Query, err)
	}
	run.Unknown = answer.Verdict == types.VerdictUnknown
	run.Ranked = withDefiningFiles(hits, func(id string) string {
		if s, ok := sources[id]; ok {
			return s
		}
		return r.known[id]
	}, r.known)
	run.Bytes = printed.Len()
	return run, nil
}

// graphFor is the graph the CLI verb would load for input.
func (r replayer) graphFor(input string) *knowledge.Graph {
	if knowledge.SeedsLazyLayer(input) {
		return r.symbols
	}
	return r.domain
}

// withDefiningFiles follows each hit with the file:<path> its node's Source points into
// and that file's dir:<dir>, keeping each id's first position: gold ids name files and
// dirs, while a hit is often a symbol, target or doc section inside one.
func withDefiningFiles(hits []string, sourceOf func(string) string, known map[string]string) []string {
	seen := make(map[string]bool, 3*len(hits))
	out := make([]string, 0, 3*len(hits))
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range hits {
		add(id)
		file := sourceFile(sourceOf(id))
		fileID := types.KindFile + ":" + file
		if _, ok := known[fileID]; file == "" || !ok {
			continue
		}
		add(fileID)
		add(types.KindDir + ":" + path.Dir(file))
	}
	return out
}

// sourceFile strips a node Source's #anchor and :line suffixes, leaving its path.
func sourceFile(source string) string {
	source, _, _ = strings.Cut(source, "#")
	for {
		i := strings.LastIndexByte(source, ':')
		if i < 0 {
			return source
		}
		if _, err := strconv.Atoi(source[i+1:]); err != nil {
			return source
		}
		source = source[:i]
	}
}

func printBaselineDeltas(t *testing.T, file string, now rankeval.Report) {
	t.Helper()
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Printf("no baseline at %s; nothing to compare against\n", file)
		return
	}
	require.NoError(t, err)
	var base rankeval.Report
	require.NoError(t, json.Unmarshal(data, &base), "decode %s", file)
	deltas := rankeval.Compare(base, now)
	fmt.Printf("against %s: %d metric(s) moved\n", file, len(deltas))
	for _, d := range deltas {
		fmt.Printf("  %s %s: %.3f -> %.3f\n", d.Slice, d.Metric, d.Base, d.Now)
	}
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
