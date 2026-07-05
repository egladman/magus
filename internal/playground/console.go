// Package playground is the browser front end for the Buzz playground: the terminal
// Console (command dispatch, completion, history, rendering to HTML rows), editor
// syntax Highlight, and the Share deep-link codec. It is the presentation layer over
// internal/dry, which does the actual non-executing evaluation: every console
// command dispatches into dry (LoadMagusfile / Run / EvalInContext) and this package
// only formats the result. cmd/buzz-playground wires it to the DOM.
package playground

import (
	"context"
	"html"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/dry"
)

// buzzLangVersion is the Buzz language version gopherbuzz implements
// (https://buzz-lang.dev/0.5.0).
const buzzLangVersion = "0.5.0"

// BuildInfo describes the toolchain that produced this binary, for the status
// line and `status` command: the in-browser analog of a REPL's build banner.
// The wasm entry point fills it from runtime plus a couple of build-pinned
// values runtime does not expose.
type BuildInfo struct {
	Compiler  string // e.g. "tinygo 0.40.0" or "gc go1.25.0"
	Target    string // GOOS/GOARCH, e.g. "js/wasm"
	Scheduler string // e.g. "asyncify"
	GoVersion string // the Go release the compiler is based on, e.g. "go1.24.7"
}

// Console is the playground terminal's brain: it holds the build info, the current
// magusfile source, its last parse, and command history, and turns a typed line
// into rendered output. Output is returned as []Line (pre-escaped HTML plus a CSS
// class), so the Go side owns formatting and the glue only appends nodes. Every
// command dispatches into the dry evaluator (LoadMagusfile / Run / EvalInContext)
// and renders the result.
type Console struct {
	info   BuildInfo
	src    string
	parsed dry.Graph
	hist   []string
	hpos   int
}

// Line is one rendered terminal row: trusted HTML (built here with escaping) and
// an optional CSS class (cmd/err/ok/muted/res).
type Line struct {
	HTML  string
	Class string
}

// ExecResult is the outcome of running a line: rows to append, or a request to
// clear the scrollback.
type ExecResult struct {
	Lines []Line
	Clear bool
}

// commands is the completable command set (also the `help` ordering source).
var commands = []string{"help", "ls", "targets", "graph", "run", "eval", "version", "clear", "about"}

func NewConsole(info BuildInfo) *Console { return &Console{info: info} }

// SetSource updates the editor content, re-parses it, and returns the editor
// header badge: ok plus a short status string.
func (s *Console) SetSource(ctx context.Context, src string) (ok bool, status string) {
	s.src = src
	s.parsed = dry.LoadMagusfile(ctx, src)
	if s.parsed.OK {
		n := len(s.parsed.Targets)
		unit := "target"
		if s.parsed.Spell {
			unit = "op" // a spell buffer's runnable units are its ops, not targets
		}
		return true, "[pass] " + strconv.Itoa(n) + " " + unit + plural(n)
	}
	if d := s.parsed.Diag; d != nil && d.Line > 0 {
		return false, "[fail] line " + strconv.Itoa(d.Line) + ":" + strconv.Itoa(d.Col)
	}
	return false, "[fail] parse error"
}

// Exec runs one input line and returns the rows to render (including the echoed
// prompt). A blank line echoes only; `clear` requests a wipe.
func (s *Console) Exec(ctx context.Context, line string) ExecResult {
	trimmed := strings.TrimSpace(line)
	if trimmed != "" {
		s.hist = append(s.hist, line)
	}
	s.hpos = len(s.hist)

	cmd, rest := splitFirst(trimmed)
	if cmd == "clear" {
		return ExecResult{Clear: true}
	}

	// Echo as a magus invocation (a dim "magus" prefix with the subcommand
	// highlighted) to signal that these are magus CLI subcommands.
	out := []Line{{HTML: `<span class="muted">magus</span> <span class="cmd">` + esc(line) + `</span>`}}
	if trimmed == "" {
		return ExecResult{Lines: out}
	}
	switch cmd {
	case "help":
		out = append(out, s.help()...)
	case "about":
		out = append(out, s.about()...)
	case "ls", "targets":
		out = append(out, s.ls()...)
	case "graph":
		out = append(out, s.graph()...)
	case "run":
		out = append(out, s.run(ctx, rest)...)
	case "eval":
		out = append(out, s.eval(ctx, rest)...)
	case "version":
		out = append(out, s.version()...)
	default:
		out = append(out, s.eval(ctx, trimmed)...) // a bare line is a Buzz expression
	}
	return ExecResult{Lines: out}
}

