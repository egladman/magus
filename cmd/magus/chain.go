package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
)

// Chained verbs: what to do with what a target produced.
//
//	magus run build --then outputs
//	magus run build --then outputs export --path ./dist-copy
//	magus run build --then file dist/magus contents
//	magus run build --then file dist/magus export --path ./out/magus
//
// The point Dagger gets right is that a result is not a string the CLI prints, it
// is an object the CLI knows verbs for. The point magus improves on is that the
// object needs no return statement: a target already declared ctx.outputs(...) for
// the cache, so the artifact set is known without asking the author for anything.
//
// The verb set is deliberately tiny and mirrors the returnable types: `outputs` is
// the Directory, `file` is the File. Nothing here reaches for Dagger's
// Container/Service - magus is not a container runtime, and copying that surface
// would be cargo-culting the architecture rather than the idea.

// chainUsage is the shared usage text; every misuse in this file prints it, so a
// wrong verb teaches the whole grammar rather than only naming what failed.
func chainUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus run <target> [projects...] --then <verb> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Verbs, applied to the files the target declared as outputs:")
	fmt.Fprintln(os.Stderr, "  outputs                          list every artifact produced")
	fmt.Fprintln(os.Stderr, "  outputs export --path <dir>      copy them all into <dir>")
	fmt.Fprintln(os.Stderr, "  file <path>                      print one artifact's absolute path")
	fmt.Fprintln(os.Stderr, "  file <path> contents             write its bytes to stdout")
	fmt.Fprintln(os.Stderr, "  file <path> export --path <dst>  copy it to <dst>")
	fmt.Fprintln(os.Stderr, "  value                            print what the target returned")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "<path> is workspace-relative, as printed by `outputs`.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Global flags go BEFORE --then, since everything after it is the verb's:")
	fmt.Fprintln(os.Stderr, "  magus run build -o json --then outputs")
}

// runChain applies the post---then verbs to the artifacts target produced.
func runChain(ctx context.Context, m *magus.Magus, opts OutputOptions, target string, targets []types.Target, argv []string, returns map[string]any) error {
	if len(argv) == 0 {
		chainUsage()
		return usagef("magus run: --then needs a verb (want outputs, file, or value)")
	}
	if argv[0] == "value" {
		return chainValue(m, opts, targets, returns, argv[1:])
	}
	artifacts, err := m.ResolveTargetOutputs(ctx, m.ResolveProjects(targets), target)
	if err != nil {
		return err
	}

	switch argv[0] {
	case "outputs":
		return chainOutputs(m, opts, artifacts, argv[1:])
	case "file":
		return chainFile(m, opts, artifacts, argv[1:])
	case "-h", "--help", "help":
		chainUsage()
		return nil
	default:
		chainUsage()
		return usagef("magus run: unknown --then verb %q (want outputs, file, or value)", argv[0])
	}
}

// chainOutputs implements `--then outputs [export --path <dir>]`.
func chainOutputs(m *magus.Magus, opts OutputOptions, artifacts []magus.TargetArtifact, argv []string) error {
	if len(argv) > 0 {
		if argv[0] != "export" {
			chainUsage()
			return usagef("magus run: unknown verb %q after outputs (want export)", argv[0])
		}
		dir, err := chainPathFlag(argv[1:], "outputs export")
		if err != nil {
			return err
		}
		for _, a := range artifacts {
			dst := filepath.Join(dir, filepath.FromSlash(a.Path))
			if err := copyArtifact(filepath.Join(m.Root(), filepath.FromSlash(a.Path)), dst); err != nil {
				return err
			}
			fmt.Println(dst)
		}
		return nil
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		out := make([]runArtifact, 0, len(artifacts))
		roles := artifactRoles(m, artifacts)
		for _, a := range artifacts {
			out = append(out, runArtifact{Path: a.Path, Glob: a.Glob, Role: roles[a.Path]})
		}
		return emitFormatted(opts, out)
	}
	for _, a := range artifacts {
		fmt.Println(a.Path)
	}
	return nil
}

