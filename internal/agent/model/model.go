package model

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// AsOf is when the shipped rows were read from Source. magus doctor reports the table
// once it is older than MaxAge, since a model released after it matches no row.
var AsOf = time.Date(2026, time.October, 13, 0, 0, 0, 0, time.UTC)

// MaxAge is how old the shipped table may be before magus doctor reports it.
const MaxAge = 120 * 24 * time.Hour

// Source is the page the shipped prices were read from.
const Source = "https://platform.claude.com/docs/en/about-claude/pricing"

// shipped is hand-written from Source, never fetched: the Models API needs an API key a
// subscription user does not have, and a hook has no time for a network call.
var shipped = []types.ModelFacts{
	{ID: "claude-fable-5-1", Window: 1_000_000, Input: 10, CacheWrite5m: 12.50, CacheWrite1h: 20, CacheRead: 0.25, Output: 50},
	{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, CacheWrite5m: 5, CacheWrite1h: 8, CacheRead: 0.20, Output: 20},
	{ID: "claude-sonnet-5-5", Window: 1_000_000, Input: 2, CacheWrite5m: 2.50, CacheWrite1h: 4, CacheRead: 0.10, Output: 10},
	// The one current model priced by prompt length: every category is 5x above 100,000
	// prompt tokens, cache reads and writes included.
	{ID: "claude-haiku-5-5", Window: 1_000_000, Input: 0.10, CacheWrite5m: 0.125, CacheWrite1h: 0.20, CacheRead: 0.01, Output: 0.50,
		PriceStep: 100_000, StepFactor: 5},
	{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.10, Output: 5},
}

// Table is the model rows agent context is measured against. The zero Table has no rows.
type Table struct {
	rows []types.ModelFacts
}

// Shipped returns the table built into this binary.
func Shipped() Table {
	return Table{rows: slices.Clone(shipped)}
}

// Lookup finds id's row in the shipped table; see Table.Lookup.
func Lookup(id string) (types.ModelFacts, bool) {
	return Shipped().Lookup(id)
}

// Merge returns t with overrides applied, as agents.models in magus.yaml declares them.
// An override whose ID is a row's ID sets that row's non-zero fields and keeps the rest;
// any other ID adds a row. t is not modified.
func (t Table) Merge(overrides []types.ModelFacts) Table {
	rows := slices.Clone(t.rows)
	for _, o := range overrides {
		o.ID = strings.ToLower(o.ID)
		i := slices.IndexFunc(rows, func(r types.ModelFacts) bool { return r.ID == o.ID })
		if i < 0 {
			rows = append(rows, o)
			continue
		}
		rows[i] = overlay(rows[i], o)
	}
	return Table{rows: rows}
}

func overlay(base, o types.ModelFacts) types.ModelFacts {
	set := func(dst *float64, v float64) {
		if v != 0 {
			*dst = v
		}
	}
	if o.Window != 0 {
		base.Window = o.Window
	}
	if o.PriceStep != 0 {
		base.PriceStep = o.PriceStep
	}
	set(&base.Input, o.Input)
	set(&base.CacheWrite5m, o.CacheWrite5m)
	set(&base.CacheWrite1h, o.CacheWrite1h)
	set(&base.CacheRead, o.CacheRead)
	set(&base.Output, o.Output)
	set(&base.StepFactor, o.StepFactor)
	return base
}

// Lookup returns the row for model id as a host reports it. A row matches an id that
// contains its ID as a whole version: a provider prefix (us.anthropic.), a date
// (-20260401), a context suffix ([1m]) or a revision (@20251101, -v1:0) around it still
// match, while claude-opus-5 does not match claude-opus-5-5. Of several matching rows the
// longest ID wins. False when no row matches, which a caller reads as "no facts", never
// as a zero-sized or free model.
func (t Table) Lookup(id string) (types.ModelFacts, bool) {
	id = strings.ToLower(id)
	var best types.ModelFacts
	found := false
	for _, r := range t.rows {
		if matches(id, r.ID) && (!found || len(r.ID) > len(best.ID)) {
			best, found = r, true
		}
	}
	return best, found
}

func matches(id, row string) bool {
	if row == "" {
		return false
	}
	for start := 0; ; {
		i := strings.Index(id[start:], row)
		if i < 0 {
			return false
		}
		i += start
		if (i == 0 || !alnum(id[i-1])) && versionEnds(id[i+len(row):]) {
			return true
		}
		start = i + 1
	}
}

// versionEnds reports whether rest, the text after a matched row ID, ends the version
// rather than continuing it: -5 or .5 or 5 would make it another model, an eight-digit
// date would not.
func versionEnds(rest string) bool {
	if rest == "" {
		return true
	}
	if digit(rest[0]) || rest[0] == '.' {
		return false
	}
	if rest[0] != '-' {
		return true
	}
	n := 0
	for n < len(rest)-1 && digit(rest[1+n]) {
		n++
	}
	return n == 0 || n >= 8
}

func digit(c byte) bool { return c >= '0' && c <= '9' }

func alnum(c byte) bool { return digit(c) || (c >= 'a' && c <= 'z') }

// LargestWindow is the largest context window of any row, 0 for an empty table.
func (t Table) LargestWindow() int64 {
	if len(t.rows) == 0 {
		return 0
	}
	return slices.MaxFunc(t.rows, func(a, b types.ModelFacts) int { return cmp.Compare(a.Window, b.Window) }).Window
}

// Rows returns the table's rows in declaration order, shipped rows first. The slice is
// the caller's.
func (t Table) Rows() []types.ModelFacts {
	return slices.Clone(t.rows)
}

// Stale reports whether the shipped table is older than MaxAge at now.
func Stale(now time.Time) bool {
	return now.Sub(AsOf) > MaxAge
}
