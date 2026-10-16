//go:build retrievaleval

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
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

	g, err := loadWorkspaceGraph(ctx, m, cfg, false, false, false)
	require.NoError(t, err)
	known := make(map[string]string)
	for _, n := range g.Nodes() {
		known[n.ID] = n.Source
	}

	runs := make([]rankeval.Run, 0, len(judgments))
	dropped := 0
	for _, j := range judgments {
		run := replayJudgment(t, ctx, root, m, cfg, j, known)
		dropped += run.Dropped
		runs = append(runs, run)
	}

	report := rankeval.Summarize(runs)
	fmt.Print(report.Text())
	fmt.Printf("dropped gold: %d\n", dropped)
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

// replayJudgment drops j's gold ids that known (the default graph's node ids) no longer
// holds, then asks j's verb j's query through the helpers the CLI verb calls.
func replayJudgment(t *testing.T, ctx context.Context, root string, m *magus.Magus, cfg config.Config, j rankeval.Judgment, known map[string]string) rankeval.Run {
	t.Helper()
	var run rankeval.Run
	gold := make(map[string]int, len(j.Gold))
	for id, grade := range j.Gold {
		if _, ok := known[id]; ok {
			gold[id] = grade
		} else {
			run.Dropped++
		}
	}
	j.Gold = gold
	run.Judgment = j

	read := graphRead{Input: j.Query}
	sources := map[string]string{}
	var hits []string
	var answer types.KnowledgeAnswer
	start := time.Now()
	switch j.Verb {
	case readQuery:
		read.Nearest = true
		res, err := searchGraph(ctx, m, cfg, read, false, false)
		run.Latency = time.Since(start)
		require.NoError(t, err, "judgment %s", j.ID)
		for _, n := range res.Out.Nodes {
			sources[n.ID] = n.Source
		}
		for _, match := range res.Out.Matches {
			hits = append(hits, match.ID)
		}
		answer = res.Out.Answer
	case readExplain:
		res, err := explainNode(ctx, m, cfg, read, false, false)
		run.Latency = time.Since(start)
		require.NoError(t, err, "judgment %s", j.ID)
		if res.Found {
			sources[res.Out.Node.ID] = res.Out.Node.Source
			hits = append(hits, res.Out.Node.ID)
			for _, e := range slices.Concat(res.Out.Out, res.Out.In) {
				hits = append(hits, e.Other)
			}
		}
		answer = res.Answer
	case readRefs:
		g, err := loadRefsGraph(ctx, m, cfg, false, j.Query)
		require.NoError(t, err, "judgment %s", j.ID)
		res := lookupRefs(ctx, m, cfg, g, read)
		run.Latency = time.Since(start)
		answer = res.Answer
		if res.Found {
			for _, site := range slices.Concat(res.Out.Defs, res.Out.Refs) {
				hits = append(hits, types.KindFile+":"+site.File)
			}
			answer = res.Out.Answer
		}
	default:
		t.Fatalf("judgment %s: no replay for verb %q", j.ID, j.Verb)
	}
	run.Unknown = answer.Verdict == types.VerdictUnknown
	run.Ranked = withDefiningFiles(hits, func(id string) string {
		if s, ok := sources[id]; ok {
			return s
		}
		return known[id]
	}, known)
	run.Bytes = printedBytes(t, ctx, root, j)
	return run
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

// printedBytes runs j's CLI verb in text form and measures what it prints to stdout.
func printedBytes(t *testing.T, ctx context.Context, root string, j rankeval.Judgment) int {
	t.Helper()
	verb := map[string]func(context.Context, string, []string) error{
		readQuery:   queryCmd,
		readExplain: explainCmd,
		readRefs:    refsCmd,
	}[j.Verb]
	var err error
	printed := captureStdout(t, func() { err = verb(ctx, root, []string{j.Query}) })
	// A miss, an unknown verdict or a stale index exits nonzero after printing its answer.
	if err != nil && !exit.AlreadyReported(err) {
		t.Fatalf("judgment %s: magus %s %q: %v", j.ID, j.Verb, j.Query, err)
	}
	return len(printed)
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
