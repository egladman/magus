//go:build js && wasm

// Command buzz-playground is the browser entry point for the Buzz playground. It
// compiles to WebAssembly (TinyGo, or the standard js/wasm toolchain) and drives
// the whole page from Go: it owns the terminal — command dispatch, completion,
// history, rendering — and the editor's live parse, manipulating the DOM through
// syscall/js. The page's JavaScript is reduced to a ~10-line bootstrap that
// instantiates this module; all behavior lives here and in
// internal/playground.Console (which is pure Go and host-tested).
//
// The page provides the static structure (see magus/website/editor.html). This
// program grabs the elements by id, wires event handlers, and renders into them.
//
// All execution is in-memory: a browser cannot fork processes or touch a
// filesystem, so magusfile targets are evaluated to their graph and dry-run
// trace, never run.
//
// Build:
//
//	tinygo build -target=wasm -no-debug -o buzz.wasm ./cmd/buzz-playground
//	GOOS=js GOARCH=wasm go build -o buzz.wasm ./cmd/buzz-playground   # fallback
package main

import (
	"context"
	_ "embed"
	"html"
	"runtime"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/egladman/magus/internal/playground"
)

// runtime exposes the compiler, its version, and GOOS/GOARCH, but not TinyGo's
// scheduler or the Go release it wraps — neither is reachable at runtime and
// TinyGo ignores -ldflags -X. These track the build-playground TinyGo
// invocation (-scheduler=asyncify); bump them when you bump TinyGo, which is
// already a manual rebuild of the committed wasm.
const (
	builtScheduler = "asyncify"
	builtWithGo    = "go1.26.4"
)

// buildInfo reports the toolchain that produced this binary. The compiler,
// version, and target are read live, so a standard-toolchain build (the CI gate)
// describes itself accurately too.
func buildInfo() playground.BuildInfo {
	info := playground.BuildInfo{
		Compiler:  runtime.Compiler + " " + runtime.Version(),
		Target:    runtime.GOOS + "/" + runtime.GOARCH,
		Scheduler: builtScheduler,
		GoVersion: builtWithGo,
	}
	if runtime.Compiler != "tinygo" {
		// A standard build knows its own Go version; the TinyGo pins don't apply.
		info.GoVersion = runtime.Version()
		info.Scheduler = "go runtime"
	}
	return info
}

// The editor ships a few examples the picker switches between. Each is a real
// .buzz file so it reads (and highlights) the same in the repo as in the browser.
//
//go:embed showcase.buzz
var defaultSrc string // the flagship: a realistic magusfile with the full pipeline

//go:embed hello.buzz
var helloSrc string // the smallest useful magusfile

//go:embed fibonacci.buzz
var fibonacciSrc string // pure Buzz, to show the interpreter runs client-side

// example is one entry in the editor's example picker.
type example struct {
	id, label, file, src string
}

// examples are listed in picker order; the first is the one the editor opens with.
var examples = []example{
	{"advanced", "Advanced", "magusfile.buzz", defaultSrc},
	{"hello", "Hello world", "hello.buzz", helloSrc},
	{"fibonacci", "Fibonacci", "fibonacci.buzz", fibonacciSrc},
}

// ui bundles the DOM handles and the shell, captured by the event handlers.
type ui struct {
	doc    js.Value
	src    js.Value // editor textarea
	hl     js.Value // highlight overlay <code> (innerHTML + scroll transform)
	gutter js.Value // line-number gutter (textContent + scroll transform)
	out    js.Value // terminal scrollback
	in     js.Value // terminal input
	badge  js.Value // editor parse-status
	shell  *playground.Console
}

