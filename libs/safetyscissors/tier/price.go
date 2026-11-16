package tier

import (
	"slices"
	"strings"
)

// USDPerMTok is a token price in US dollars per million tokens.
type USDPerMTok float64

// PriceStep is a long-context surcharge: every price is multiplied by Multiplier once a
// call's prompt passes Above tokens, cache reads and writes counting toward it.
type PriceStep struct {
	Above      int64
	Multiplier float64
}

// Pricing is one model's row in a [Table]. The module ships no rows: the caller supplies
// every one.
type Pricing struct {
	// ID is the model id the entry matches exactly. A provider prefix, a date
	// or a context suffix makes another id, with an entry of its own.
	ID string
	// Tier is the model-tier case this exact id runs, "" for none.
	Tier string
	// Alias is the short name the host takes for this model, matched exactly like ID and
	// never as a prefix, "" for none.
	Alias string
	// Window is the context window in tokens.
	Window int64
	// Input, CacheWrite5m, CacheWrite1h, CacheRead and Output are the standard prices.
	// A field left 0 is unknown, never free.
	Input        USDPerMTok
	CacheWrite5m USDPerMTok
	CacheWrite1h USDPerMTok
	CacheRead    USDPerMTok
	Output       USDPerMTok
	// LongContext is the model's long-context surcharge, nil when it has none.
	LongContext *PriceStep
}

// PriceMultiplier is the multiplier a call sending promptTokens pays on every price:
// LongContext.Multiplier above LongContext.Above, else 1.
func (p Pricing) PriceMultiplier(promptTokens int64) float64 {
	if s := p.LongContext; s != nil && s.Above > 0 && promptTokens > s.Above && s.Multiplier > 0 {
		return s.Multiplier
	}
	return 1
}

// Table is the model rows agent context is measured against. The zero Table has no rows;
// InEffect builds one from rows the caller supplies.
type Table struct {
	rows []Pricing
}

// InEffect layers overrides over base: a row whose ID an earlier row has sets that row's
// non-zero fields and keeps the rest, a non-nil LongContext replacing the whole step; any
// other ID adds a row. IDs match case-insensitively. Neither argument is changed.
func InEffect(base, overrides []Pricing) Table {
	var rows []Pricing
	for _, layer := range [][]Pricing{base, overrides} {
		for _, o := range layer {
			o.ID = strings.ToLower(o.ID)
			i := slices.IndexFunc(rows, func(r Pricing) bool { return r.ID == o.ID })
			if i < 0 {
				rows = append(rows, o)
				continue
			}
			rows[i] = overlay(rows[i], o)
		}
	}
	return Table{rows: rows}
}

func overlay(base, o Pricing) Pricing {
	set := func(dst *USDPerMTok, v USDPerMTok) {
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
func (t Table) Lookup(id string) (Pricing, bool) {
	id = strings.ToLower(id)
	i := slices.IndexFunc(t.rows, func(r Pricing) bool { return r.ID == id })
	if i < 0 {
		return Pricing{}, false
	}
	return t.rows[i], true
}

// Rows returns the table's rows in declaration order. The slice is the caller's.
func (t Table) Rows() []Pricing {
	return slices.Clone(t.rows)
}
