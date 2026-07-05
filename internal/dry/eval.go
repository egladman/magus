package dry

import (
	"bytes"
	"context"
	"regexp"
	"strconv"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
	vm "github.com/egladman/gopherbuzz/vm"

	hostgen "github.com/egladman/magus/host/gen"
)

// WASMCompatibleMagusModules is the allowlist of magus modules the browser
// playground registers: the WASMCompatible entries of the one host-module registry
// (hostgen.Modules), each pure computation with no filesystem, network, process,
// or randomness access under WASM. IO modules (fs / os / http / vcs / archive) and
// uuid stay out; their examples render as reference-only code blocks in the docs.
//
// Derived from the registry rather than hand-listed, so a new pure module is
// covered automatically. The docs generator (cmd/magus-docs) reads this map so the
// runnable-marker and actually-runs-in-browser decisions use the same source.
var WASMCompatibleMagusModules = wasmCompatibleMagusModules()

func wasmCompatibleMagusModules() map[string]func(context.Context, *buzz.Session) vm.Value {
	out := make(map[string]func(context.Context, *buzz.Session) vm.Value)
	for name, reg := range hostgen.Modules {
		if reg.WASMCompatible {
			out[name] = reg.Register
		}
	}
	return out
}

// registerWASMCompatibleMagusModules installs every module in WASMCompatibleMagusModules
// on sess, so `import "strings"; strings.camelCase("hi")` etc. run in-browser.
func registerWASMCompatibleMagusModules(ctx context.Context, sess *buzz.Session) {
	for name, register := range WASMCompatibleMagusModules {
		sess.SetSyntheticModule(name, register(ctx, sess))
	}
}

// PlaygroundHostModules names every magus host module the browser playground makes
// available: the WASM-compatible bare imports (registered above) plus "magus", which
// installHost wires as a global (sess.SetGlobal("magus", ...)) rather than a registry
// module. It is the single truth for what runs in the playground - kept next to the
// wiring so the two can't drift - and the langservice manifest diffs against it to
// decide which modules are reference-only there. Because magus is listed here (it is
// genuinely wired), it is never reported as excluded: no special-casing downstream.
func PlaygroundHostModules() []string {
	out := make([]string, 0, len(WASMCompatibleMagusModules)+1)
	for name := range WASMCompatibleMagusModules {
		out = append(out, name)
	}
	out = append(out, "magus")
	return out
}