func main() {
	// Install globalThis.buzz FIRST so consumers that don't need the full UI
	// (WS N's Run buttons on docs pages; console scripting) always have the raw
	// evaluation API, regardless of whether the playground's editor markup
	// happens to be on this page. The UI setup below assumes the playground DOM
	// and would panic on a bare docs page - keeping the scoped nil channel below
	// alive on the goroutine keeps our exported callbacks reachable either way.
	exposeDataAPI()

	doc := js.Global().Get("document")
	// If the editor's root element is absent, this page isn't the playground:
	// leave main() to keep exposeDataAPI's callbacks alive without touching the
	// missing DOM. The Run-a-snippet path only needs window.buzz.evalBuzz.
	if !doc.Call("getElementById", "src").Truthy() {
		<-make(chan struct{}) // keep callbacks alive; nothing else to wire
		return
	}

	u := &ui{
		doc:    doc,
		src:    doc.Call("getElementById", "src"),
		hl:     doc.Call("getElementById", "highlight-code"),
		gutter: doc.Call("getElementById", "gutter"),
		out:    doc.Call("getElementById", "term-out"),
		in:     doc.Call("getElementById", "term-in"),
		badge:  doc.Call("getElementById", "parse-status"),
		shell:  playground.NewConsole(buildInfo()),
	}

	u.in.Call("addEventListener", "keydown", js.FuncOf(u.onTerminalKey))
	u.src.Call("addEventListener", "input", js.FuncOf(u.onEditorInput))
	u.src.Call("addEventListener", "keydown", js.FuncOf(u.onEditorKey))
	u.src.Call("addEventListener", "scroll", js.FuncOf(func(js.Value, []js.Value) any {
		u.syncScroll()
		return nil
	}))
	doc.Call("getElementById", "term").Call("addEventListener", "click",
		js.FuncOf(func(js.Value, []js.Value) any { u.in.Call("focus"); return nil }))

	// Populate the example picker and seed the editor with the first example
	// (parse + highlight happen in loadExample), then show the sandbox banner.
	u.setupExamplePicker()
	u.loadExample(examples[0].id)
	// If the URL carries a #source=<base64url> deep link, replace the editor
	// contents with the decoded source. The hash keeps source client-side (never
	// sent to the server / CDN / referrer / logs), which is why the plan pins it
	// to the hash instead of a ?code= query. Any load failure silently falls back
	// to the seeded example.
	u.applyHashSource()
	u.render(u.shell.Banner())

	if loading := doc.Call("getElementById", "loading"); loading.Truthy() {
		loading.Call("remove")
	}
	u.in.Set("disabled", false)
	u.in.Call("focus")
	showIntroOnce(doc)

	// (exposeDataAPI already ran at the top of main so window.buzz is available
	// on docs pages that don't carry the editor markup.)

	<-make(chan struct{}) // keep the exported callbacks alive
}

// setupExamplePicker fills the <select id="example-picker"> with the examples and
// loads the chosen one into the editor on change. Absent (older page), it no-ops.
func (u *ui) setupExamplePicker() {
	picker := u.doc.Call("getElementById", "example-picker")
	if !picker.Truthy() {
		return
	}
	for _, ex := range examples {
		opt := u.doc.Call("createElement", "option")
		opt.Set("value", ex.id)
		opt.Set("textContent", ex.label)
		picker.Call("appendChild", opt)
	}
	picker.Set("value", examples[0].id)
	picker.Call("addEventListener", "change", js.FuncOf(func(js.Value, []js.Value) any {
		u.loadExample(picker.Get("value").String())
		u.in.Call("focus")
		return nil
	}))
}

// loadExample swaps the editor to the named example: it replaces the source,
// updates the filename label, and re-parses/highlights. Unknown id is a no-op.
func (u *ui) loadExample(id string) {
	for _, ex := range examples {
		if ex.id != id {
			continue
		}
		u.src.Set("value", ex.src)
		u.src.Set("scrollTop", 0)
		if name := u.doc.Call("getElementById", "file-name"); name.Truthy() {
			name.Set("textContent", ex.file)
		}
		u.onSourceChanged()
		return
	}
}