// Complete returns the input line after tab-completion plus any listing rows to
// print (when the completion is ambiguous).
func (s *Console) Complete(line string) (replacement string, listing []Line) {
	line = strings.TrimLeft(line, " ") // tolerate leading/extra spaces
	sp := strings.IndexByte(line, ' ')
	var prefix, base string
	var candidates []string

	if sp == -1 {
		prefix, base = "", line
		candidates = filterPrefix(commands, base)
	} else {
		cmd := line[:sp]
		base = strings.TrimLeft(line[sp+1:], " ")
		prefix = cmd + " "
		if cmd != "run" {
			return line, nil
		}
		for _, t := range s.parsed.Targets {
			if strings.HasPrefix(t.Key, base) {
				candidates = append(candidates, t.Key)
			}
		}
	}

	if len(candidates) == 0 {
		return line, nil
	}
	if len(candidates) == 1 {
		return prefix + candidates[0] + " ", nil
	}
	cp := commonPrefix(candidates)
	listing = []Line{{HTML: `<span class="muted">` + esc(strings.Join(candidates, "   ")) + `</span>`}}
	if len(cp) > len(base) {
		return prefix + cp, listing
	}
	return line, listing
}

// HistPrev and HistNext walk command history for the up/down keys. Both share one
// contract: the bool reports whether the input should be replaced with the returned
// line. At either end of the history they return ("", false), so pressing down at
// the newest entry leaves the in-progress line untouched rather than clearing it.
func (s *Console) HistPrev() (string, bool) {
	if s.hpos > 0 {
		s.hpos--
		return s.hist[s.hpos], true
	}
	return "", false
}

func (s *Console) HistNext() (string, bool) {
	if s.hpos < len(s.hist)-1 {
		s.hpos++
		return s.hist[s.hpos], true
	}
	s.hpos = len(s.hist)
	return "", false
}

func (s *Console) help() []Line {
	return []Line{{HTML: strings.Join([]string{
		`<span class="muted">commands</span>`,
		`  <b>ls</b>            list targets in the magusfile`,
		`  <b>graph</b>         show projects, targets and depends_on edges`,
		`  <b>run</b> &lt;target&gt;  dry-run a target (deps first, then its op trace)`,
		`  <b>eval</b> &lt;expr&gt;   evaluate a Buzz expression`,
		`  <b>version</b>       build &amp; runtime info (compiler, target, scheduler)`,
		`  <b>clear</b>         clear the terminal`,
		`  <b>about</b>         what this is`,
		``,
		`<span class="muted">a bare line is evaluated as Buzz. tab completes; ↑↓ recall history.</span>`,
	}, "\n")}}
}

// Banner is the terminal's opening message: a one-line build/runtime header (like a
// REPL's startup banner) followed by an unmissable note that the playground is a
// sandbox where nothing executes. The `about` command reprints it; `status` prints
// the full build detail.
func (s *Console) Banner() []Line {
	dot := `<span class="muted"> · </span>`
	head := `<span class="ok">●</span> <b>gopherbuzz</b>` + dot + `Buzz ` + buzzLangVersion
	if s.info.Compiler != "" {
		head += dot + esc(s.info.Compiler)
	}
	if s.info.Target != "" {
		head += dot + esc(s.info.Target)
	}
	head += dot + `<b>sandbox</b>`

	return []Line{
		{HTML: head},
		{HTML: ``},
		{HTML: `<span class="muted">  The interpreter is compiled to <b>WebAssembly</b> and runs in this browser tab:</span>`},
		{HTML: `<span class="muted">  no server, no shell, no filesystem. A magusfile or spell is </span><b>planned, not run</b><span class="muted">:</span>`},
		{HTML: `<span class="muted">  build steps are recorded so you can read the plan, but </span><b>no command is executed.</b>`},
		{HTML: ``},
		{HTML: `<span class="muted">  New here? Type </span><b>help</b><span class="muted"> to see the commands, or </span><b>ls</b><span class="muted"> to list this file's targets.</span>`},
	}
}

func (s *Console) about() []Line { return s.Banner() }