// chainFile implements `--then file <path> [contents | export --path <dst>]`.
func chainFile(m *magus.Magus, opts OutputOptions, artifacts []magus.TargetArtifact, argv []string) error {
	if len(argv) == 0 {
		chainUsage()
		return usagef("magus run: `file` needs a path")
	}
	want := filepath.ToSlash(argv[0])
	var match *magus.TargetArtifact
	for i, a := range artifacts {
		if a.Path == want {
			match = &artifacts[i]
			break
		}
	}
	if match == nil {
		// Naming what the target DID produce turns a typo into a one-line fix; the
		// alternative is "no such file", which is also true of a path that was never
		// an artifact in the first place.
		var have []string
		for _, a := range artifacts {
			have = append(have, a.Path)
		}
		if len(have) == 0 {
			return usagef("magus run: the target produced no artifacts, so %q is not one of them", want)
		}
		return usagef("magus run: %q is not an artifact of this target (produced: %s)", want, strings.Join(have, ", "))
	}
	abs := filepath.Join(m.Root(), filepath.FromSlash(match.Path))

	verb := ""
	if len(argv) > 1 {
		verb = argv[1]
	}
	switch verb {
	case "":
		if opts.Format == outputJSON || opts.Format == outputYAML || opts.Format == outputTemplate {
			return emitFormatted(opts, runArtifact{
				Path: match.Path, Glob: match.Glob, Role: artifactRoles(m, []magus.TargetArtifact{*match})[match.Path],
			})
		}
		fmt.Println(abs)
		return nil
	case "contents":
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(os.Stdout, f)
		return err
	case "export":
		dst, err := chainPathFlag(argv[2:], "file export")
		if err != nil {
			return err
		}
		if err := copyArtifact(abs, dst); err != nil {
			return err
		}
		fmt.Println(dst)
		return nil
	}
	chainUsage()
	return usagef("magus run: unknown verb %q after file (want contents or export)", verb)
}

// chainPathFlag reads the required --path value for an export verb.
func chainPathFlag(argv []string, verb string) (string, error) {
	for i, a := range argv {
		if a == "--path" || a == "-path" {
			if i+1 >= len(argv) {
				return "", usagef("magus run: %s: --path needs a value", verb)
			}
			return argv[i+1], nil
		}
		if v, ok := strings.CutPrefix(a, "--path="); ok {
			return v, nil
		}
	}
	chainUsage()
	return "", usagef("magus run: %s requires --path <dir>", verb)
}

// artifactRoles classifies artifacts so a structured result says whether each file
// is generated, which is the question that follows "where is it".
func artifactRoles(m *magus.Magus, artifacts []magus.TargetArtifact) map[string]string {
	paths := make([]string, len(artifacts))
	for i, a := range artifacts {
		paths[i] = a.Path
	}
	roles := m.DescribeFiles(paths)
	out := make(map[string]string, len(roles.Files))
	for _, f := range roles.Files {
		out[f.Path] = f.Role
	}
	return out
}

// copyArtifact copies src to dst, creating parent directories. It refuses to
// overwrite nothing silently: a missing source is a real error, since the artifact
// list came from the working tree moments earlier.
func copyArtifact(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// chainValue implements `--then value`: print what the target returned.
//
// This is the bare-scalar form, for substitution straight into another command:
//
//	VER=$(magus run describe --then value)
//
// -o json gives the same value with its project, which is what a multi-project run
// needs; the text form deliberately prints the value alone with no label to strip,
// the same contract `magus where` keeps.
func chainValue(m *magus.Magus, opts OutputOptions, targets []types.Target, returns map[string]any, argv []string) error {
	if len(argv) > 0 {
		chainUsage()
		return usagef("magus run: `value` takes no further verbs, got %q", argv[0])
	}
	projects := m.ResolveProjects(targets)

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		out := make([]runProject, 0, len(projects))
		for _, p := range projects {
			out = append(out, runProject{Path: p.Path, Value: returns[p.Path]})
		}
		return emitFormatted(opts, out)
	}

	// The text form exists to be substituted - VER=$(... --then value) - so it must
	// yield ONE project's value. A fan-out that printed every project's value
	// unlabeled would concatenate them into a single string with nothing to say it
	// happened, which is worse than refusing. -o json carries the project alongside
	// each value and is the right answer for more than one.
	var withValue []string
	for _, p := range projects {
		if _, ok := returns[p.Path]; ok {
			withValue = append(withValue, p.Path)
		}
	}
	switch len(withValue) {
	case 0:
		// Not an error to RUN - `> void` is the default and nearly every target is
		// one - but asking for a value it never produced is. An empty line would read
		// as "the value was the empty string".
		return usagef("magus run: this target returned nothing (it is `> void`), so there is no value to print")
	case 1:
		v := returns[withValue[0]]
		if items, ok := v.([]string); ok {
			for _, item := range items {
				fmt.Println(item)
			}
			return nil
		}
		fmt.Println(v)
		return nil
	default:
		return usagef("magus run: %d projects returned a value (%s); the text form is for substituting ONE, so narrow the scope or use -o json",
			len(withValue), strings.Join(withValue, ", "))
	}
}
