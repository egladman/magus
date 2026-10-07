package guard

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cli"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPipeRecordsNameRealFields holds every record the pipe rewrite names to the type the
// CLI renders it from, by json key, so a deny can never name a field -o template rejects.
// `ls` renders a type private to cmd/magus; its registry example is the contract instead.
func TestPipeRecordsNameRealFields(t *testing.T) {
	rendered := map[string]any{
		"ls jobs":          types.JobList{},
		"describe file":    types.FileReport{},
		"describe spells":  types.SpellReport{},
		"describe targets": types.TargetReport{},
		"describe modules": types.ModuleReport{},
		"describe charms":  types.CharmReport{},
		"refs":             types.KnowledgeRefsOutput{},
		"query":            types.KnowledgeQueryOutput{},
	}
	for verb, rec := range pipeRecords {
		if verb == "ls" {
			i := slices.IndexFunc(cli.All, func(c cli.Command) bool { return c.Name == "ls" })
			require.GreaterOrEqual(t, i, 0)
			example := strings.Join(exampleCommands(cli.All[i]), "\n")
			assert.Contains(t, example, "{{range ."+rec.list+"}}{{."+rec.id+"}}", verb)
			assert.Empty(t, rec.count, "ls has no count field the registry documents")
			continue
		}
		// Both spellings of a noun render one type.
		v, ok := rendered[verb]
		if !ok {
			v, ok = rendered[strings.TrimSuffix(verb, "s")]
		}
		if !ok {
			v, ok = rendered[verb+"s"]
		}
		require.True(t, ok, "%s: no rendered type to hold the record to", verb)
		list, found := jsonField(reflect.TypeOf(v), rec.list)
		require.True(t, found, "%s: list field %q", verb, rec.list)
		require.Equal(t, reflect.Slice, list.Type.Kind(), "%s: %q is not a list", verb, rec.list)
		_, found = jsonField(list.Type.Elem(), rec.id)
		assert.True(t, found, "%s: id field %q on %s", verb, rec.id, list.Type.Elem())
		if rec.count != "" {
			count, found := jsonField(reflect.TypeOf(v), rec.count)
			require.True(t, found, "%s: count field %q", verb, rec.count)
			assert.Equal(t, reflect.Int, count.Type.Kind(), verb)
		}
	}
}

func exampleCommands(c cli.Command) []string {
	var out []string
	for _, e := range c.Examples {
		out = append(out, e.Command)
	}
	return out
}

// jsonField finds the struct field whose json key is name.
func jsonField(t reflect.Type, name string) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if key == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// TestOutputPipeRewrites: the deny spells the native form of the pipe the reader typed,
// with the count they asked for and the fields their verb's record carries. Measured over
// 6,816 piped magus segments in 21 days of transcripts: `| head -N` on a listing verb was
// 1,815, `| tr` 43 (31 of them joining `-o name` onto one line), `| wc -l` 30.
func TestOutputPipeRewrites(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    string
	}{
		// The first N of a list record, by the field that identifies an element.
		{"magus ls | head -5", `-o template='{{range slice .projects 0 (min 5 (len .projects))}}{{.path}}{{"\n"}}{{end}}'`},
		{"magus ls 2>&1 | head", "min 10 (len .projects)"},
		{"magus ls -o name | head -n 3", "min 3 (len .projects)"},
		{"./magus ls | head --lines=2", "min 2 (len .projects)"},
		{"magus ls libs/foo | head -3", "min 3 (len .projects)"},
		{"magus describe file a.go b.go | head -3", "slice .files 0 (min 3 (len .files))}}{{.path}}"},
		{"magus describe spells -o name | head -4", "slice .spells 0 (min 4 (len .spells))}}{{.name}}"},
		{"magus describe target | head -4", "slice .targets 0 (min 4 (len .targets))}}{{.name}}"},
		{"magus ls jobs | head -20", "slice .jobs 0 (min 20 (len .jobs))}}{{.id}}"},
		{"magus refs Open | head -20", "slice .refs 0 (min 20 (len .refs))}}{{.file}}"},
		{"magus query kind:spell | head -5", "slice .matches 0 (min 5 (len .matches))}}{{.id}}"},
		// A scalar record.
		{"magus version 2>&1 | head -2", "`-o name` prints the bare version."},
		// A count the record carries, else the list's length.
		{"magus describe spells -o name | wc -l", "`-o template='{{.count}}'` is the count the record already carries."},
		{"magus query kind:diagnostic -o name | wc -l", "`-o template='{{.match_count}}'`"},
		{"magus ls jobs | wc -l", "`-o template='{{len .jobs}}'` counts the jobs."},
		{"magus ls -o name | wc", "`-o template='{{len .projects}}'`"},
		// Joining names onto one line, and stripping a capture's trailing newline.
		{"magus describe spells -o name | tr '\\n' ' '", `-o template='{{range .spells}}{{.name}} {{end}}'`},
		{"magus describe targets -o name | tr '\\n' ','", `{{range .targets}}{{.name}},{{end}}`},
		{"magus ls -o name | tr -d '\\n'", `{{range .projects}}{{.path}}{{end}}`},
		{"T=$(magus config mcp token print 2>/dev/null | tr -d '[:space:]')", "A `$(...)` substitution already drops the trailing newline"},
		{"magus config mcp token print | tr -d '\\r'", "`-o template` prints none"},
		// A filter that picks rows or columns names the record's own fields.
		{"magus ls | sort", `-o template='{{range .projects}}{{.path}}{{"\n"}}{{end}}'`},
		{"magus describe file a.go | grep output", "`-o name` prints each path"},
		{"magus ls | cut -d' ' -f1", "{{range .projects}}{{.path}}"},
	} {
		v := Evaluate(strict(testDependencies()), tc.command)
		got := v.Deny + v.Context
		assert.Contains(t, got, tc.want, tc.command)
		assert.Contains(t, got, "magus answers this without the pipe", tc.command)
	}
}

