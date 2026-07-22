//go:build js && wasm

// Command buzz-playground is the browser entry point for the Buzz playground. It
// compiles to WebAssembly (TinyGo, or the standard js/wasm toolchain) and drives
// the whole page from Go: it owns the terminal (command dispatch, completion,
// history, rendering) and the editor's live parse via syscall/js. The page's
// JavaScript is a ~10-line bootstrap that instantiates this module; all behavior
// lives here and in internal/playground.Console (pure Go and host-tested).
//
// The page provides the static structure (see docs/playground.html); this
// program grabs the elements by id, wires handlers, and renders into them.
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

	"github.com/egladman/magus/internal/dry"
	"github.com/egladman/magus/internal/langservice"
	"github.com/egladman/magus/internal/playground"
)

// TinyGo's scheduler and the Go release it wraps aren't reachable at runtime
// (TinyGo ignores -ldflags -X), so these pins track the build-playground TinyGo
// invocation (-scheduler=asyncify) manually; bump them when you bump TinyGo,
// which is already a manual rebuild of the committed wasm.
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

// The editor opens on one minimal hello-world magusfile. Curated, guided examples
// now live on the site's Tour page, which deep-links each into this playground;
// the editor no longer ships a picker.
//
//go:embed hello.buzz
var helloSrc string // the minimal magusfile the editor opens with

// example is the entry seeded into the editor on boot.
type example struct {
	id, label, file, src string
}

