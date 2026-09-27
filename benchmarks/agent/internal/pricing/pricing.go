// Package pricing is the checked-in model list-price table and the loader that
// reads it. rates.json records which vendor's published page the numbers came
// from and the date they were read; the hostagnostic linter is why the
// name lives in the data rather than in this prose.
//
// The table is the cost basis on purpose. A host's self-reported dollar figure
// is a client-side estimate computed from whatever price table that binary
// carries, and such a table typically falls back to a default model's rates for
// an id it has never heard of, which prices a run at a constant multiple of the
// truth with nothing in the output saying so. This loader errors on a model it
// does not know instead, so an unpriced run stops rather than being quietly
// mispriced.
//
// There is no first-party JSON pricing API. The published machine-readable
// source is the docs Markdown named in rates.json's _source; GET /v1/models
// returns ids, context windows and capabilities but no prices. The only
// programmatic source of ACTUAL billed USD is the Admin API cost report
// (GET /v1/organizations/cost_report), which needs an Admin API key, is daily
// granularity only, groups by workspace or description, and returns decimal
// strings in cents. It cannot attribute spend to a single benchmark run unless
// that run gets a workspace of its own.
//
// Refreshing the table is an explicit edit or an explicitly invoked target that
// rewrites rates.json. Nothing here reaches the network: a published number
// must never move because a fetch succeeded or failed at report time.
//
// The table carries no batch, fast-mode or inference-geo pricing. Those are
// billing modes the published page also lists, but benchmarks/agent's runner
// always issues an ordinary interactive request (runner/agent.sh execs `claude
// -p` with no batch, fast-mode or inference_geo lever), so no run this package
// ever prices could have used one. Options and Effective existed here with no
// caller but their own tests; wire them back in, with the rates restored from
// the vendor's page, if the runner ever gains a way to select a mode.
package pricing

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"

	_ "embed"
)

// tokensPerPriceUnit is the denominator every rate is quoted against.
const tokensPerPriceUnit = 1_000_000.0

//go:embed rates.json
var embedded []byte

// The API serves a dated model id (the alias plus a -YYYYMMDD suffix) while the
// table is keyed by alias, so the date is stripped before a second lookup.
var datedModelSuffix = regexp.MustCompile(`-\d{8}$`)

// Rates are one model's list prices in USD per million tokens.
type Rates struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
}

// Cost is what the given token counts total at r's rates, in USD.
func (r Rates) Cost(input, output, cacheRead, cacheWrite5m, cacheWrite1h int64) float64 {
	total := 0.0
	total += float64(input) * r.Input / tokensPerPriceUnit
	total += float64(output) * r.Output / tokensPerPriceUnit
	total += float64(cacheRead) * r.CacheRead / tokensPerPriceUnit
	total += float64(cacheWrite5m) * r.CacheWrite5m / tokensPerPriceUnit
	total += float64(cacheWrite1h) * r.CacheWrite1h / tokensPerPriceUnit
	return total
}

// Table is a price table keyed by model alias, plus the surcharges that are not
// token rates.
type Table struct {
	models               map[string]Rates
	webSearchPerThousand float64
	source               string
	fetchedOn            string
}

// Default is the table compiled into this binary. It fails only if the embedded
// JSON is malformed, which this package's tests rule out.
func Default() (Table, error) {
	return parse(embedded, "benchmarks/agent/internal/pricing/rates.json")
}

// Load reads a price table from a file, for pricing a run against a table other
// than the checked-in one.
func Load(file string) (Table, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Table{}, fmt.Errorf("pricing: %w", err)
	}
	return parse(raw, file)
}

// Source is the URL the table was transcribed from and the date it was read.
func (t Table) Source() (url, fetchedOn string) { return t.source, t.fetchedOn }

// Models lists every priced alias, sorted.
func (t Table) Models() []string { return slices.Sorted(maps.Keys(t.models)) }

// WebSearchUSD is what n server-side web searches cost. Web search is billed on
// top of tokens, so a run that used it is priced above its token total.
func (t Table) WebSearchUSD(searches int64) float64 {
	return float64(searches) * t.webSearchPerThousand / 1000.0
}

// Lookup finds a model's list rates by its id, then by the id with its date
// suffix stripped. An id absent under both names is an error rather than a
// guess: guessing is what produces a bill-shaped number nobody can audit.
func (t Table) Lookup(model string) (Rates, error) {
	if r, ok := t.models[model]; ok {
		return r, nil
	}
	if r, ok := t.models[datedModelSuffix.ReplaceAllString(model, "")]; ok {
		return r, nil
	}
	return Rates{}, fmt.Errorf("pricing: model %q is absent from the pricing table; add its published prices", model)
}

// parse reads rates.json. The underscore-prefixed members of that file are
// prose for whoever opens it and are deliberately not loaded, apart from the
// provenance pair every published figure has to be traceable to.
func parse(raw []byte, file string) (Table, error) {
	var doc struct {
		Source               string                `json:"_source"`
		FetchedOn            string                `json:"_fetched_on"`
		WebSearchPerThousand *float64              `json:"web_search_usd_per_thousand"`
		Models               map[string]modelEntry `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Table{}, fmt.Errorf("pricing: table %s: %w", file, err)
	}
	if len(doc.Models) == 0 {
		return Table{}, fmt.Errorf("pricing: table %s has no models", file)
	}
	if doc.WebSearchPerThousand == nil {
		return Table{}, fmt.Errorf("pricing: table %s lacks web_search_usd_per_thousand", file)
	}
	t := Table{
		models:               make(map[string]Rates, len(doc.Models)),
		webSearchPerThousand: *doc.WebSearchPerThousand,
		source:               doc.Source,
		fetchedOn:            doc.FetchedOn,
	}
	for model, entry := range doc.Models {
		rates, err := entry.rates()
		if err != nil {
			return Table{}, fmt.Errorf("pricing: table %s: model %s: %w", file, model, err)
		}
		t.models[model] = rates
	}
	return t, nil
}

// modelEntry is one models block. Every rate is a pointer so a missing key is
// distinguishable from a published zero.
type modelEntry struct {
	Input        *float64 `json:"input"`
	Output       *float64 `json:"output"`
	CacheRead    *float64 `json:"cache_read"`
	CacheWrite5m *float64 `json:"cache_write_5m"`
	CacheWrite1h *float64 `json:"cache_write_1h"`
}

func (e modelEntry) rates() (Rates, error) {
	var r Rates
	for _, rate := range []struct {
		name string
		src  *float64
		dst  *float64
	}{
		{"input", e.Input, &r.Input},
		{"output", e.Output, &r.Output},
		{"cache_read", e.CacheRead, &r.CacheRead},
		{"cache_write_5m", e.CacheWrite5m, &r.CacheWrite5m},
		{"cache_write_1h", e.CacheWrite1h, &r.CacheWrite1h},
	} {
		if rate.src == nil {
			return Rates{}, fmt.Errorf("pricing: lacks the five rates: no %s", rate.name)
		}
		if *rate.src <= 0 {
			return Rates{}, fmt.Errorf("pricing: %s is not a positive rate", rate.name)
		}
		*rate.dst = *rate.src
	}
	return r, nil
}
