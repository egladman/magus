package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/cmdline/output"
)

// The -o formats only magus's graph commands accept, passed to output.Parse as extras.
const (
	outputDot      output.Format = "dot"
	outputTree     output.Format = "tree"
	outputMarkdown output.Format = "markdown" // describe graph: target catalog + dependency graph as a Markdown doc
	outputGraphML  output.Format = "graphml"  // graph export: GraphML XML for external graph viewers
)

// structuredStdout is where structured output goes besides the --tee file. --silent
// with --tee means the caller captures the record in the file and wants stdout empty,
// so it does not pay for the same record twice in its context.
func structuredStdout(out io.Writer) io.Writer {
	if global.silent && global.tee != "" {
		return io.Discard
	}
	return out
}

// outputDestination returns the writer for structured output over out, mirrored to the
// --tee file when set; the caller runs the returned close once it is done writing.
func outputDestination(out io.Writer) (io.Writer, func() error, error) {
	if global.tee == "" {
		return out, func() error { return nil }, nil
	}
	f, err := os.OpenFile(global.tee, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("--tee: %w", err)
	}
	return io.MultiWriter(structuredStdout(out), f), f.Close, nil
}

// openSink builds the invocation's one sink for its -o format, over stdout (mirrored to
// --tee) and stderr; close flushes it and closes the tee. A text sink whose stdout a
// magus stage reads also writes its records there (see pipeRecordStage).
func openSink(ctx context.Context, opts output.Options) (*magus.Sink, func() error, error) {
	stdout := io.Writer(os.Stdout)
	tee := &lazyFile{path: global.tee}
	if global.tee != "" {
		stdout = io.MultiWriter(structuredStdout(os.Stdout), tee)
	}
	sinkOpts := []magus.SinkOption{magus.WithSinkLevel(globalCfg.Log.SlogLevel()), magus.WithSinkFilter(globalCfg.Report.Filter)}
	if opts.Format == outputText && pipeStageFromContext(ctx).writesRecords() {
		sinkOpts = append(sinkOpts, magus.WithSinkRecords(stdout))
	}
	sink, err := magus.NewSink(opts.Format, stdout, os.Stderr, sinkOpts...)
	if err != nil {
		return nil, nil, err
	}
	return sink, func() error { return errors.Join(sink.Close(), tee.Close()) }, nil
}

// lazyFile opens path for appending on the first write, so a format that keeps stdout
// empty leaves no --tee file behind. Not safe for concurrent writes.
type lazyFile struct {
	path string
	f    *os.File
	err  error
}

func (l *lazyFile) Write(p []byte) (int, error) {
	if l.f == nil && l.err == nil {
		l.f, l.err = os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	}
	if l.err != nil {
		return 0, fmt.Errorf("--tee: %w", l.err)
	}
	return l.f.Write(p)
}

func (l *lazyFile) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}

// emitFormatted renders v with opts to the structured-output destination (stdout,
// mirrored to --tee) and closes it. It owns the outputDestination/cleanup pair so every
// command's `-o json|yaml|jsonl|template` arm is one call instead of repeating the
// same destination/defer/writeFormatted dance.
func emitFormatted(opts output.Options, v any) error {
	w, cleanup, err := outputDestination(os.Stdout)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	return writeFormatted(w, opts, v)
}

// emitNames writes the `-o name` form: one bare token per line, to out mirrored to --tee.
//
// The output package does not render -o name and cannot: which field IS the identity of
// a record is the command's to say, not the renderer's. So a command that offers -o name
// renders it through here.
func emitNames(out io.Writer, names []string) error {
	w, cleanup, err := outputDestination(out)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	for _, n := range names {
		if _, err := fmt.Fprintln(w, n); err != nil {
			return err
		}
	}
	return nil
}

// emitItemNames is emitNames over a slice of records, projecting each to its identity.
//
// It exists because the shape it replaces was four lines at every call site -- allocate,
// loop, append, emit -- and most `-o name` arms skipped it and wrote fmt.Println instead.
// That silently bypassed --tee: the flag was accepted, no file was written, and nothing
// said so. A helper only prevents that when using it is shorter than not using it.
func emitItemNames[T any](out io.Writer, items []T, name func(T) string) error {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, name(item))
	}
	return emitNames(out, names)
}

// emitRecord renders every format but text: v in a structured one, names
// under -o name. Taking names here means a command cannot forget -o name.
func emitRecord(opts output.Options, v any, names []string) error {
	if opts.Format == outputName {
		return emitNames(os.Stdout, names)
	}
	return emitFormatted(opts, v)
}

// outputWriter renders json, yaml, jsonl and template through magus's one JSON codec,
// so -o json and a template see the same wire form every other byte magus writes has.
var outputWriter = output.Writer{
	Codec: output.Codec{Marshal: json.Marshal, Unmarshal: json.Unmarshal},
	Funcs: templateFuncs(),
}

// writeFormatted renders v in opts' structured format to w.
func writeFormatted(w io.Writer, opts output.Options, v any) error {
	return outputWriter.Write(w, opts, v)
}

// flagWasSet reports whether flag name was explicitly set (not just defaulted) on fs.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// excludedSprigFuncs lists sprig entries absent from magus templates (non-hermetic or dangerous).
// HermeticTxtFuncMap already drops env/expandenv/getHostByName; this list adds the rest.
var excludedSprigFuncs = []string{
	// non-deterministic
	"now", "date", "dateInZone", "dateModify", "mustDateModify",
	"toDate", "unixEpoch", "htmlDate", "htmlDateInZone",
	"uuidv4",
	// crypto — no business in a build-tool template
	"genPrivateKey", "genCA", "genSelfSignedCert", "genSignedCert",
	"derivePassword", "htpasswd", "bcrypt",
	// confusing in -o context
	"fail",
}

