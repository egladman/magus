// Package emit writes magus's generated artifacts: the last stage of every generator,
// after its source of truth has been read and its output rendered.
//
// "emit" is the compiler-pipeline term for this step (parse, analyze, emit), and it is
// deliberately narrower than a name like codegen: this package generates nothing, it
// WRITES what a generator produced - idempotently, and formatted when the artifact is
// Go. The reading half lives beside each source of truth.
//
// Build-time only. Nothing in the magus binary imports it and nothing should, so
// internal/ keeps it unimportable from outside the module - the right shape for code
// whose whole job is producing checked-in files.
//
// Note the directory vocabulary this fits into: a gen/ directory holds generated
// OUTPUT (there are ten of them in this repo), so generator code never lives in one.
package emit

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"strings"
	"text/template"
)

// FileMode is the permission every generated file is written with. Generated output
// is source-adjacent and checked in, so it is readable and never executable.
const FileMode os.FileMode = 0o644

// File writes data to path only when the bytes differ.
//
// Skipping an identical write is not a micro-optimization: magus keys its cache on
// file content and mtime, so rewriting an unchanged generated file marks every
// downstream target dirty and re-runs work that had nothing to do. Two scribes
// bypassed this with a bare os.WriteFile before it lived here.
func File(path string, data []byte) error {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, FileMode)
}

// Go gofmts data before writing it.
//
// The format step is the guard that matters: a template that emits unparsable Go
// fails HERE, naming the file, rather than at the next build with an error pointing
// into generated output nobody wrote by hand.
func Go(path string, data []byte) error {
	formatted, err := format.Source(data)
	if err != nil {
		return fmt.Errorf("gofmt %s: %w", path, err)
	}
	return File(path, formatted)
}

// GoTemplate executes tmpl into path as gofmt'd Go source.
func GoTemplate(path string, tmpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("template %s: %w", path, err)
	}
	if err := Go(path, buf.Bytes()); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Marker names a generated region inside an otherwise hand-maintained file.
//
// Some artifacts are mostly hand-written with one generated list in them - the shell
// completion scripts are four dialects of stable hand-written logic wrapped around a
// subcommand list that drifted. Owning the whole file would mean maintaining four
// shell templates to fix a problem that is entirely about one list.
type Marker struct {
	Begin string
	End   string
}

// CommentMarker builds a Marker from a comment prefix and a region name, e.g.
// CommentMarker("#", "subcommands") for shell.
func CommentMarker(comment, name string) Marker {
	return Marker{
		Begin: comment + " magus-utils:" + name + ":begin",
		End:   comment + " magus-utils:" + name + ":end",
	}
}

// Region swaps the lines between m.Begin and m.End for body, keeping both
// marker lines. Indentation on the marker lines is preserved as written, so a marker
// can sit inside an indented block.
//
// A missing marker is an error rather than an append: the generator cannot know where
// in a hand-written file the region belongs, and guessing would corrupt it.
func Region(src string, m Marker, body string) (string, error) {
	lines := strings.Split(src, "\n")
	begin, end := -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, m.Begin):
			if begin >= 0 {
				return "", fmt.Errorf("%s appears more than once", m.Begin)
			}
			begin = i
		case strings.Contains(l, m.End):
			if end >= 0 {
				return "", fmt.Errorf("%s appears more than once", m.End)
			}
			end = i
		}
	}
	switch {
	case begin < 0:
		return "", fmt.Errorf("missing marker %s", m.Begin)
	case end < 0:
		return "", fmt.Errorf("missing marker %s", m.End)
	case end < begin:
		return "", fmt.Errorf("%s appears before %s", m.End, m.Begin)
	}

	out := make([]string, 0, len(lines))
	out = append(out, lines[:begin+1]...)
	out = append(out, strings.Split(body, "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}
