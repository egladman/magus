package generate

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/clikit/command"
)

// tool is a small tree with every section the renderers emit.
var tool = command.Command{
	Name:  "tool",
	Short: "do things to a workspace",
	Usage: "tool [flags] <subcommand> [args]",
	Long:  "tool does things.\n\nIt reads its config first.",
	Flags: []command.Flag{
		{Name: "root", Kind: command.String, Doc: "Workspace `path`"},
		{Name: "v", Kind: command.Bool, Doc: "Increase verbosity"},
	},
	Env:   []command.Env{{Name: "TOOL_DIR", Key: "dir", Default: ".tool", Doc: "Where the cache lives"}},
	Files: []command.File{{Name: "tool.yaml", Doc: "Configuration."}},
	Children: []command.Command{
		{
			Name:  "run",
			Short: "run a target",
			Usage: "tool run <target> [flags]",
			Flags: []command.Flag{
				{Name: "dry-run", Kind: command.Bool, Default: false, Doc: "Print what would run"},
				{Name: "jobs", Kind: command.Int, Default: 4, Doc: "Run at most `N` at once"},
			},
			Operands: []command.Operand{{Name: "build", Short: "Build it"}},
			Examples: []command.Example{{Comment: "Build everything", Command: "tool run build"}},
		},
		{
			Name:  "config",
			Short: "view or change config",
			Usage: "tool config <subcommand>",
			Children: []command.Command{{
				Name: "cache", Short: "the cache",
				Children: []command.Command{{
					Name: "prune", Short: "prune it",
					Flags: []command.Flag{{Name: "older-than", Kind: command.Duration, Unit: "h", Doc: "Prune entries older than this"}},
				}},
			}},
		},
		{Name: "init", Short: "bootstrap a workspace", Usage: "tool init [flags]"},
	},
}

func pagesByName(pages []Page) map[string]string {
	out := make(map[string]string, len(pages))
	for _, page := range pages {
		out[page.Name] = string(page.Content)
	}
	return out
}

func TestRoff(t *testing.T) {
	rendered, err := Roff(tool, "2026-07-26", "v1.2.3")
	noError(t, err)
	pages := pagesByName(rendered)
	equal(t, 1+len(tool.Children), len(pages))

	contains(t, pages["tool.1"], `.TH TOOL 1 "2026-07-26" "tool v1.2.3" "tool Manual"`)
	contains(t, pages["tool.1"], ".SH GLOBAL FLAGS")
	contains(t, pages["tool.1"], `\fB\-\-root\fR \fIpath\fR`)
	contains(t, pages["tool.1"], `\fBtool\-run\fR(1)`)
	contains(t, pages["tool.1"], ".SH ENVIRONMENT\n.RS\n.TP\n\\fBTOOL_DIR\\fR\nWhere the cache lives (default: \\&.tool). Config file key: \\fBdir\\fR.")
	contains(t, pages["tool.1"], ".SH FILES")

	run := pages["tool-run.1"]
	contains(t, run, ".SH OPTIONS")
	contains(t, run, `\fB\-\-jobs\fR \fIN\fR (default: 4)`)
	contains(t, run, `\fB\-\-[no\-]dry\-run\fR (default: off)`)
	contains(t, run, ".SH OPERANDS")
	contains(t, run, ".SH EXAMPLES")
	notContains(t, run, ".SH ENVIRONMENT")

	// Recursive: a flag three levels down still gets its options section.
	contains(t, pages["tool-config.1"], ".SS config cache prune options")
}

// TestRoffSynopsisNamesTheCommand pins that SYNOPSIS splits the program from its
// arguments rather than emitting a bare ".B " line.
func TestRoffSynopsisNamesTheCommand(t *testing.T) {
	rendered, err := Roff(tool, "", "")
	noError(t, err)
	content := pagesByName(rendered)["tool-init.1"]
	notContains(t, content, ".SH SYNOPSIS\n.B \n")
	contains(t, content, ".B tool\n.RI \"init [flags]\"")
}

// TestRoffExitStatusSectionIsOptional pins both halves of the contract: a command
// declaring codes gets the section, and one declaring none gets no empty heading.
func TestRoffExitStatusSectionIsOptional(t *testing.T) {
	with := command.Command{
		Name:  "guard",
		Short: "judge one command",
		Usage: "tool guard [flags]",
		ExitStatus: []command.ExitCode{
			{Code: 0, Meaning: "allowed"},
			{Code: 2, Meaning: "denied, and also a well-formed call it could not parse"},
		},
	}
	rendered, err := renderCommandRoff(tool, with, "2026-07-26", "v1.2.3")
	noError(t, err)
	contains(t, string(rendered), ".SH EXIT STATUS")
	contains(t, string(rendered), `\fB0\fR`)
	contains(t, string(rendered), `\fB2\fR`)
	// The meaning is escaped like any other prose: an unescaped hyphen renders as a
	// soft-hyphen break rather than a minus.
	contains(t, string(rendered), `well\-formed`)

	without := command.Command{Name: "guard", Short: "judge one command", Usage: "tool guard [flags]"}
	plain, err := renderCommandRoff(tool, without, "2026-07-26", "v1.2.3")
	noError(t, err)
	notContains(t, string(plain), "EXIT STATUS")
}

func TestRoffIsDeterministic(t *testing.T) {
	first, err := Roff(tool, "", "")
	noError(t, err)
	second, err := Roff(tool, "", "")
	noError(t, err)
	equal(t, first, second)
}

func TestRoffNamesTheFlagItCannotBind(t *testing.T) {
	broken := command.Command{Name: "tool", Children: []command.Command{
		{Name: "run", Flags: []command.Flag{{Name: "n", Kind: command.Int, Default: "8"}}},
	}}
	_, err := Roff(broken, "", "")
	isError(t, err)
	contains(t, err.Error(), "flag --n is int but its default 8 is a string")
}

// The helpers below stand in for testify: a library module takes no test dependency.

// message reads msg as testify does: a format string and its arguments.
func message(msg []any) string {
	if len(msg) == 0 {
		return ""
	}
	if format, ok := msg[0].(string); ok {
		return fmt.Sprintf(format, msg[1:]...)
	}
	return fmt.Sprint(msg...)
}

func equal(t *testing.T, want, got any, msg ...any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Errorf("%s\nwant: %#v\ngot:  %#v", message(msg), want, got)
	}
}

func contains(t *testing.T, s, sub string, msg ...any) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("%s\n%q\ndoes not contain\n%q", message(msg), s, sub)
	}
}

func notContains(t *testing.T, s, sub string, msg ...any) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("%s\n%q\ncontains\n%q", message(msg), s, sub)
	}
}

func isTrue(t *testing.T, ok bool, msg ...any) {
	t.Helper()
	if !ok {
		t.Error(message(msg))
	}
}

func empty(t *testing.T, v any, msg ...any) {
	t.Helper()
	if n := reflect.ValueOf(v).Len(); n != 0 {
		t.Errorf("%s: want empty, got %d: %#v", message(msg), n, v)
	}
}

func isError(t *testing.T, err error, msg ...any) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: want an error", message(msg))
	}
}

func noError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
