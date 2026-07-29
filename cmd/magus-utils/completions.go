package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// The completion scripts are hand-written shell in four dialects, and the LOGIC in
// them is stable - what drifted was the data. So this scribe owns only the marked
// list regions and leaves the surrounding shell alone:
//
//	# magus-utils:subcommands:begin
//	  ...regenerated...
//	# magus-utils:subcommands:end
//
// Rewriting the whole script per dialect would mean maintaining four shell templates
// to fix a problem that is entirely about one list. Three subcommands (refs, memory,
// agent) had shipped without reaching any script, and run's --no-volatility-retry was
// still spelled --no-flake-retry from before that rename.
const (
	markerBegin = "magus-utils:subcommands:begin"
	markerEnd   = "magus-utils:subcommands:end"
)

// subcommandDoc mirrors cmd/magus's subcommand struct.
type subcommandDoc struct {
	Name  string
	Short string
}

func runCompletions(args []string) error {
	fs := flag.NewFlagSet("completions", flag.ExitOnError)
	surfacePath := fs.String("surface", "surface.go", "Go declaration of the CLI surface")
	outDir := fs.String("out", "completions", "Directory holding the completion scripts")
	if err := fs.Parse(args); err != nil {
		return err
	}

	subs, err := parseSurface(*surfacePath)
	if err != nil {
		return err
	}
	if len(subs) == 0 {
		return fmt.Errorf("completions: no subcommands found in %s; the parse is wrong, not the surface", *surfacePath)
	}

	renderers := map[string]func([]subcommandDoc) string{
		"magus.bash": renderBashList,
		"magus.zsh":  renderZshList,
		"magus.fish": renderFishList,
		"magus.ps1":  renderPowerShellList,
	}
	for name, render := range renderers {
		path := filepath.Join(*outDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated, err := replaceMarked(string(src), render(subs))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if updated == string(src) {
			continue // already current; do not touch the mtime
		}
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "completions: wrote %d subcommands to %s\n", len(subs), path)
	}
	return nil
}

// parseSurface reads the `subcommands = []subcommand{...}` literal. It parses the Go
// AST rather than matching text so a reformatted or reordered declaration still reads
// correctly - the same approach the config scribe takes with config.go.
func parseSurface(path string) ([]subcommandDoc, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []subcommandDoc
	ast.Inspect(file, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "subcommands" || len(vs.Values) == 0 {
			return true
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range lit.Elts {
			entry, ok := el.(*ast.CompositeLit)
			if !ok {
				continue
			}
			var doc subcommandDoc
			for _, f := range entry.Elts {
				kv, ok := f.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, _ := kv.Key.(*ast.Ident)
				val, _ := kv.Value.(*ast.BasicLit)
				if key == nil || val == nil {
					continue
				}
				switch key.Name {
				case "Name":
					doc.Name = strings.Trim(val.Value, `"`)
				case "Short":
					doc.Short = strings.Trim(val.Value, `"`)
				}
			}
			if doc.Name != "" {
				out = append(out, doc)
			}
		}
		return false
	})
	return out, nil
}

// replaceMarked swaps the region between the markers, preserving both marker lines.
func replaceMarked(src, body string) (string, error) {
	lines := strings.Split(src, "\n")
	begin, end := -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, markerBegin):
			begin = i
		case strings.Contains(l, markerEnd):
			end = i
		}
	}
	if begin < 0 || end < 0 {
		return "", fmt.Errorf("missing %s / %s markers", markerBegin, markerEnd)
	}
	if end < begin {
		return "", fmt.Errorf("%s appears before %s", markerEnd, markerBegin)
	}
	out := append([]string{}, lines[:begin+1]...)
	out = append(out, strings.Split(body, "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

func renderBashList(subs []subcommandDoc) string {
	names := make([]string, len(subs))
	for i, s := range subs {
		names[i] = s.Name
	}
	return `    local subcommands="` + strings.Join(names, " ") + `"`
}

func renderZshList(subs []subcommandDoc) string {
	// The marker wraps the whole assignment, not just the entries: zsh does not treat
	// "#" as a comment inside an array literal, so a marker between the parens is
	// parsed as an array element and the closing paren then fails to parse.
	var b strings.Builder
	b.WriteString("            subcommands=(\n")
	for _, s := range subs {
		// Two escapes, both load-bearing: a literal colon would terminate zsh's
		// name:description pair, and an apostrophe ("a node's neighborhood") would
		// close the single-quoted string and break the whole script's parse.
		desc := strings.ReplaceAll(s.Short, ":", `\:`)
		desc = strings.ReplaceAll(desc, "'", `'\''`)
		b.WriteString("                '" + s.Name + ":" + desc + "'\n")
	}
	b.WriteString("            )")
	return b.String()
}

func renderFishList(subs []subcommandDoc) string {
	var b strings.Builder
	width := 0
	for _, s := range subs {
		if len(s.Name) > width {
			width = len(s.Name)
		}
	}
	for i, s := range subs {
		cont := " \\"
		if i == len(subs)-1 {
			cont = ""
		}
		// fish reads the list as printf arguments, so an apostrophe inside a
		// single-quoted description would close the quote.
		desc := strings.ReplaceAll(s.Short, "'", `\'`)
		fmt.Fprintf(&b, "        %-*s '%s'%s\n", width, s.Name, desc, cont)
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderPowerShellList(subs []subcommandDoc) string {
	// The marker wraps the assignment itself, so this emits it: PowerShell needs the
	// continuation commas, and only the first line carries the variable name.
	var lines []string
	cur := make([]string, 0, 6)
	flush := func(last bool) {
		prefix := "                   "
		if len(lines) == 0 {
			prefix = "    $subcommands = "
		}
		row := prefix + strings.Join(cur, ", ")
		if !last {
			row += ","
		}
		lines = append(lines, row)
		cur = nil
	}
	for i, s := range subs {
		cur = append(cur, "'"+s.Name+"'")
		if len(cur) == 6 || i == len(subs)-1 {
			flush(i == len(subs)-1)
		}
	}
	return strings.Join(lines, "\n")
}
