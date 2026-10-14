package model

import (
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Table is the model rows agent context is measured against. The zero Table has no rows;
// InEffect builds one. No row is built into magus.
type Table struct {
	rows []types.ModelPricing
}

// InEffect is the table a workspace prices against: the rows its harnesses publish, then
// the person's agents.models over them. A row whose ID an earlier row has sets that row's
// non-zero fields and keeps the rest, a non-nil LongContext replacing the whole step; any
// other ID adds a row. IDs match case-insensitively.
func InEffect(harness, overrides []types.ModelPricing) Table {
	var rows []types.ModelPricing
	for _, layer := range [][]types.ModelPricing{harness, overrides} {
		for _, o := range layer {
			o.ID = strings.ToLower(o.ID)
			i := slices.IndexFunc(rows, func(r types.ModelPricing) bool { return r.ID == o.ID })
			if i < 0 {
				rows = append(rows, o)
				continue
			}
			rows[i] = overlay(rows[i], o)
		}
	}
	return Table{rows: rows}
}

func overlay(base, o types.ModelPricing) types.ModelPricing {
	set := func(dst *types.USDPerMTok, v types.USDPerMTok) {
		if v != 0 {
			*dst = v
		}
	}
	if o.Window != 0 {
		base.Window = o.Window
	}
	if o.LongContext != nil {
		base.LongContext = o.LongContext
	}
	set(&base.Input, o.Input)
	set(&base.CacheWrite5m, o.CacheWrite5m)
	set(&base.CacheWrite1h, o.CacheWrite1h)
	set(&base.CacheRead, o.CacheRead)
	set(&base.Output, o.Output)
	return base
}

// Lookup returns the row for model id as a host reports it. A row matches an id that
// contains its ID as a whole version: a provider prefix (us.anthropic.), a date
// (-20260401), a context suffix ([1m]) or a revision (@20251101, -v1:0) around it still
// match, while claude-opus-5 does not match claude-opus-5-5. Of several matching rows the
// longest ID wins. False when no row matches, which a caller reads as "no facts", never
// as a zero-sized or free model.
func (t Table) Lookup(id string) (types.ModelPricing, bool) {
	id = strings.ToLower(id)
	var best types.ModelPricing
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

// Rows returns the table's rows in declaration order. The slice is the caller's.
func (t Table) Rows() []types.ModelPricing {
	return slices.Clone(t.rows)
}