// Diag is a structured evaluation error: the message plus a 1-based source
// position when one could be recovered from it (0 when not).
type Diag struct {
	Msg  string `json:"msg"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
}

// EvalResult is the outcome of an evaluation: the value of a trailing `return`
// (Result), anything the program printed (Output), and a Diag on failure. Trace
// is populated only in tracer mode (Eval with WithTracer): the ordered host-op
// trace a spell example's targets would perform (empty otherwise).
type EvalResult struct {
	OK     bool   `json:"ok"`
	Result string `json:"result"`
	Output string `json:"output"`
	Diag   *Diag  `json:"diag"`
	Trace  []Op   `json:"trace,omitempty"`
}

// evalConfig is the resolved configuration for an Eval call. The zero value is
// the plain language playground: Buzz stdlib plus the WASM-compatible host modules,
// evaluated once for its Result. Options layer on the tracing magusfile host.
type evalConfig struct {
	// tracer switches from the eval-once path (return a Result) to the dry-run
	// path: install the tracing magus/spell host, probe every target, and return
	// the host-op Trace instead.
	tracer bool
	// spells names extra spells (import name -> op names) to register beyond the
	// built-ins, so a workspace or third-party spell's example traces too. Non-nil
	// implies tracer mode.
	spells map[string][]string
}

// EvalOption configures Eval. Options are additive: each turns on a capability
// over the plain-language base, so a caller opts into exactly the host surface its
// snippet needs.
type EvalOption func(*evalConfig)

// WithTracer runs src as a magusfile dry run rather than a plain expression: it
// installs the tracing magus/spell host, probes every target body once, and
// returns the ordered host-op Trace those bodies would perform (nothing is
// forked). This is the runnable path for the spell docs.
func WithTracer() EvalOption {
	return func(c *evalConfig) { c.tracer = true }
}

// WithSpells registers additional spells (import name -> op names) as tracing
// `magus/spell/<name>` modules on top of the built-ins, so an example binding a
// workspace or third-party spell traces its ops like a built-in's. Implies
// WithTracer. This is the first-class hook for documenting non-built-in spells.
func WithSpells(spells map[string][]string) EvalOption {
	return func(c *evalConfig) {
		c.tracer = true
		if c.spells == nil {
			c.spells = map[string][]string{}
		}
		for name, ops := range spells {
			c.spells[name] = ops
		}
	}
}

// Eval evaluates Buzz source in a fresh sandboxed session. With no options it
// is the language playground: Buzz stdlib plus the WASM-compatible host modules
// (strings / json / crypto / ...), evaluated once, returning the trailing value's
// Result and any print Output. This runs the stdlib-module doc examples.
//
// With WithTracer (or WithSpells), it instead runs src as a magusfile dry run:
// the tracing magus/spell host is layered on, every target is probed once, and
// the ordered host-op Trace those targets would perform is returned - so a spell
// example like `import "magus/spell/go"; go["go-build"]()` reports a `go build` op
// instead of forking anything. A parse/compile failure returns a Diag; a target
// that panics mid-probe still yields the ops traced up to the panic.
func Eval(ctx context.Context, src string, opts ...EvalOption) EvalResult {
	var cfg evalConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// Tracer mode (WithTracer/WithSpells): probe every target under the tracing host
	// and flatten their ops into one Trace, in discovery (sorted-key) order so a
	// multi-target example reads top to bottom. Unlike Run it does not walk a single
	// target's dependency closure: an example is self-contained, so every op it wires
	// is worth showing.
	if cfg.tracer {
		tr, targets, ops, isSpellBuf, diag := evalAndProbe(ctx, src, nil, mergeSpells(cfg.spells))
		if diag != nil {
			return EvalResult{Output: tr.out.String(), Diag: diag}
		}
		var trace []Op
		if isSpellBuf {
			// A SPELL buffer traces one op per discovered op (with any ward), rather
			// than per-target host ops, so its example reads top to bottom like a
			// magusfile's.
			for _, o := range ops {
				// Charms are off in the docs tracer, so renderCommand cannot fail on a
				// patch; a decode error would mean a malformed docs spell (constructor-built
				// patches never are), rendering an empty command - fine for the preview.
				detail, _ := o.renderCommand(nil)
				trace = append(trace, Op{Target: o.name, Kind: o.kind, Name: o.name, Detail: detail})
				for _, w := range o.wards {
					trace = append(trace, Op{Target: o.name, Kind: "ward", Name: string(w.Code), Detail: wardDetail(w)})
				}
			}
		} else {
			for _, t := range targets {
				trace = append(trace, tr.opsByTarget[t.key]...)
			}
		}
		return EvalResult{OK: true, Output: tr.out.String(), Trace: trace}
	}

	// Plain mode: evaluate the Buzz snippet once and return its trailing value + output.
	var out bytes.Buffer
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	buzzstd.RegisterWithOutput(sess, &out)
	registerWASMCompatibleMagusModules(ctx, sess)

	v, err := sess.Eval(ctx, src)
	if err != nil {
		return EvalResult{Output: out.String(), Diag: toDiag(err)}
	}
	return EvalResult{OK: true, Result: v.String(), Output: out.String()}
}

// mergeSpells returns the built-in spell registry with extra merged over it (extra
// wins on a name clash), so a caller's WithSpells adds to rather than replaces the
// built-ins. A nil extra returns the built-ins unchanged.
func mergeSpells(extra map[string][]string) map[string][]string {
	if len(extra) == 0 {
		return builtinSpellOps
	}
	merged := make(map[string][]string, len(builtinSpellOps)+len(extra))
	for name, ops := range builtinSpellOps {
		merged[name] = ops
	}
	for name, ops := range extra {
		merged[name] = ops
	}
	return merged
}

// EvalInContext evaluates expr in a session that has first executed magusfileSrc,
// so the file's top-level functions, objects, enums, and consts are in scope,
// like a REPL with the magusfile autoloaded. It uses the same tracing host as
// magusfile mode, so magus/spell imports resolve. Only top-level runs (targets
// are defined, not invoked). A magusfile that fails to compile binds nothing, so
// a self-contained expr still evaluates; one referencing the file's defs reports
// the undefined name.
func EvalInContext(ctx context.Context, magusfileSrc, expr string) EvalResult {
	tr := newTracer()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	installHost(ctx, sess, tr, builtinSpellOps)

	_ = sess.Exec(ctx, magusfileSrc) // best effort: bind whatever compiles

	// Compile the expression form first so a bare expression prints its value
	// (ldflags(VERSION) -> a string), falling back to the statement form
	// (var x = 1;). Mirrors the real Buzz REPL driver and avoids running side
	// effects twice.
	chunk, err := sess.Compile("return " + expr)
	if err != nil {
		chunk, err = sess.Compile(expr)
		if err != nil {
			return EvalResult{Output: tr.out.String(), Diag: toDiag(err)}
		}
	}
	v, err := sess.EvalChunk(ctx, chunk)
	if err != nil {
		return EvalResult{Output: tr.out.String(), Diag: toDiag(err)}
	}
	return EvalResult{OK: true, Result: v.String(), Output: tr.out.String()}
}

// posRe matches the "buzz: line L:C:" prefix the parser and checker emit, so the
// editor can mark the offending line.
var posRe = regexp.MustCompile(`line (\d+):(\d+)`)

func toDiag(err error) *Diag {
	d := &Diag{Msg: err.Error()}
	if m := posRe.FindStringSubmatch(d.Msg); m != nil {
		d.Line, _ = strconv.Atoi(m[1])
		d.Col, _ = strconv.Atoi(m[2])
	}
	return d
}
