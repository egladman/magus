// Command judge-docs judges prose against the prose rules and writes a JSON
// array of findings on stdout.
//
// With no flag it reads a JSON array of symbol records on stdin and judges
// each one's doc, reporting in input order and then the order [prose.Judge]
// reports them. With -kind markdown it judges the Markdown files its
// arguments name, in argument order; -kind guide judges them as procedural
// pages, held to the guide rules as well; -kind skill judges them as skills an
// agent loads as written, and -kind skill-source as skill bodies
// internal/agent renders into a short and a full form, each finding at its
// source line. With -kind pull-request it reads a pull
// request on stdin, the title on the first line and the description after it,
// and with -kind reply a review comment or a reply in a thread on stdin. A
// finding from text names its file, or "pull-request" or "reply", and its line
// as source, `path:line`, the way a symbol's index position reads. Each
// finding carries its severity, error or advisory.
//
// -profile plain, the default, holds text to every rule; -profile
// collaborative leaves out this repository's house style. -only and -skip take
// comma-separated rule names, and a name the judge does not know is an error.
// -severity error writes only the findings a gate refuses. -thread-length N
// tells the reply rules how many replies the author already posted in the
// thread.
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
	"slices"
	"strconv"
	"strings"

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
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Match    string `json:"match"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run returns the process exit code: 0 once the findings are written, 1 when
// the flags or the input cannot be read. A finding is not a failure: the
// caller decides what one costs.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("judge-docs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "",
		"judge `markdown`, guide, skill or skill-source files named as arguments, or a pull-request or reply on stdin")
	profile := fs.String("profile", string(prose.ProfilePlain), "hold text to the `plain` or collaborative rules")
	severity := fs.String("severity", "all", "write `all` findings, or error findings alone")
	only := fs.String("only", "", "judge by these comma-separated `rules` alone")
	skip := fs.String("skip", "", "leave these comma-separated `rules` out")
	threadLength := fs.Int("thread-length", 0, "the `count` of replies the author already posted in the thread")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	fail := func(err error) int {
		fmt.Fprintf(stderr, "judge-docs: %v\n", err)

		return 1
	}

	if *severity != "all" && *severity != string(prose.SeverityError) {
		return fail(fmt.Errorf("unknown severity %q: want all or error", *severity))
	}

	opts, err := options(*profile, *only, *skip, *threadLength)
	if err != nil {
		return fail(err)
	}

	out, err := judge(prose.Kind(*kind), fs.Args(), stdin, opts)
	if err != nil {
		return fail(err)
	}

	if *severity == string(prose.SeverityError) {
		out = slices.DeleteFunc(out, func(f finding) bool { return f.Severity != string(prose.SeverityError) })
	}

	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		return fail(fmt.Errorf("write findings: %w", err))
	}

	return 0
}

// options reads the rule selection flags. A rule name the judge does not know
// is an error, not a selection that matches nothing.
func options(profile, only, skip string, threadLength int) ([]prose.Option, error) {
	switch prose.Profile(profile) {
	case prose.ProfilePlain, prose.ProfileCollaborative:
	default:
		return nil, fmt.Errorf("unknown profile %q: want plain or collaborative", profile)
	}

	onlyRules, err := ruleList(only)
	if err != nil {
		return nil, err
	}

	skipRules, err := ruleList(skip)
	if err != nil {
		return nil, err
	}

	return []prose.Option{
		prose.WithProfile(prose.Profile(profile)), prose.WithOnly(onlyRules...), prose.WithSkip(skipRules...),
		prose.WithThreadLength(threadLength),
	}, nil
}

func ruleList(list string) ([]prose.Rule, error) {
	if list == "" {
		return nil, nil
	}

	known := prose.Rules()

	var out []prose.Rule

	for name := range strings.SplitSeq(list, ",") {
		r := prose.Rule(strings.TrimSpace(name))
		if !slices.Contains(known, r) {
			return nil, fmt.Errorf("unknown rule %q", r)
		}

		out = append(out, r)
	}

	return out, nil
}

func judge(kind prose.Kind, paths []string, stdin io.Reader, opts []prose.Option) ([]finding, error) {
	switch kind {
	case "":
		if len(paths) > 0 {
			return nil, errors.New("symbols are read from stdin; a path needs -kind markdown")
		}

		return judgeSymbols(stdin, opts)
	case prose.KindMarkdown, prose.KindGuide, prose.KindSkill, prose.KindSkillSource:
		return judgeFiles(paths, kind, opts)
	case prose.KindPullRequest, prose.KindReply:
		if len(paths) > 0 {
			return nil, fmt.Errorf("a %s is read from stdin, not from a path", kind)
		}

		text, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read the %s: %w", kind, err)
		}

		return textFindings(string(kind), string(text), kind, opts), nil
	default:
		return nil, fmt.Errorf("unknown kind %q: want markdown, guide, skill, skill-source, pull-request or reply", kind)
	}
}

func judgeFiles(paths []string, kind prose.Kind, opts []prose.Option) ([]finding, error) {
	out := []finding{}

	for _, path := range paths {
		text, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		out = append(out, textFindings(path, string(text), kind, opts)...)
	}

	return out, nil
}

func textFindings(name, text string, kind prose.Kind, opts []prose.Option) []finding {
	out := []finding{}

	for _, f := range prose.JudgeText(text, kind, opts...) {
		out = append(out, finding{
			Node: name, Source: name + ":" + strconv.Itoa(f.Line), Language: string(kind),
			Rule: string(f.Rule), Severity: string(f.Severity), Message: f.Message, Match: f.Match,
		})
	}

	return out
}

func judgeSymbols(stdin io.Reader, opts []prose.Option) ([]finding, error) {
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

		for _, f := range prose.Judge(symbol, opts...) {
			out = append(out, finding{
				Node: r.Node, Source: r.Source, Language: r.Language,
				Rule: string(f.Rule), Severity: string(f.Severity), Message: f.Message, Match: f.Match,
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
