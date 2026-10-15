package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/internal/generate/emit"
)

// The completion scripts are hand-written shell in four dialects, and the LOGIC in
// them is stable; what drifted was the data. So this generator owns only the marked
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

// shellMarker names the generated list region; all four dialects comment with #.
var shellMarker = emit.CommentMarker("#", "subcommands")

func runCompletions(args []string) error {
	fs := flag.NewFlagSet("completions", flag.ExitOnError)
	outDir := fs.String("out", "completions", "Directory holding the completion scripts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return writeCompletions(*outDir, slices.Concat(cli.All, []cli.Command{cli.Help}))
}

// writeCompletions fills the marked region of each completion script in dir with subs.
func writeCompletions(dir string, subs []cli.Command) error {
	if len(subs) == 0 {
		return errors.New("completions: no subcommands to write")
	}
	renderers := map[string]func([]cli.Command) string{
		"magus.bash": renderBashList,
		"magus.zsh":  renderZshList,
		"magus.fish": renderFishList,
		"magus.ps1":  renderPowerShellList,
	}
	for name, render := range renderers {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		updated, err := emit.Region(string(src), shellMarker, render(subs))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if updated == string(src) {
			continue // already current; do not touch the mtime
		}
		if err := emit.File(path, []byte(updated)); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "completions: wrote %d subcommands to %s\n", len(subs), path)
	}
	return nil
}

func renderBashList(subs []cli.Command) string {
	names := make([]string, len(subs))
	for i, s := range subs {
		names[i] = s.Name
	}
	return `    local subcommands="` + strings.Join(names, " ") + `"`
}

func renderZshList(subs []cli.Command) string {
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

func renderFishList(subs []cli.Command) string {
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

func renderPowerShellList(subs []cli.Command) string {
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
