package guard

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/cli"
)

// pipeRecord is a verb's structured output in -o template's json keys: the list a text
// row comes from, the field naming one element, and the count it carries, "" for none.
// TestPipeRecordsNameRealFields holds each entry to the type the CLI emits.
type pipeRecord struct {
	list, id, count string
}

// pipeRecords is keyed on the verb and its noun, in both spellings where the CLI takes both.
// A verb absent here gets the generic answer, and the gap is worth measuring before a
// projection feature is added for it.
var pipeRecords = map[string]pipeRecord{
	"ls":               {list: "projects", id: "path"},
	"ls jobs":          {list: "jobs", id: "id"},
	"ls job":           {list: "jobs", id: "id"},
	"describe file":    {list: "files", id: "path", count: "count"},
	"describe files":   {list: "files", id: "path", count: "count"},
	"describe spell":   {list: "spells", id: "name", count: "count"},
	"describe spells":  {list: "spells", id: "name", count: "count"},
	"describe targets": {list: "targets", id: "name", count: "count"},
	"describe module":  {list: "modules", id: "name", count: "count"},
	"describe modules": {list: "modules", id: "name", count: "count"},
	"describe charm":   {list: "charms", id: "name", count: "count"},
	"describe charms":  {list: "charms", id: "name", count: "count"},
	"refs":             {list: "refs", id: "file"},
	"query":            {list: "matches", id: "id", count: "match_count"},
}

// pipeRecordFor resolves the record a magus argv renders. ok is false when the guard holds
// none for the verb, or a flag or noun changes the shape: `describe target <ref>` renders
// one target's graph, refs' --occurrences, --definition and --source their own records, and
// refs --text prints grep lines that ignore -o template.
func pipeRecordFor(args []string) (rec pipeRecord, ok bool) {
	words := magusSubcommandWords(args)
	if len(words) == 0 {
		return pipeRecord{}, false
	}
	if len(words) == 2 && words[0] == "describe" && words[1] == "target" {
		return pipeRecords["describe targets"], true
	}
	if len(words) > 1 {
		if rec, ok = pipeRecords[words[0]+" "+words[1]]; ok {
			return rec, true
		}
		if registryCommand(words[:2]) {
			return pipeRecord{}, false
		}
	}
	if words[0] == "refs" {
		for _, mode := range []string{"occurrences", "definition", "source", "text"} {
			if magusFlag(args, mode) {
				return pipeRecord{}, false
			}
		}
	}
	rec, ok = pipeRecords[words[0]]
	return rec, ok
}

// registryCommand reports whether words walk the CLI registry down to a command.
func registryCommand(words []string) bool {
	level := cli.All
	for _, w := range words {
		i := slices.IndexFunc(level, func(c cli.Command) bool { return c.Name == w })
		if i < 0 {
			return false
		}
		level = level[i].Children
	}
	return len(words) > 0
}

// pipeRewrite is the native spelling of the pipe, or false when magus has no projection
// that answers it. No --limit is offered for `| head -N`: refs' bounds --text matches
// only, which the registry cannot say.
func pipeRewrite(p pipedMagus) (string, bool) {
	words := magusSubcommandWords(p.args)
	rec, known := pipeRecordFor(p.args)
	switch p.filter {
	case "head":
		n, counted := headCount(p.filterArgs)
		switch {
		case !counted:
			return "", false
		case len(words) > 0 && words[0] == "version":
			// The registry example `magus version -o name` documents the bare version.
			return "`-o name` prints the bare version.", true
		case known:
			return fmt.Sprintf("`-o template='{{range slice .%[1]s 0 (min %[2]d (len .%[1]s))}}{{.%[3]s}}{{\"\\n\"}}{{end}}'` prints the first %[2]d; `-o name` prints every %[3]s.",
				rec.list, n, rec.id), true
		}
	case "wc":
		if !known || !countsLines(p.filterArgs) {
			return "", false
		}
		if rec.count != "" {
			return "`-o template='{{." + rec.count + "}}'` is the count the record already carries.", true
		}
		return "`-o template='{{len ." + rec.list + "}}'` counts the " + rec.list + ".", true
	case "tr":
		if sep, joins := trJoiner(p.filterArgs); joins && known {
			return "`-o template='{{range ." + rec.list + "}}{{." + rec.id + "}}" + sep + "{{end}}'` joins the " + rec.id + "s on one line.", true
		}
		if trTrims(p.filterArgs) {
			return "A `$(...)` substitution already drops the trailing newline, and `-o template` prints none.", true
		}
	case "grep", "egrep", "fgrep", "rg", "ag", "sort", "uniq", "cut", "awk", "sed", "column":
		if known {
			return "`-o name` prints each " + rec.id + "; `-o template='{{range ." + rec.list + "}}{{." + rec.id + "}}{{\"\\n\"}}{{end}}'` picks fields, and a bare `-o template` lists them.", true
		}
	}
	return "", false
}

// headCount reads how many lines a head asked for (-N, -n N, -nN, --lines N, --lines=N),
// 10 when it said nothing. counted is false for a byte count or a form it cannot read.
func headCount(args []string) (n int, counted bool) {
	n = 10
	for i := 0; i < len(args); i++ {
		a := args[i]
		var value string
		switch {
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return 0, false
			}
			i++
			value = args[i]
		case strings.HasPrefix(a, "--lines="):
			value = strings.TrimPrefix(a, "--lines=")
		case strings.HasPrefix(a, "-n"):
			value = strings.TrimPrefix(a, "-n")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			value = a[1:]
		default:
			return 0, false
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return 0, false
		}
		n = parsed
	}
	return n, true
}

// countsLines reports a wc that prints a line count: bare, or -l among its flags.
func countsLines(args []string) bool {
	if len(args) == 0 {
		return true
	}
	return slices.ContainsFunc(args, func(a string) bool {
		return strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "l")
	})
}

// trJoiner reads a tr that turns newlines into a separator (`tr '\n' ' '`, `tr -d '\n'`)
// and returns the separator the join needs.
func trJoiner(args []string) (sep string, joins bool) {
	switch {
	case len(args) == 2 && args[0] == "-d" && isNewline(args[1]):
		return "", true
	case len(args) == 2 && isNewline(args[0]):
		return args[1], true
	}
	return "", false
}

// trTrims reports a tr that only strips whitespace, the shape a `$(...)` capture reaches
// for to lose the trailing newline.
func trTrims(args []string) bool {
	if len(args) != 2 || args[0] != "-d" {
		return false
	}
	set := strings.NewReplacer(`\n`, "", `\r`, "", "\n", "", "\r", "", "[:space:]", "", " ", "", `\t`, "").Replace(args[1])
	return set == ""
}

func isNewline(s string) bool { return s == `\n` || s == "\n" }
