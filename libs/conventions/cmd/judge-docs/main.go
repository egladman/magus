// Command judge-docs judges prose against the prose rules and writes a JSON
// array of findings on stdout.
//
// With no flag it reads a JSON array of symbol records on stdin and judges
// each one's doc, reporting in input order and then the order [prose.Judge]
// reports them. With -surface markdown it judges the Markdown files its
// arguments name, in argument order; -surface guide judges them as procedural
// pages, held to the guide rules as well; -surface skill judges them as skills an
// agent loads as written, and -surface skill-source as skill bodies
// internal/agent renders into a short and a full form, each finding at its
// source line. With -surface pull-request it reads a pull
// request on stdin, the title on the first line and the description after it.
// A finding from text names its file, or "pull-request", and its line as
// source, `path:line`, the way a symbol's index position reads.
//
// This repository's lint rules and its pull request guard and CI step run it.
// The magus module never imports libs/conventions, so the rules stay this
// repository's policy.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/egladman/magus/libs/conventions/prose"
)

// record is one symbol as `magus\symbols()` describes it.
type record struct {
	Node     string `json:"node"`
	Source   string `json:"source"`
	Language string `json:"language"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Owner    string `json:"owner"`
	Doc      string `json:"doc"`
}

// finding is one rule's verdict, carrying the record's identity so a caller can
// point at the declaration.
type finding struct {
	Node     string `json:"node"`
	Source   string `json:"source"`
	Language string `json:"language"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
	Match    string `json:"match"`
}

// pullRequestSource names a pull request's findings, which have no file.
const pullRequestSource = "pull-request"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run returns the process exit code: 0 once the findings are written, 1 when
// the flags or the input cannot be read. A finding is not a failure: the
// caller decides what one costs.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("judge-docs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	surface := fs.String("surface", "",
		"judge `markdown`, guide, skill or skill-source files named as arguments, or a pull-request on stdin")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	out, err := judge(prose.Surface(*surface), fs.Args(), stdin)
	if err != nil {
		fmt.Fprintf(stderr, "judge-docs: %v\n", err)

		return 1
	}

	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		fmt.Fprintf(stderr, "judge-docs: write findings: %v\n", err)

		return 1
	}

	return 0
}

func judge(surface prose.Surface, paths []string, stdin io.Reader) ([]finding, error) {
	switch surface {
	case "":
		if len(paths) > 0 {
			return nil, errors.New("symbols are read from stdin; a path needs -surface markdown")
		}

		return judgeSymbols(stdin)
	case prose.SurfaceMarkdown, prose.SurfaceGuide, prose.SurfaceSkill, prose.SurfaceSkillSource:
		return judgeFiles(paths, surface)
	case prose.SurfacePullRequest:
		if len(paths) > 0 {
			return nil, errors.New("a pull request is read from stdin, not from a path")
		}

		text, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read the pull request: %w", err)
		}

		return textFindings(pullRequestSource, string(text), surface), nil
	default:
		return nil, fmt.Errorf("unknown surface %q: want markdown, guide, skill, skill-source or pull-request", surface)
	}
}

func judgeFiles(paths []string, surface prose.Surface) ([]finding, error) {
	out := []finding{}

	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		out = append(out, textFindings(path, string(text), surface)...)
	}

	return out, nil
}

func textFindings(name, text string, surface prose.Surface) []finding {
	out := []finding{}

	for _, f := range prose.JudgeText(text, surface) {
		out = append(out, finding{
			Node: name, Source: name + ":" + strconv.Itoa(f.Line), Language: string(surface),
			Rule: string(f.Rule), Message: f.Message, Match: f.Match,
		})
	}

	return out
}

func judgeSymbols(stdin io.Reader) ([]finding, error) {
	records, err := decode(stdin)
	if err != nil {
		return nil, err
	}

	out := []finding{}

	for _, r := range records {
		symbol := prose.Symbol{
			Name:     r.Name,
			Owner:    r.Owner,
			Callable: r.Kind == "function" || r.Kind == "method",
			Doc:      r.Doc,
		}

		for _, f := range prose.Judge(symbol) {
			out = append(out, finding{
				Node: r.Node, Source: r.Source, Language: r.Language,
				Rule: string(f.Rule), Message: f.Message, Match: f.Match,
			})
		}
	}

	return out, nil
}

func decode(stdin io.Reader) ([]record, error) {
	dec := json.NewDecoder(stdin)
	dec.DisallowUnknownFields()

	var records []record
	if err := dec.Decode(&records); err != nil {
		return nil, fmt.Errorf("read symbols: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("read symbols: data after the array")
	}

	return records, nil
}