// applyHashSource decodes a `#source=<base64url>` URL fragment and drops the
// decoded text into the editor, replacing whatever example was seeded. This is
// how "Open in Playground" deep-links from the docs (WS N) and the future Share
// button pass source into the page — through the URL hash, so the content stays
// client-side and never rides an HTTP request. Any malformed hash silently
// no-ops (the seeded example stays put).
func (u *ui) applyHashSource() {
	hash := js.Global().Get("location").Get("hash").String()
	if hash == "" {
		return
	}
	// Strip leading "#" and split into k=v pairs; consume only "source".
	q := strings.TrimPrefix(hash, "#")
	for _, part := range strings.Split(q, "&") {
		k, v, ok := strings.Cut(part, "=")
		if !ok || k != "source" {
			continue
		}
		// Base64URL-decode without depending on encoding/base64 (TinyGo builds
		// this file, and pulling in extra encoding packages balloons the wasm).
		// Instead, delegate to the browser: atob (standard base64) after
		// converting URL-safe -> standard alphabet and re-padding.
		s := strings.ReplaceAll(v, "-", "+")
		s = strings.ReplaceAll(s, "_", "/")
		switch len(s) % 4 {
		case 2:
			s += "=="
		case 3:
			s += "="
		}
		// atob returns a "binary string" (each JS char = one decoded byte); wrap
		// it in decodeURIComponent(escape(...)) to reinterpret as UTF-8. Any
		// invalid input throws; catch it to keep the boot path silent.
		defer func() { _ = recover() }()
		atob := js.Global().Get("atob")
		if !atob.Truthy() {
			return
		}
		decoded := atob.Invoke(s).String()
		// decoded is bytes-as-latin1; UTF-8-decode via decodeURIComponent(escape).
		esc := js.Global().Get("escape").Invoke(decoded).String()
		src := js.Global().Get("decodeURIComponent").Invoke(esc).String()
		u.src.Set("value", src)
		u.src.Set("scrollTop", 0)
		u.onSourceChanged()
		return
	}
}

// showIntroOnce reveals the first-visit callout unless the visitor has dismissed
// it before (a flag persisted in localStorage), and wires its dismiss button.
func showIntroOnce(doc js.Value) {
	intro := doc.Call("getElementById", "intro")
	if !intro.Truthy() {
		return
	}
	const key = "buzz.introDismissed"
	store := js.Global().Get("localStorage")
	if store.Truthy() && store.Call("getItem", key).Truthy() {
		return // already dismissed — leave it hidden
	}
	intro.Call("removeAttribute", "hidden")

	btn := doc.Call("getElementById", "intro-dismiss")
	if !btn.Truthy() {
		return
	}
	btn.Call("addEventListener", "click", js.FuncOf(func(js.Value, []js.Value) any {
		intro.Set("hidden", true)
		if store.Truthy() {
			store.Call("setItem", key, "1")
		}
		return nil
	}))
}

func (u *ui) onTerminalKey(_ js.Value, args []js.Value) any {
	e := args[0]
	switch e.Get("key").String() {
	case "Enter":
		line := u.in.Get("value").String()
		u.in.Set("value", "")
		res := u.shell.Exec(context.Background(), line)
		if res.Clear {
			u.out.Set("innerHTML", "")
		} else {
			u.render(res.Lines)
		}
	case "Tab":
		e.Call("preventDefault")
		repl, listing := u.shell.Complete(u.in.Get("value").String())
		u.in.Set("value", repl)
		if len(listing) > 0 {
			u.render(listing)
		}
	case "ArrowUp":
		e.Call("preventDefault")
		if v, ok := u.shell.HistPrev(); ok {
			u.in.Set("value", v)
		}
	case "ArrowDown":
		e.Call("preventDefault")
		if v, ok := u.shell.HistNext(); ok {
			u.in.Set("value", v)
		}
	}
	return nil
}

func (u *ui) onEditorInput(js.Value, []js.Value) any {
	u.onSourceChanged()
	return nil
}

func (u *ui) onEditorKey(_ js.Value, args []js.Value) any {
	e := args[0]
	if e.Get("key").String() != "Tab" {
		return nil
	}
	e.Call("preventDefault")
	start := u.src.Get("selectionStart").Int()
	end := u.src.Get("selectionEnd").Int()
	val := u.src.Get("value").String()
	if start < 0 || end > len(val) || start > end {
		return nil
	}
	u.src.Set("value", val[:start]+"    "+val[end:])
	u.src.Set("selectionStart", start+4)
	u.src.Set("selectionEnd", start+4)
	u.onSourceChanged() // a programmatic value change fires no input event
	return nil
}

