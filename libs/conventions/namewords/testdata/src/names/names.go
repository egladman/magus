package names

import "io"

func graphOf() {} // want `graphOf ends in the word "Of"`

func ServableTo() {} // want `ServableTo ends in the word "To"`

func value_for() {} // want `value_for ends in the word "for"`

func URLFor() {} // want `URLFor ends in the word "For"`

func parseSrc() {} // want `parseSrc uses the short form "Src"`

func srcAndOffset() {} // want `srcAndOffset uses the short form "src"`

func ensureDstSafe() {} // want `ensureDstSafe uses the short form "Dst"`

func loadCfgOf() {} // want `loadCfgOf ends in the word "Of"` `loadCfgOf uses the short form "Cfg"`

type Ring struct{}

func (Ring) moveTo() {} // want `moveTo ends in the word "To"`

// WriteTo satisfies io.WriterTo, which chose the name.
func (*Ring) WriteTo(w io.Writer) (int64, error) { return 0, nil }

// ReadFrom satisfies io.ReaderFrom, which chose the name.
func (*Ring) ReadFrom(r io.Reader) (int64, error) { return 0, nil }

type Sink struct{}

// WriteTo here does not match io.WriterTo's signature, so the name is its own.
func (Sink) WriteTo(path string) error { return nil } // want `WriteTo ends in the word "To"`

// One-word names, words that only contain a listed one, and what is not a
// function or method all pass. ReadFrom is two words, so the suffix applies.
func Of() {}

func offset() {}

func Format() {}

func ReadFrom() {} // want `ReadFrom ends in the word "From"`

// A digit run is a word of its own.
func sourceAndDst2() {} // want `sourceAndDst2 uses the short form "Dst"`

var srcOf = 1

type DstFor struct{ cfgTo int }

func keep(src string, cfg int) (dst string) { return src }

var _ = srcOf
