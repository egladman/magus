package generate

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/cmdline"
)

// Completion renders the subcommand list of a shell completion script: the
// assignment the script's own logic completes from, for bash, zsh, fish or
// powershell. The logic around it is hand-written and stable; the list is the part
// that drifts. An unknown shell is an error.
func Completion(shell string, commands []cmdline.Command) (string, error) {
	switch shell {
	case "bash":
		return bashList(commands), nil
	case "zsh":
		return zshList(commands), nil
	case "fish":
		return fishList(commands), nil
	case "powershell":
		return powerShellList(commands), nil
	}
	return "", fmt.Errorf("completion: no renderer for shell %q, want bash, zsh, fish or powershell", shell)
}

func bashList(commands []cmdline.Command) string {
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.Name
	}
	return `    local subcommands="` + strings.Join(names, " ") + `"`
}

func zshList(commands []cmdline.Command) string {
	// The list is the whole assignment, not just the entries: zsh does not treat "#"
	// as a comment inside an array literal, so a marker between the parens would be
	// parsed as an element.
	var b strings.Builder
	b.WriteString("            subcommands=(\n")
	for _, c := range commands {
		// A literal colon would end zsh's name:description pair, and an apostrophe
		// would close the single-quoted string and break the script's parse.
		desc := strings.ReplaceAll(c.Short, ":", `\:`)
		desc = strings.ReplaceAll(desc, "'", `'\''`)
		b.WriteString("                '" + c.Name + ":" + desc + "'\n")
	}
	b.WriteString("            )")
	return b.String()
}

func fishList(commands []cmdline.Command) string {
	var b strings.Builder
	width := 0
	for _, c := range commands {
		width = max(width, len(c.Name))
	}
	for i, c := range commands {
		cont := " \\"
		if i == len(commands)-1 {
			cont = ""
		}
		// fish reads the list as printf arguments, so an apostrophe inside a
		// single-quoted description would close the quote.
		desc := strings.ReplaceAll(c.Short, "'", `\'`)
		fmt.Fprintf(&b, "        %-*s '%s'%s\n", width, c.Name, desc, cont)
	}
	return strings.TrimRight(b.String(), "\n")
}

func powerShellList(commands []cmdline.Command) string {
	// PowerShell needs the continuation commas, and only the first line carries the
	// variable name, so this emits the assignment itself.
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
	for i, c := range commands {
		cur = append(cur, "'"+c.Name+"'")
		if len(cur) == 6 || i == len(commands)-1 {
			flush(i == len(commands)-1)
		}
	}
	return strings.Join(lines, "\n")
}