// onSourceChanged re-parses the editor, repaints the highlight overlay and the
// line-number gutter, and re-aligns them with the textarea's scroll position.
func (u *ui) onSourceChanged() {
	src := u.src.Get("value").String()
	u.setSource(src)

	var b strings.Builder
	for _, sp := range playground.Highlight(src) {
		if sp.Class == "" {
			b.WriteString(html.EscapeString(sp.Text))
			continue
		}
		b.WriteString(`<span class="hl-`)
		b.WriteString(sp.Class)
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(sp.Text))
		b.WriteString(`</span>`)
	}
	u.hl.Set("innerHTML", b.String())
	u.gutter.Set("textContent", gutterText(src))
	u.syncScroll()
}

// gutterText is "1\n2\n…\nN" for N source lines, rendered in a white-space:pre
// gutter aligned line-for-line with the editor.
func gutterText(src string) string {
	lines := strings.Count(src, "\n") + 1
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		if i > 1 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(i))
	}
	return b.String()
}

// syncScroll mirrors the textarea's scroll onto the highlight layer and gutter
// via transforms, so the overlay never needs its own scroll container.
func (u *ui) syncScroll() {
	top := strconv.Itoa(u.src.Get("scrollTop").Int())
	left := strconv.Itoa(u.src.Get("scrollLeft").Int())
	u.hl.Get("style").Set("transform", "translate(-"+left+"px,-"+top+"px)")
	u.gutter.Get("style").Set("transform", "translateY(-"+top+"px)")
}

func (u *ui) setSource(src string) {
	ok, status := u.shell.SetSource(context.Background(), src)
	u.badge.Set("textContent", status)
	if ok {
		u.badge.Set("className", "badge ok")
	} else {
		u.badge.Set("className", "badge err")
	}
}

func (u *ui) render(lines []playground.Line) {
	for _, ln := range lines {
		div := u.doc.Call("createElement", "div")
		if ln.Class != "" {
			div.Set("className", ln.Class)
		}
		div.Set("innerHTML", ln.HTML)
		u.out.Call("appendChild", div)
	}
	u.out.Set("scrollTop", u.out.Get("scrollHeight"))
}

// exposeDataAPI installs globalThis.buzz with the raw evaluation functions, so
// the interpreter is scriptable from the browser console independent of the UI.
func exposeDataAPI() {
	api := js.Global().Get("Object").New()
	api.Set("evalBuzz", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		r := playground.EvalBuzz(context.Background(), args[0].String())
		return map[string]any{"ok": r.OK, "result": r.Result, "output": r.Output}
	}))
	// evalBuzzWithRecorder is the spell-docs Run path: it dry-runs a magusfile
	// example (probing its targets under the recording host) and returns the
	// host-op trace the targets would perform, so `import "magus/spell/go";
	// go["go-build"]()` reports a `go build` op instead of failing on a module the
	// bare evalBuzz can't resolve. Trace entries are marshalled as plain objects
	// the client renders as "[target] name detail  kind · recorded" lines.
	api.Set("evalBuzzWithRecorder", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		r := playground.EvalBuzz(context.Background(), args[0].String(), playground.WithRecorder())
		trace := make([]any, len(r.Trace))
		for i, op := range r.Trace {
			trace[i] = map[string]any{
				"target": op.Target, "kind": op.Kind, "name": op.Name, "detail": op.Detail,
			}
		}
		return map[string]any{"ok": r.OK, "output": r.Output, "trace": trace}
	}))
	api.Set("loadMagusfile", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		g := playground.LoadMagusfile(context.Background(), args[0].String())
		return map[string]any{"ok": g.OK, "targets": targetKeys(g.Targets)}
	}))
	js.Global().Set("buzz", api)
}

func targetKeys(ts []playground.Target) []any {
	out := make([]any, len(ts))
	for i, t := range ts {
		out[i] = t.Key
	}
	return out
}