// templateFuncs returns the FuncMap exposed to -o template= bodies.
func templateFuncs() template.FuncMap {
	f := sprig.HermeticTxtFuncMap()
	for _, name := range excludedSprigFuncs {
		delete(f, name)
	}
	// Preserve magus's original bindings; magus's list-first join wins over sprig's
	// reversed (sep-first) join. templateJoin (not strings.Join) so it also handles
	// the []any that json-normalization produces for list fields.
	f["join"] = templateJoin
	f["upper"] = strings.ToUpper
	f["lower"] = strings.ToLower
	f["trim"] = strings.TrimSpace
	return f
}

// templateJoin joins a list's elements with sep, keeping magus's list-first arg order
// ({{join .list ","}}). Unlike strings.Join it accepts ANY slice/array ([]string
// from sprig helpers, and the []any that -o template's json-normalized data yields
// for list fields), stringifying each element. Non-slices stringify whole.
func templateJoin(list any, sep string) string {
	if list == nil { // an absent/null list field joins to empty, like strings.Join(nil, sep)
		return ""
	}
	rv := reflect.ValueOf(list)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return fmt.Sprint(list)
	}
	parts := make([]string, rv.Len())
	for i := range parts {
		parts[i] = fmt.Sprint(rv.Index(i).Interface())
	}
	return strings.Join(parts, sep)
}

// commandsWithoutOutput names the verbs that render nothing structured, keyed by the
// name they hand commandParse.
//
// -o is bound into EVERY FlagSet (bindDisplayFlags), because it is a global flag that
// may precede or follow the subcommand. A verb that never consults it therefore accepted
// `-o json`, printed prose, and exited 0: the accepted-and-ignored failure that costs
// most in a pipe, since the caller's parse fails somewhere else entirely.
//
// Declared rather than derived: deriving it needs a check AFTER the verb has had its
// chance to consult -o, which is a post-dispatch hook this file cannot reach. A wrong
// entry here is loud rather than silent (the verb's own `-o json` tests fail), which is
// what makes the list safe to keep by hand.
var commandsWithoutOutput = map[string]bool{
	"affected --bisect":           true,
	"agent harness install":       true,
	"agent install":               true,
	"agent starter":               true,
	"broker":                      true,
	"broker stop":                 true,
	"buzz":                        true,
	"buzz lsp":                    true,
	"completion":                  true,
	"config cache export":         true,
	"config cache import":         true,
	"config cache prune":          true,
	"config console token create": true,
	"config console token ls":     true,
	"config console token revoke": true,
	"config history dedup":        true,
	"config history import":       true,
	"config history passed":       true,
	"config mcp connector create": true,
	"config mcp connector ls":     true,
	"config mcp connector revoke": true,
	"config set":                  true,
	"config token generate":       true,
	"config token print":          true,
	"config token revoke":         true,
	"config token status":         true,
	"graph build":                 true,
	"graph pull":                  true,
	"graph push":                  true,
	"init":                        true,
	"job watch":                   true,
	"man install":                 true,
	"mcp":                         true,
	"notes capture":               true,
	"notes edit":                  true,
	"queue apply":                 true,
	"queue gate":                  true,
	"queue ls":                    true,
	"queue plan":                  true,
	"queue validate":              true,
	"self install-shorthand":      true,
	"self place":                  true,
	"self update":                 true,
	"server prune-preserved":      true,
	"server reload":               true,
	"server rotate-activities":    true,
	"server rotate-logs":          true,
	"server start":                true,
	"server stop":                 true,
	"vcs merge-driver":            true,
	"vcs resolve":                 true,
	"watch":                       true,
}

// checkOutputSupported rejects -o on a verb that renders nothing structured. Usage-class
// (exit 2): the flag was never going to do anything, so failing before the work happens
// is the whole point.
//
// It asks the FlagSet whether -o was typed on THIS invocation rather than reading
// global.output, which is ambient: bindDisplayFlags seeds each flag's default from the
// live global. A value left there by an earlier dispatch on the same process (one
// adopted run in the server, one testscript command) would otherwise fail the next
// command for a flag nobody passed it.
func checkOutputSupported(name string, fs *flag.FlagSet) error {
	if !commandsWithoutOutput[name] {
		return nil
	}
	if !flagWasSet(fs, "output") && !flagWasSet(fs, "o") {
		return nil
	}
	return exit.Usagef("magus %s: -o is not supported by this command", name)
}

// refuseOutputOnPath is checkOutputSupported for what commandParse never sees:
// a -o before the verb, and a verb with a FlagSet of its own.
// It runs once per process, so output is what this invocation typed.
func refuseOutputOnPath(output string, args []string) error {
	if output == "" {
		return nil
	}
	path := commandPath(args)
	if !commandsWithoutOutput[path] {
		return nil
	}
	return exit.Usagef("magus %s: -o is not supported by this command", path)
}

// commandPath is the deepest command args name in the tree, such as
// "config cache prune". Flags and words no child answers to are skipped.
func commandPath(args []string) string {
	var path []string
	children := commands.Root.Children
	for _, arg := range args {
		if arg == "--" {
			break
		}
		i := slices.IndexFunc(children, func(c cmdline.Command) bool { return c.Name == arg })
		if i < 0 {
			continue
		}
		path = append(path, arg)
		children = children[i].Children
	}
	return strings.Join(path, " ")
}

