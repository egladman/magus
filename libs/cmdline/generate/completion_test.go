package generate

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/cmdline"
)

// awkward carries the two characters that have broken a generated script: a colon
// ends zsh's name:description pair, and an apostrophe closes the single-quoted
// string in zsh, fish and PowerShell alike.
var awkward = []cmdline.Command{
	{Name: "run", Short: "Run it"},
	{Name: "query", Short: "a node's neighborhood: really"},
}

func completion(t *testing.T, shell string, commands []cmdline.Command) string {
	t.Helper()
	got, err := Completion(shell, commands)
	noError(t, err)
	return got
}

func TestCompletionBash(t *testing.T) {
	equal(t, `    local subcommands="run query"`, completion(t, "bash", awkward))
}

func TestCompletionZshEscapesColonsAndApostrophes(t *testing.T) {
	want := "            subcommands=(\n" +
		"                'run:Run it'\n" +
		"                'query:a node" + `'\''` + "s neighborhood" + `\:` + " really'\n" +
		"            )"
	equal(t, want, completion(t, "zsh", awkward))
}

func TestCompletionFishPadsToTheWidestNameAndDropsTheLastContinuation(t *testing.T) {
	want := "        run   'Run it' \\" + "\n" +
		`        query 'a node\'s neighborhood: really'`
	equal(t, want, completion(t, "fish", awkward))
}

func TestCompletionPowerShellBreaksAtSix(t *testing.T) {
	var commands []cmdline.Command
	for _, name := range strings.Split("abcdefg", "") {
		commands = append(commands, cmdline.Command{Name: name})
	}
	want := "    $subcommands = 'a', 'b', 'c', 'd', 'e', 'f',\n" +
		"                   'g'"
	equal(t, want, completion(t, "powershell", commands))
}

// The sole row carries the variable name and no trailing comma.
func TestCompletionPowerShellSingleRow(t *testing.T) {
	equal(t, "    $subcommands = 'run', 'query'", completion(t, "powershell", awkward))
}

func TestCompletionRefusesAnUnknownShell(t *testing.T) {
	_, err := Completion("tcsh", awkward)
	isError(t, err)
}
