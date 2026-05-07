package interp

import (
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/interp/engine"
	"github.com/fatih/color"
)

// PrettyOpts configures the pretty-printer.
type PrettyOpts struct {
	MaxDepth int    // recursion limit; default 4
	Indent   string // per-level indent; default "  "
	Color    bool   // ANSI color; REPL sets based on stream + NO_COLOR
}

// PrettyPrint writes a human-readable rendering of v to w with cycle detection.
func PrettyPrint(w io.Writer, v engine.Value, opts PrettyOpts) {
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 4
	}
	if opts.Indent == "" {
		opts.Indent = "  "
	}
	p := &prettyPrinter{w: w, opts: opts, seen: map[uintptr]bool{}}
	p.print(v, 0)
	fmt.Fprintln(w)
}

type prettyPrinter struct {
	w    io.Writer
	opts PrettyOpts
	seen map[uintptr]bool // keyed by table pointer for correct cycle detection
}

func (p *prettyPrinter) print(v engine.Value, depth int) {
	if v == nil || v.IsNil() {
		p.write(p.colorize("nil", color.FgRed))
		return
	}
	if t, ok := v.AsTable(); ok {
		p.printTable(t, depth)
		return
	}
	if s, ok := v.AsString(); ok {
		p.write(p.colorize(fmt.Sprintf("%q", s), color.FgYellow))
		return
	}
	if n, ok := v.AsNumber(); ok {
		p.write(p.colorize(formatNumber(n), color.FgMagenta))
		return
	}
	s := v.String()
	if s == "true" || s == "false" {
		p.write(p.colorize(s, color.FgRed))
		return
	}
	p.write(s)
}

func (p *prettyPrinter) printTable(t engine.Table, depth int) {
	key := reflect.ValueOf(t).Pointer()
	if p.seen[key] {
		p.write(p.colorize("<cycle>", color.FgRed))
		return
	}
	if depth >= p.opts.MaxDepth {
		p.write(p.colorize("{...}", color.Faint))
		return
	}
	p.seen[key] = true
	defer delete(p.seen, key)

	type entry struct {
		k engine.Value
		v engine.Value
	}
	var strs, nums, other []entry
	t.ForEach(func(k, v engine.Value) {
		if _, ok := k.AsString(); ok {
			strs = append(strs, entry{k, v})
		} else if _, ok := k.AsNumber(); ok {
			nums = append(nums, entry{k, v})
		} else {
			other = append(other, entry{k, v})
		}
	})
	sort.Slice(strs, func(i, j int) bool {
		a, _ := strs[i].k.AsString()
		b, _ := strs[j].k.AsString()
		return a < b
	})
	sort.Slice(nums, func(i, j int) bool {
		a, _ := nums[i].k.AsNumber()
		b, _ := nums[j].k.AsNumber()
		return a < b
	})

	all := slices.Concat(nums, strs, other)
	if len(all) == 0 {
		p.write("{}")
		return
	}
	pad := strings.Repeat(p.opts.Indent, depth+1)
	end := strings.Repeat(p.opts.Indent, depth)
	p.write("{\n")
	for i, e := range all {
		p.write(pad)
		if s, ok := e.k.AsString(); ok && isIdent(s) {
			p.write(p.colorize(s, color.FgCyan))
			p.write(" = ")
		} else if n, ok := e.k.AsNumber(); ok {
			p.write(p.colorize("["+formatNumber(n)+"]", color.FgCyan))
			p.write(" = ")
		} else if s, ok := e.k.AsString(); ok {
			p.write(p.colorize(fmt.Sprintf("[%q]", s), color.FgCyan))
			p.write(" = ")
		} else {
			p.write("[")
			p.write(e.k.String())
			p.write("] = ")
		}
		p.print(e.v, depth+1)
		if i < len(all)-1 {
			p.write(",")
		}
		p.write("\n")
	}
	p.write(end)
	p.write("}")
}

func (p *prettyPrinter) write(s string) {
	_, _ = io.WriteString(p.w, s)
}

func (p *prettyPrinter) colorize(s string, attrs ...color.Attribute) string {
	if !p.opts.Color {
		return s
	}
	return color.New(attrs...).Sprint(s)
}

func formatNumber(n float64) string {
	if n == float64(int64(n)) {
		return fmt.Sprintf("%d", int64(n))
	}
	return fmt.Sprintf("%g", n)
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		//nolint:staticcheck // QF1001: negated character-class disjunction is clearer than the De Morgan form
		if i == 0 && !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
		//nolint:staticcheck // QF1001: negated character-class disjunction is clearer than the De Morgan form
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}
