//go:build retrievaleval

package main

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/rankeval"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
)


// replayer answers judgments from two graphs loaded once: domain, the default graph,
// and symbols, the default graph with every symbol shard merged, which is what
// searchGraph, explainNode and loadRefsGraph load per call for a read that seeds the
// symbol layer. coverage measures the symbol indexes once rather than per read, which
// walks the tree. known maps each node id of symbols to its Source: a Go file's file:
// and dir: nodes, which most gold names, exist only in the symbol shards. Safe for
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
