// Command scip-buzz writes a SCIP index of the Buzz files under the working
// directory.
//
//	scip-buzz [--output FILE] [--workspace-root DIR] [--version]
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/egladman/magus/libs/scipbuzz"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("scip-buzz", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "index.scip", "write the index to `FILE`")
	workspaceRoot := flags.String("workspace-root", "", "resolve symbol paths against `DIR` (default: the nearest directory holding magus.yaml)")
	version := flags.Bool("version", false, "print the version and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *version {
		fmt.Fprintf(stdout, "scip-buzz %s\n", scipbuzz.Version)
		return 0
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "scip-buzz: unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	idx, err := scipbuzz.Index(ctx, scipbuzz.Options{
		WorkspaceRoot: *workspaceRoot,
		Warnf: func(format string, a ...any) {
			fmt.Fprintf(stderr, "scip-buzz: "+format+"\n", a...)
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "scip-buzz: %v\n", err)
		return 1
	}
	idx.Metadata.ToolInfo.Arguments = args
	var buf bytes.Buffer
	if err := scipbuzz.Write(&buf, idx); err != nil {
		fmt.Fprintf(stderr, "scip-buzz: %v\n", err)
		return 1
	}
	if err := writeFile(*output, buf.Bytes()); err != nil {
		fmt.Fprintf(stderr, "scip-buzz: %v\n", err)
		return 1
	}
	return 0
}

// writeFile replaces path through a rename, so a reader never sees a partial index.
func writeFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".scip-buzz-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