// version prints the build/runtime detail, the in-browser analog of the upstream
// Buzz REPL's startup banner.
func (s *Console) version() []Line {
	row := func(key, val string) Line {
		if val == "" {
			val = "n/a"
		}
		return Line{HTML: `  <span class="muted">` + key + `</span>  ` + esc(val)}
	}
	return []Line{
		{HTML: `<b>gopherbuzz</b>: a Buzz ` + buzzLangVersion + ` interpreter, written in Go`},
		row("compiler ", s.info.Compiler),
		row("target   ", s.info.Target),
		row("scheduler", s.info.Scheduler),
		row("go       ", s.info.GoVersion),
		row("mode     ", "sandbox · build steps recorded, nothing executed"),
	}
}

func (s *Console) ls() []Line {
	if !s.parsed.OK {
		return []Line{{HTML: "magusfile does not parse; fix it in the editor.", Class: "err"}}
	}
	if len(s.parsed.Targets) == 0 {
		return []Line{{HTML: "no targets defined.", Class: "muted"}}
	}
	var out []Line
	for _, t := range s.parsed.Targets {
		tail := ""
		if deps := s.depsOf(t.Key); len(deps) > 0 {
			tail = `  <span class="muted">→ ` + esc(strings.Join(deps, ", ")) + `</span>`
		}
		out = append(out, Line{HTML: "  <b>" + esc(t.Key) + "</b>" + tail})
	}
	return out
}

func (s *Console) graph() []Line {
	if !s.parsed.OK {
		return []Line{{HTML: "magusfile does not parse; fix it in the editor.", Class: "err"}}
	}
	var out []Line
	for _, p := range s.parsed.Projects {
		line := "project <b>" + esc(orDot(p.Path)) + "</b>"
		line += muted("spells", p.Spells)
		line += muted("depends_on", p.DependsOn)
		line += muted("outputs", p.Outputs)
		line += muted("no-cache", p.NoCache)
		line += muted("exclusive", p.ExclusiveTargets)
		line += muted("slots", p.Slots)
		if p.Exclusive {
			line += ` <span class="muted">exclusive</span>`
		}
		out = append(out, Line{HTML: line})
	}
	out = append(out, Line{HTML: "targets:", Class: "muted"})
	for _, t := range s.parsed.Targets {
		// Show the source name when it differs from the canonical key
		// (e.g. regen_pgo becomes regen-pgo) so the mapping is visible.
		name := ""
		if t.Name != t.Key {
			name = ` <span class="muted">(` + esc(t.Name) + `)</span>`
		}
		tail := ""
		if deps := s.depsOf(t.Key); len(deps) > 0 {
			tail = `  <span class="muted">→ ` + esc(strings.Join(deps, ", ")) + `</span>`
		}
		out = append(out, Line{HTML: "  " + esc(t.Key) + name + tail})
	}
	return out
}