var examples = []example{
	{"hello", "Hello world", "magusfile.buzz", helloSrc},
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
	// (Run buttons on docs pages; console scripting) always have the raw
	// evaluation API, whether or not the playground's editor markup is on this
	// page. The UI setup below assumes the playground DOM and would panic on a
	// bare docs page.
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

	// Seed the editor with the minimal example (loadExample parses + highlights),
	// then show the sandbox banner.
	u.loadExample(examples[0].id)
	// A #source=<base64url> deep link replaces the editor contents with the
	// decoded source. The hash keeps source client-side (never sent to the server
	// / CDN / referrer / logs), which is why it's a hash and not a ?code= query.
	// Any load failure silently falls back to the seeded example.
	u.applyHashSource()
	u.render(u.shell.Banner())

	if loading := doc.Call("getElementById", "loading"); loading.Truthy() {
		loading.Call("remove")
	}
	u.in.Set("disabled", false)
	// preventScroll: the console input sits near the bottom of a viewport-tall panel,
	// so a plain focus() at boot yanks the page down to it - the reader should land at
	// the top (title + editor), not scrolled past them.
	u.in.Call("focus", map[string]any{"preventScroll": true})
	showIntroOnce(doc)

	<-make(chan struct{}) // keep the exported callbacks alive
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

// applyHashSource decodes a `#source=<base64url>` URL fragment into the editor,
// replacing whatever example was seeded. This is how "Open in Playground"
// deep-links from the docs and the future Share button pass source into the page:
// through the URL hash, so the content stays client-side and never rides an HTTP
// request. Any malformed hash silently no-ops (the seeded example stays put).
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
		// Base64URL-decode via the browser's atob rather than encoding/base64
		// (TinyGo builds this file, and extra encoding packages balloon the wasm):
		// convert URL-safe -> standard alphabet, re-pad, then atob.
		s := strings.ReplaceAll(v, "-", "+")
		s = strings.ReplaceAll(s, "_", "/")
		switch len(s) % 4 {
		case 2:
			s += "=="
		case 3:
			s += "="
		}
		// atob returns a "binary string" (each JS char = one decoded byte); invalid
		// input throws, so recover to keep the boot path silent.
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
		return // already dismissed; leave it hidden
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

// gutterText is "1\n2\n...\nN" for N source lines, rendered in a white-space:pre
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
		r := dry.Eval(context.Background(), args[0].String())
		return map[string]any{"ok": r.OK, "result": r.Result, "output": r.Output}
	}))
	// evalBuzzWithRecorder is the spell-docs Run path: it dry-runs a magusfile
	// example under the tracing host and returns the host-op trace its targets
	// would perform, so `import "magus/spell/go"; go["go-build"]()` reports a
	// `go build` op instead of failing on a module the bare evalBuzz can't
	// resolve. Trace entries marshal as plain objects the client renders as
	// "[target] name detail  kind · would run" lines.
	api.Set("evalBuzzWithRecorder", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		r := dry.Eval(context.Background(), args[0].String(), dry.WithTracer())
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
		g := dry.LoadMagusfile(context.Background(), args[0].String())
		return map[string]any{"ok": g.OK, "targets": targetKeys(g.Targets)}
	}))
	// The language-service trio (diagnostics / complete / hover) is the editor's
	// IDE surface: the CodeMirror adapters call these to draw squiggles, populate
	// the completion popup, and show hover tooltips. All three are pure functions of
	// (source, cursor) - no session state - so the page can call them freely on each
	// keystroke. offset is a UTF-8 byte offset into source (the adapter converts from
	// the editor's UTF-16 position); positions in results are 1-based line/col.
	api.Set("diagnostics", dataHandler(func(args []js.Value) any {
		if len(args) < 1 {
			return nil
		}
		ds := dry.Diagnostics(context.Background(), args[0].String())
		out := make([]any, len(ds))
		for i, d := range ds {
			out[i] = map[string]any{"line": d.Line, "col": d.Col, "msg": d.Msg}
		}
		return out
	}))
	api.Set("complete", dataHandler(func(args []js.Value) any {
		off, ok := numberArg(args, 1)
		if !ok {
			return nil
		}
		cs := langservice.CompleteAt(args[0].String(), off)
		out := make([]any, len(cs))
		for i, c := range cs {
			out[i] = map[string]any{
				"label": c.Label, "kind": string(c.Kind),
				"detail": c.Detail, "doc": c.Doc, "replace": c.Replace,
			}
		}
		return out
	}))
	api.Set("hover", dataHandler(func(args []js.Value) any {
		off, ok := numberArg(args, 1)
		if !ok {
			return nil
		}
		h := langservice.HoverAt(args[0].String(), off)
		if h == nil {
			return nil
		}
		return map[string]any{"title": h.Title, "doc": h.Doc}
	}))
	// signature is call-signature help: given the cursor inside a call's arg list, it
	// returns the callee's signature and doc for the editor to float above the line.
	api.Set("signature", dataHandler(func(args []js.Value) any {
		off, ok := numberArg(args, 1)
		if !ok {
			return nil
		}
		s := langservice.SignatureAt(args[0].String(), off)
		if s == nil {
			return nil
		}
		return map[string]any{"label": s.Label, "doc": s.Doc}
	}))
	// excludedModules lists the host modules that do NOT run in this build: the ones
	// needing a process, filesystem, or network the browser can't provide. The page
	// renders it into a collapsible notice, so that list is never hand-kept in HTML.
	api.Set("excludedModules", js.FuncOf(func(js.Value, []js.Value) any {
		mods := langservice.ExcludedModules(dry.PlaygroundHostModules())
		out := make([]any, len(mods))
		for i, m := range mods {
			out[i] = map[string]any{"name": m.Name, "doc": m.Doc}
		}
		return out
	}))
	js.Global().Set("buzz", api)
}

// dataHandler wraps a language-service callback so a panic on adversarial or
// half-typed source surfaces to JS as null instead of aborting the whole wasm
// instance. The unnamed `any` return is nil after a recovered panic.
func dataHandler(fn func(args []js.Value) any) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer func() { _ = recover() }()
		return fn(args)
	})
}

// numberArg reads args[i] as an int, but only when it is actually a JS number.
// js.Value.Int() panics on a non-number, and these entry points are reachable via
// globalThis.buzz for console scripting (not just the UI, which always passes a
// number), so a bad offset must return not-ok rather than crash the module.
func numberArg(args []js.Value, i int) (int, bool) {
	if i >= len(args) || args[i].Type() != js.TypeNumber {
		return 0, false
	}
	return args[i].Int(), true
}

func targetKeys(ts []dry.Target) []any {
	out := make([]any, len(ts))
	for i, t := range ts {
		out[i] = t.Key
	}
	return out
}