// TestOutputPipeKeepsTheGenericAnswerWithoutARecord pins the gaps: a verb whose record
// the guard does not hold, a mode that renders a different record, and a filter form the
// rewrite cannot read all get the menu rather than a field that may not exist.
func TestOutputPipeKeepsTheGenericAnswerWithoutARecord(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    string
	}{
		// ls targets nests targets under projects: no flat list to slice, no --limit.
		{"magus ls targets | head -5", "`-s` stays quiet"},
		{"magus status | head -5", "`-s` stays quiet"},
		{"magus describe target ci . | head -60", "`-s` stays quiet"},
		{"magus describe job foo | head -40", "`-s` stays quiet"},
		{"magus run test . | tail -30", "`-s` stays quiet"},
		{"magus run test . | head -c 200", "`-s` stays quiet"},
		{"magus ls | head -c 200", "`-s` stays quiet"},
		// refs' other modes render their own records, and its --limit bounds --text alone.
		{"magus refs Open --occurrences | head -20", "`-s` stays quiet"},
		{"magus refs Open --definition | head", "`-s` stays quiet"},
		// Nothing projects "the last N", so tail keeps the menu.
		{"magus ls | tail -3", "`-s` stays quiet"},
		{"magus session | head -5", "`-s` stays quiet"},
		// query's subcommands are not the query record; output is exempt, invocation is not.
		{"magus query invocation abc | head", "`-s` stays quiet"},
		{"magus run generate . | wc -l", "`-o name` prints one id per line"},
		{"magus status | wc -l", "`-o name` prints one id per line"},
		{"magus ls | wc -c", "`-o name` prints one id per line"},
		{"magus status | tr -d ' '", "`-o template` prints none"},
		{"magus ls | tr 'a' 'b'", "`-o name`, `-o json`, or `-o template='{{.field}}'` project the record"},
		{"magus agent install | sort", "`-o name`, `-o json`, or `-o template='{{.field}}'` project the record"},
	} {
		v := Evaluate(strict(testDependencies()), tc.command)
		got := v.Deny + v.Context
		assert.Contains(t, got, tc.want, tc.command)
		assert.NotContains(t, got, "slice .", tc.command)
	}
}

// TestOutputPipeLeavesCompositionAlone: jq over -o json and magus into magus consume a
// contract, and tr on anything but magus is not this rule's business.
func TestOutputPipeLeavesCompositionAlone(t *testing.T) {
	for _, command := range []string{
		"magus ls -o json | jq -r '.projects[].path'",
		"magus ls jobs -o json | jq -r '.jobs[] | select(.state==\"running\") | .id'",
		"magus affected ci --plan | magus run --stdin",
		"magus watch | magus affected --stdin",
		"magus ls -o jsonl --tee /tmp/ls.jsonl",
		"git status --porcelain | tr -d ' '",
		"magus query output out1a2b3c | tr -d '\\r'",
		"magus refs TODO --text | tr ':' '\\t'",
		"magus ls --help | tr -s ' '",
	} {
		v := Evaluate(strict(testDependencies()), command)
		assert.Empty(t, v.Deny, command)
		assert.NotEqual(t, advisoryGraphPipe, v.Kind, command)
	}
}

// TestPipeRecordForRefsModes pins that only refs' default listing is the refs record:
// --text prints grep lines that -o template does not shape.
func TestPipeRecordForRefsModes(t *testing.T) {
	rec, ok := parsePipeRecord([]string{"refs", "Open"})
	assert.True(t, ok)
	assert.Equal(t, pipeRecord{list: "refs", id: "file"}, rec)
	for _, mode := range []string{"--text", "--occurrences", "--definition", "--source"} {
		_, ok := parsePipeRecord([]string{"refs", "Open", mode})
		assert.False(t, ok, mode)
	}
}

func TestHeadCount(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		n       int
		counted bool
	}{
		{nil, 10, true},
		{[]string{"-5"}, 5, true},
		{[]string{"-n", "3"}, 3, true},
		{[]string{"-n3"}, 3, true},
		{[]string{"--lines", "7"}, 7, true},
		{[]string{"--lines=7"}, 7, true},
		{[]string{"-c", "200"}, 0, false},
		{[]string{"-n"}, 0, false},
		{[]string{"-0"}, 0, false},
		{[]string{"file.txt"}, 0, false},
	} {
		n, counted := headCount(tc.args)
		assert.Equal(t, tc.counted, counted, "%v", tc.args)
		assert.Equal(t, tc.n, n, "%v", tc.args)
	}
}
