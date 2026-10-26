package names

import "io"

func graphOf() {} // want `graphOf ends in the word "Of"`

func ServableTo() {} // want `ServableTo ends in the word "To"`

func value_for() {} // want `value_for ends in the word "for"`

func URLFor() {} // want `URLFor ends in the word "For"`

func parseSrc() {} // want `parseSrc uses the short form "Src"`

func loadCtx() {} // want `loadCtx uses the short form "Ctx"`

func readReq() {} // want `readReq uses the short form "Req"`

func writeResp() {} // want `writeResp uses the short form "Resp"`

func writeRsp() {} // want `writeRsp uses the short form "Rsp"`

func fillBuf() {} // want `fillBuf uses the short form "Buf"`

func buildFn() {} // want `buildFn uses the short form "Fn"`

func parseOpts() {} // want `parseOpts uses the short form "Opts"`

func decodeStr() {} // want `decodeStr uses the short form "Str"`

func decodeStrs() {} // want `decodeStrs uses the short form "Strs"`

func checkErr() {} // want `checkErr uses the short form "Err"`

func runCmd() {} // want `runCmd uses the short form "Cmd"`

func withCwd() {} // want `withCwd uses the short form "Cwd"`

func histPrev() {} // want `histPrev uses the short form "Prev"`

func lookupImpl() {} // want `lookupImpl uses the short form "Impl"`

func findRepo() {} // want `findRepo uses the short form "Repo"`

func Ctx() {} // want `Ctx uses the short form "Ctx"`

func srcAndOffset() {} // want `srcAndOffset uses the short form "src"`

func ensureDstSafe() {} // want `ensureDstSafe uses the short form "Dst"`

func loadCfgOf() {} // want `loadCfgOf ends in the word "Of"` `loadCfgOf uses the short form "Cfg"`

type Ring struct{}

func (Ring) moveTo() {} // want `moveTo ends in the word "To"`

func (Ring) bindCtx() {} // want `bindCtx uses the short form "Ctx"`

// WriteTo satisfies io.WriterTo, which chose the name.
func (*Ring) WriteTo(w io.Writer) (int64, error) { return 0, nil }

// ReadFrom satisfies io.ReaderFrom, which chose the name.
func (*Ring) ReadFrom(r io.Reader) (int64, error) { return 0, nil }

type Sink struct{}

// WriteTo here does not match io.WriterTo's signature, so the name is its own.
func (Sink) WriteTo(path string) error { return nil } // want `WriteTo ends in the word "To"`

// Suffixes skip a one-word name. A one-word short form is still judged, and
// what is not a function or method passes.
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
