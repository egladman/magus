//go:build js && wasm

// Command buzz-playground is the browser entry point for the Buzz playground. It
// compiles to WebAssembly (TinyGo, or the standard js/wasm toolchain) and drives
// the whole page from Go: it owns the terminal — command dispatch, completion,
// history, rendering — and the editor's live parse, manipulating the DOM through
// syscall/js. The page's JavaScript is reduced to a ~10-line bootstrap that
// instantiates this module; all behavior lives here and in
// internal/playground.Shell (which is pure Go and host-tested).
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
	builtWithGo    = "go1.24.7"
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

// defaultSrc is the magusfile the editor opens with — a commented tour of the
// language and magus's features. Kept as a real .buzz file so it reads (and
// highlights) the same in the repo as in the browser.
//
//go:embed showcase.buzz
var defaultSrc string

// ui bundles the DOM handles and the shell, captured by the event handlers.
type ui struct {
	doc    js.Value
	src    js.Value // editor textarea
	hl     js.Value // highlight overlay <code> (innerHTML + scroll transform)
	gutter js.Value // line-number gutter (textContent + scroll transform)
	out    js.Value // terminal scrollback
	in     js.Value // terminal input
	badge  js.Value // editor parse-status
	shell  *playground.Shell
}

func main() {
	doc := js.Global().Get("document")
	u := &ui{
		doc:    doc,
		src:    doc.Call("getElementById", "src"),
		hl:     doc.Call("getElementById", "highlight-code"),
		gutter: doc.Call("getElementById", "gutter"),
		out:    doc.Call("getElementById", "term-out"),
		in:     doc.Call("getElementById", "term-in"),
		badge:  doc.Call("getElementById", "parse-status"),
		shell:  playground.NewShell(buildInfo()),
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

	// Seed the editor, parse + highlight it, and show the sandbox banner.
	u.src.Set("value", defaultSrc)
	u.onSourceChanged()
	u.render(u.shell.Banner())

	if loading := doc.Call("getElementById", "loading"); loading.Truthy() {
		loading.Call("remove")
	}
	u.in.Set("disabled", false)
	u.in.Call("focus")
	showIntroOnce(doc)

	// A small data API for the browser console / scripting, in addition to the UI.
	exposeDataAPI()

	<-make(chan struct{}) // keep the exported callbacks alive
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

// ── event handlers ────────────────────────────────────────────────────────────

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

// ── rendering ─────────────────────────────────────────────────────────────────

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

// ── console data API ──────────────────────────────────────────────────────────

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
