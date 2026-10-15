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

// Lookup returns the row whose ID is model id exactly, ignoring case. A provider prefix
// (us.anthropic.), a date (-20260401) or a context suffix ([1m]) makes another id, which
// matches no row until one names it: a model is never inferred from a name. False when no
// row matches, which a caller reads as "no facts", never as a zero-sized or free model.
func (t Table) Lookup(id string) (types.ModelPricing, bool) {
	id = strings.ToLower(id)
	i := slices.IndexFunc(t.rows, func(r types.ModelPricing) bool { return r.ID == id })
	if i < 0 {
		return types.ModelPricing{}, false
	}
	return t.rows[i], true
}

// Rows returns the table's rows in declaration order. The slice is the caller's.
func (t Table) Rows() []types.ModelPricing {
	return slices.Clone(t.rows)
}