func (s *Console) run(ctx context.Context, target string) []Line {
	if target == "" {
		return append([]Line{{HTML: `usage: <b>run &lt;target&gt;</b>`, Class: "muted"}}, s.ls()...)
	}
	key, charms := splitCharms(target)
	r := dry.Run(ctx, s.src, key, charms)
	if !r.OK {
		return []Line{{HTML: esc(diagMsg(r.Diag, "dry-run failed")), Class: "err"}}
	}
	var out []Line
	if r.Output != "" {
		out = append(out, Line{HTML: esc(r.Output)})
	}
	out = append(out, Line{HTML: `order: <span class="muted">` + esc(strings.Join(r.Order, "  →  ")) + `</span>`})
	if len(r.Trace) == 0 {
		out = append(out, Line{HTML: "  (no host operations)", Class: "muted"})
	} else {
		for _, op := range r.Trace {
			tag := `<span class="muted">[` + esc(op.Target) + `]</span> `
			if op.Kind == "log" {
				// A magus.info/warn/error line, not a tool call.
				cls := "muted"
				if op.Name == "warn" || op.Name == "error" {
					cls = "err"
				}
				out = append(out, Line{HTML: "  " + tag + `<span class="` + cls + `">` + esc(op.Detail) + `</span>`})
				continue
			}
			if op.Kind == "run" {
				// A magus.run(...) recursive target invocation, not a tool call.
				out = append(out, Line{HTML: "  " + tag + `<span class="muted">magus run</span> <b>` +
					esc(op.Name) + `</b>  <span class="muted">recursive invocation · recorded</span>`})
				continue
			}
			if op.Kind == "ward" {
				// A kind-coherence diagnostic (MGSxxxx) raised for a resolved spell op:
				// an error line showing its code and message.
				out = append(out, Line{HTML: "  " + tag + esc(op.Detail), Class: "err"})
				continue
			}
			detail := ""
			if op.Detail != "" {
				detail = " " + esc(op.Detail)
			}
			hint := op.Kind + " · recorded"
			if op.Kind == "service" {
				// A long-running, magus-supervised op shared across dependents by
				// config fingerprint, distinct from a run-to-completion command.
				hint = "service · supervised, shared"
			}
			out = append(out, Line{HTML: "  " + tag + `<b>` + esc(op.Name) + `</b>` + detail +
				`  <span class="muted">` + esc(hint) + `</span>`})
		}
	}
	wards := 0
	for _, op := range r.Trace {
		if op.Kind == "ward" {
			wards++
		}
	}
	if wards > 0 {
		// A ward is a rejection at resolution, not a passing plan: magus refuses the
		// op before running anything. Say so rather than a green pass, so the wards
		// lesson reads correctly.
		out = append(out, Line{HTML: "[fail] " + esc(target) + " rejected at resolution: " +
			strconv.Itoa(wards) + " ward" + plural(wards) + " raised, <b>nothing would run</b>", Class: "err"})
		return out
	}
	n := len(r.Trace)
	out = append(out, Line{HTML: "[pass] dry-run of " + esc(target) + ": " + strconv.Itoa(n) +
		" step" + plural(n) + " recorded, <b>nothing executed</b>", Class: "ok"})
	return out
}

func (s *Console) eval(ctx context.Context, src string) []Line {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	// Evaluate against the editor's magusfile so an expression can call the
	// functions and name the types defined there (e.g. ldflags(VERSION)).
	r := dry.EvalInContext(ctx, s.src, src)
	var out []Line
	if r.Output != "" {
		out = append(out, Line{HTML: esc(r.Output)})
	}
	if r.OK {
		out = append(out, Line{HTML: "⇒ " + esc(r.Result), Class: "res"})
		return out
	}
	where := ""
	if d := r.Diag; d != nil && d.Line > 0 {
		where = ` <span class="muted">(line ` + strconv.Itoa(d.Line) + ":" + strconv.Itoa(d.Col) + `)</span>`
	}
	out = append(out, Line{HTML: esc(diagMsg(r.Diag, "error")) + where, Class: "err"})
	return out
}

func (s *Console) depsOf(key string) []string {
	var out []string
	for _, e := range s.parsed.Edges {
		if e.From == key {
			out = append(out, e.To)
		}
	}
	return out
}

func esc(s string) string { return html.EscapeString(s) }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func orDot(s string) string {
	if s == "" {
		return "."
	}
	return s
}

func diagMsg(d *dry.Diag, fallback string) string {
	if d != nil && d.Msg != "" {
		return d.Msg
	}
	return fallback
}

func muted(label string, vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return `  <span class="muted">` + label + ": " + esc(strings.Join(vals, ", ")) + `</span>`
}

// splitCharms splits a "target:charm,charm" run reference into the target key
// and its active charms (mirroring the CLI's `magus run target:charm` suffix).
// No colon means no charms.
func splitCharms(ref string) (target string, charms []string) {
	i := strings.IndexByte(ref, ':')
	if i < 0 {
		return ref, nil
	}
	target = ref[:i]
	for _, c := range strings.Split(ref[i+1:], ",") {
		if c = strings.TrimSpace(c); c != "" {
			charms = append(charms, c)
		}
	}
	return target, charms
}

func splitFirst(s string) (head, rest string) {
	if i := strings.Index(s, " "); i != -1 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func filterPrefix(items []string, prefix string) []string {
	var out []string
	for _, it := range items {
		if strings.HasPrefix(it, prefix) {
			out = append(out, it)
		}
	}
	return out
}

func commonPrefix(items []string) string {
	if len(items) == 0 {
		return ""
	}
	p := items[0]
	for _, s := range items {
		i := 0
		for i < len(p) && i < len(s) && p[i] == s[i] {
			i++
		}
		p = p[:i]
	}
	return p
}
