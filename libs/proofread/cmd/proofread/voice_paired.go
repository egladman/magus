package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/egladman/magus/libs/proofread"
	"github.com/egladman/magus/libs/proofread/internal/calibrate"
)

// pairedEval is what `voice eval --paired` was asked for.
type pairedEval struct {
	owner, other, field, kindField, idField, format string
	kind                                            proofread.Kind
	pool                                            int
	seed                                            uint64
}

// textPair is one owner text and its rewrite.
type textPair struct{ owner, other string }

// pairedKind is one kind's pairs and what its two files held unpaired.
type pairedKind struct {
	pairs    []textPair
	unpaired int
}

// runPairedEval joins the owner's texts with their rewrites by id, per kind,
// and writes how far each feature moves across the pairs.
func runPairedEval(e pairedEval, stdout, stderr io.Writer) int {
	byKind, err := e.join()
	if err != nil {
		return failure(stderr, err)
	}

	kinds := slices.Sorted(maps.Keys(byKind))
	seps := make(map[proofread.Kind][]calibrate.PairedSeparation, len(kinds))
	units := make(map[proofread.Kind]int, len(kinds))

	for _, k := range kinds {
		owners, others := e.measure(k, byKind[k].pairs)
		seps[k], units[k] = calibrate.SeparatePairs(owners, others), len(owners)
	}

	if e.format == "json" {
		data, err := json.MarshalIndent(seps, "", "  ")
		if err != nil {
			return failure(stderr, fmt.Errorf("encode the separations: %w", err))
		}

		fmt.Fprintf(stdout, "%s\n", data)

		return exitOK
	}

	for i, k := range kinds {
		if i > 0 {
			fmt.Fprintln(stdout)
		}

		fmt.Fprint(stdout, pairedTable(k, units[k], byKind[k].unpaired, e.pool, seps[k]))
	}

	return exitOK
}

// join reads both files and pairs their texts by kind and id. An id on one
// side alone is counted as unpaired, and an id twice on one side is an error.
func (e pairedEval) join() (map[proofread.Kind]*pairedKind, error) {
	owners, err := e.read(e.owner)
	if err != nil {
		return nil, err
	}

	others, err := e.read(e.other)
	if err != nil {
		return nil, err
	}

	out := map[proofread.Kind]*pairedKind{}
	at := func(k proofread.Kind) *pairedKind {
		if out[k] == nil {
			out[k] = &pairedKind{}
		}

		return out[k]
	}

	for k, texts := range owners {
		for _, id := range slices.Sorted(maps.Keys(texts)) {
			if rewrite, ok := others[k][id]; ok {
				at(k).pairs = append(at(k).pairs, textPair{owner: texts[id], other: rewrite})
			} else {
				at(k).unpaired++
			}
		}
	}

	for k, texts := range others {
		for id := range texts {
			if _, ok := owners[k][id]; !ok {
				at(k).unpaired++
			}
		}
	}

	return out, nil
}

// read returns path's texts by kind and id.
func (e pairedEval) read(path string) (map[proofread.Kind]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	records, err := jsonLineRecords(path, f, e.kind, e.field, e.kindField, e.idField)
	if err != nil {
		return nil, err
	}

	out := map[proofread.Kind]map[string]string{}

	for _, rec := range records {
		if !slices.ContainsFunc(subcommands, func(s subcommand) bool { return s.kind == rec.kind }) {
			return nil, fmt.Errorf("%s: unknown kind %q", path, rec.kind)
		}

		if out[rec.kind] == nil {
			out[rec.kind] = map[string]string{}
		}

		if _, dup := out[rec.kind][rec.id]; dup {
			return nil, fmt.Errorf("%s: %s %q appears twice", path, rec.kind, rec.id)
		}

		out[rec.kind][rec.id] = rec.text
	}

	return out, nil
}

// measure returns each pair's two shapes. A pool groups shuffled pairs until
// the owner side holds pool words. Each chunk and its rewrite then cover the
// same texts, which keeps the topic fixed.
func (e pairedEval) measure(kind proofread.Kind, pairs []textPair) (owners, others []proofread.Shape) {
	if e.pool == 0 {
		for _, p := range pairs {
			owners = append(owners, proofread.MeasureShape(p.owner, kind))
			others = append(others, proofread.MeasureShape(p.other, kind))
		}

		return owners, others
	}

	shuffled := slices.Clone(pairs)
	rand.New(rand.NewPCG(e.seed, e.seed)).Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	var (
		ownerChunk, otherChunk []string
		words                  int
	)

	for _, p := range shuffled {
		ownerChunk, otherChunk = append(ownerChunk, p.owner), append(otherChunk, p.other)
		words += proofread.Measure(p.owner, kind).Words

		if words >= e.pool {
			owners = append(owners, proofread.MeasureShape(strings.Join(ownerChunk, "\n\n"), kind))
			others = append(others, proofread.MeasureShape(strings.Join(otherChunk, "\n\n"), kind))
			ownerChunk, otherChunk, words = nil, nil, 0
		}
	}

	return owners, others
}

// pairedTable is a header naming kind and its counts, then one aligned line
// per feature.
func pairedTable(kind proofread.Kind, units, unpaired, pool int, seps []calibrate.PairedSeparation) string {
	var b strings.Builder

	unit := "pairs"
	if pool > 0 {
		unit = fmt.Sprintf("chunk pairs of %d+ owner words", pool)
	}

	fmt.Fprintf(&b, "%s: %d %s, %d texts unpaired; cut at %d%% owner false alarms\n",
		kind, units, unit, unpaired, calibrate.PairedFalseAlarmPercent)

	if len(seps) == 0 {
		fmt.Fprintf(&b, "  no feature measured on %d pairs\n", calibrate.MinSeparationTexts)

		return b.String()
	}

	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  feature\tpairs\trewrite higher\tcut\tfalse alarms\tdetected\tseparates")

	for _, s := range seps {
		verdict := "no"
		if s.Separates {
			verdict = "yes"
		}

		fmt.Fprintf(tw, "  %s\t%d\t%.3f\t%g\t%.3f\t%.3f\t%s\n",
			s.Feature, s.Pairs, s.Higher, s.Cut, s.FalseAlarms, s.Detected, verdict)
	}

	_ = tw.Flush()

	return b.String()
}
