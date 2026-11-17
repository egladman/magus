package budget

// USDPerMTok is a token price in US dollars per million tokens.
type USDPerMTok float64

// PriceStep is a long-context surcharge: every rate is multiplied by Multiplier once a
// call's prompt passes Above tokens, cache reads and writes counting toward it.
type PriceStep struct {
	Above      int64
	Multiplier float64
}

// Price is one model's token rates. The module ships no rates: a [Pricer] the caller
// implements supplies every one. A rate left 0 is unknown, never free, so a caller
// that reads 0 has no price to charge rather than a free call.
type Price struct {
	Input        USDPerMTok
	CacheWrite5m USDPerMTok
	CacheWrite1h USDPerMTok
	CacheRead    USDPerMTok
	Output       USDPerMTok
	// LongContext is the model's long-context surcharge, nil when it has none.
	LongContext *PriceStep
}

// Multiplier is what a call sending promptTokens pays on every rate:
// LongContext.Multiplier above LongContext.Above, else 1. A step missing either half
// surcharges nothing.
func (p Price) Multiplier(promptTokens int64) float64 {
	if s := p.LongContext; s != nil && s.Above > 0 && promptTokens > s.Above && s.Multiplier > 0 {
		return s.Multiplier
	}
	return 1
}

// Pricer answers a model's rates. False means the model is unknown, which a caller
// reads as no price, never as a free model.
type Pricer interface {
	Price(model string) (Price, bool)
}
