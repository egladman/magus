// Package pricing is the checked-in model list-price table and the loader that
// reads it. pricing.json records which vendor's published page the numbers came
// from and the date they were read; TestNoHostSpecificBehaviorInCode is why the
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
// source is the docs Markdown named in pricing.json's _source; GET /v1/models
// returns ids, context windows and capabilities but no prices. The only
// programmatic source of ACTUAL billed USD is the Admin API cost report
// (GET /v1/organizations/cost_report), which needs an Admin API key, is daily
// granularity only, groups by workspace or description, and returns decimal
// strings in cents. It cannot attribute spend to a single benchmark run unless
// that run gets a workspace of its own.
//
// Refreshing the table is an explicit edit or an explicitly invoked target that
// rewrites pricing.json. Nothing here reaches the network: a published number
// must never move because a fetch succeeded or failed at report time.
package pricing

import (
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"

	_ "embed"

	"github.com/egladman/magus/internal/json"
)

// TokensPerPriceUnit is the denominator every rate is quoted against.
const TokensPerPriceUnit = 1_000_000.0

//go:embed pricing.json
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

	// FastInput and FastOutput replace Input and Output under fast mode. Both
	// are zero on a model that does not offer it.
	FastInput  float64 `json:"fast_input"`
	FastOutput float64 `json:"fast_output"`

	// GeoMultipliers scales every token category when a request pins
	// inference_geo. Published for 4.6-era models and later only, so it is nil
	// on everything older.
	GeoMultipliers map[string]float64 `json:"geo_multipliers"`
}

// Fast reports whether the model publishes a fast-mode price pair.
func (r Rates) Fast() bool { return r.FastInput > 0 && r.FastOutput > 0 }

// Options are the billing modes that multiply a model's list price. The zero
// value is the ordinary interactive request.
type Options struct {
	// Batch prices the request through the Batch API.
	Batch bool
	// Fast selects fast mode, which only some models offer.
	Fast bool
	// InferenceGeo pins the serving region; "" is the unmultiplied default.
	InferenceGeo string
}

// Table is a price table keyed by model alias, plus the surcharges that are not
// token rates.
type Table struct {
	models               map[string]Rates
	batchMultiplier      float64
	webSearchPerThousand float64
	source               string
	fetchedOn            string
}

// Default is the table compiled into this binary. It fails only if the embedded
// JSON is malformed, which this package's tests rule out.
func Default() (Table, error) { return parse(embedded, "libs/pricing/pricing.json") }

// Load reads a price table from a file, for pricing a run against a table other
// than the checked-in one.
func Load(file string) (Table, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return Table{}, err
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
	return Rates{}, fmt.Errorf("model %q is absent from the pricing table; add its published prices", model)
}

// Effective is a model's rates with o's billing modes applied.
//
// Fast mode keeps the model's own cache multipliers, so a fast cache read stays
// the same fraction of input it is at the base rate. Batch discounts input and
// output, the two categories the source quotes it for. A mode the model does
// not publish is an error, because pricing a mode that was never quoted is the
// same guess Lookup refuses.
//
// The result is a rate set, not a table entry, so the mode fields it was derived
// from are cleared: applying Effective to its own output would compound them.
func (t Table) Effective(model string, o Options) (Rates, error) {
	r, err := t.Lookup(model)
	if err != nil {
		return Rates{}, err
	}
	if o.Fast {
		if !r.Fast() {
			return Rates{}, fmt.Errorf("model %q publishes no fast-mode price", model)
		}
		// Scale by the ratio rather than re-listing four more numbers: the
		// cache multipliers are a property of the model, not of the mode.
		scale := r.FastInput / r.Input
		r.Input, r.Output = r.FastInput, r.FastOutput
		r.CacheRead *= scale
		r.CacheWrite5m *= scale
		r.CacheWrite1h *= scale
	}
	if o.Batch {
		r.Input *= t.batchMultiplier
		r.Output *= t.batchMultiplier
	}
	if o.InferenceGeo != "" {
		m, ok := r.GeoMultipliers[o.InferenceGeo]
		if !ok {
			return Rates{}, fmt.Errorf("model %q publishes no inference_geo multiplier for %q", model, o.InferenceGeo)
		}
		r.Input *= m
		r.Output *= m
		r.CacheRead *= m
		r.CacheWrite5m *= m
		r.CacheWrite1h *= m
	}
	r.FastInput, r.FastOutput, r.GeoMultipliers = 0, 0, nil
	return r, nil
}

// parse reads pricing.json. The underscore-prefixed members of that file are
// prose for whoever opens it and are deliberately not loaded, apart from the
// provenance pair every published figure has to be traceable to.
func parse(raw []byte, file string) (Table, error) {
	var doc struct {
		Source               string                `json:"_source"`
		FetchedOn            string                `json:"_fetched_on"`
		BatchMultiplier      *float64              `json:"batch_multiplier"`
		WebSearchPerThousand *float64              `json:"web_search_usd_per_thousand"`
		Models               map[string]modelEntry `json:"models"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Table{}, fmt.Errorf("pricing table %s: %w", file, err)
	}
	if len(doc.Models) == 0 {
		return Table{}, fmt.Errorf("pricing table %s has no models", file)
	}
	if doc.BatchMultiplier == nil || doc.WebSearchPerThousand == nil {
		return Table{}, fmt.Errorf("pricing table %s lacks batch_multiplier or web_search_usd_per_thousand", file)
	}
	t := Table{
		models:               make(map[string]Rates, len(doc.Models)),
		batchMultiplier:      *doc.BatchMultiplier,
		webSearchPerThousand: *doc.WebSearchPerThousand,
		source:               doc.Source,
		fetchedOn:            doc.FetchedOn,
	}
	for model, entry := range doc.Models {
		rates, err := entry.rates()
		if err != nil {
			return Table{}, fmt.Errorf("pricing table %s: model %s: %w", file, model, err)
		}
		t.models[model] = rates
	}
	return t, nil
}

// modelEntry is one models block. Every rate is a pointer so a missing key is
// distinguishable from a published zero.
type modelEntry struct {
	Input          *float64           `json:"input"`
	Output         *float64           `json:"output"`
	CacheRead      *float64           `json:"cache_read"`
	CacheWrite5m   *float64           `json:"cache_write_5m"`
	CacheWrite1h   *float64           `json:"cache_write_1h"`
	FastInput      *float64           `json:"fast_input"`
	FastOutput     *float64           `json:"fast_output"`
	GeoMultipliers map[string]float64 `json:"geo_multipliers"`
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
			return Rates{}, fmt.Errorf("lacks the five rates: no %s", rate.name)
		}
		if *rate.src <= 0 {
			return Rates{}, fmt.Errorf("%s is not a positive rate", rate.name)
		}
		*rate.dst = *rate.src
	}
	if (e.FastInput == nil) != (e.FastOutput == nil) {
		return Rates{}, fmt.Errorf("fast mode needs both fast_input and fast_output or neither")
	}
	if e.FastInput != nil {
		r.FastInput, r.FastOutput = *e.FastInput, *e.FastOutput
	}
	for geo, m := range e.GeoMultipliers {
		if m <= 0 {
			return Rates{}, fmt.Errorf("geo_multipliers %s is not a positive multiplier", geo)
		}
	}
	r.GeoMultipliers = e.GeoMultipliers
	return r, nil
}
