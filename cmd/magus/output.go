package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/pipe"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
)

// The -o formats only magus's graph commands accept, passed to output.Parse as extras.
const (
	outputDot      output.Format = "dot"
	outputTree     output.Format = "tree"
	outputMarkdown output.Format = "markdown" // describe graph: target catalog + dependency graph as a Markdown doc
	outputGraphML  output.Format = "graphml"  // graph export: GraphML XML for external graph viewers
)

// openSink builds the invocation's one sink for its -o format, over stdout (mirrored to
// --tee) and stderr; close flushes it and closes the tee. A text sink whose stdout a
// magus stage reads also writes its records there (see pipeRecordStage).
func openSink(ctx context.Context, opts output.Options) (*magus.Sink, func() error, error) {
	stdout := io.Writer(os.Stdout)
	tee := render.NewLazyFile(&global.tee)
	if global.tee.Path != "" {
		stdout = io.MultiWriter(global.tee.StructuredStdout(os.Stdout, global.silent), tee)
	}
	sinkOpts := []magus.SinkOption{magus.WithSinkLevel(globalCfg.Log.SlogLevel()), magus.WithSinkFilter(globalCfg.Report.Filter)}
	if opts.Format == outputText && pipe.StageFromContext(ctx).WritesRecords() {
		sinkOpts = append(sinkOpts, magus.WithSinkRecords(stdout))
	}
	sink, err := magus.NewSink(opts.Format, stdout, os.Stderr, sinkOpts...)
	if err != nil {
		return nil, nil, err
	}
	return sink, func() error { return errors.Join(sink.Close(), tee.Close()) }, nil
}

// writeFormatted renders v in opts' structured format to w. queue.go calls it by this name.
func writeFormatted(w io.Writer, opts output.Options, v any) error {
	return render.WriteFormatted(w, opts, v)
}

// emitFormatted renders v with opts to the structured-output destination (stdout,
// mirrored to --tee) and closes it. It owns the outputDestination/cleanup pair so every
// command's `-o json|yaml|jsonl|template` arm is one call instead of repeating the
// same destination/defer/writeFormatted dance.
func emitFormatted(opts output.Options, v any) error {
	w, cleanup, err := global.tee.Destination(os.Stdout, global.silent)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()
	return render.WriteFormatted(w, opts, v)
}

// emitNames writes the `-o name` form: one bare token per line, to out mirrored to --tee.
//
// The output package does not render -o name and cannot: which field IS the identity of
// a record is the command's to say, not the renderer's. So a command that offers -o name
// renders it through here.
func emitNames(out io.Writer, names []string) error {
	w, cleanup, err := global.tee.Destination(out, global.silent)
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

// unstructuredCommand reads the declaration, so dispatch and flag parsing share
// the command tree's decision about who owns stdout.
func unstructuredCommand(name string) bool {
	var raw bool
	command.Walk(commands.Root.Spec.Command, func(path string, c command.Command) {
		if path == name {
			raw = c.UnstructuredOutput
		}
	})
	return raw
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
	if !unstructuredCommand(name) {
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
	if !unstructuredCommand(path) {
		return nil
	}
	return exit.Usagef("magus %s: -o is not supported by this command", path)
}

// commandPath is the deepest command args name in the tree, such as
// "config cache prune". Flags and words no child answers to are skipped.
func commandPath(args []string) string {
	var path []string
	children := commands.Root.Spec.Children
	for _, arg := range args {
		if arg == "--" {
			break
		}
		i := slices.IndexFunc(children, func(c command.Command) bool { return c.Name == arg })
		if i < 0 {
			continue
		}
		path = append(path, arg)
		children = children[i].Children
	}
	return strings.Join(path, " ")
}
