package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
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
	fmt.Fprintln(os.Stderr, "Usage: magus run <target> [project...] --then <verb> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Verbs, applied to the files the target declared as outputs:")
	fmt.Fprintln(os.Stderr, "  outputs                          list every artifact produced")
	fmt.Fprintln(os.Stderr, "  outputs export --path <dir>      copy them all into <dir>")
	fmt.Fprintln(os.Stderr, "  file <path>                      print one artifact's absolute path")
	fmt.Fprintln(os.Stderr, "  file <path> contents             write its bytes to stdout")
	fmt.Fprintln(os.Stderr, "  file <path> export --path <dst>  copy it to <dst>")
	fmt.Fprintln(os.Stderr, "  file <path> hash                 print its content hash (the store's blob key)")
	fmt.Fprintln(os.Stderr, "  file <path> history              every cached version: when its bytes changed")
	fmt.Fprintln(os.Stderr, "  file <path> diff                 compare it against the last cached version")
	fmt.Fprintln(os.Stderr, "  value                            print what the target returned")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "<path> is workspace-relative, as printed by `outputs`.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "`diff` renders nothing itself: it materializes the cached side and runs your")
	fmt.Fprintln(os.Stderr, "difftool, from $MAGUS_DIFFTOOL, else $DIFFTOOL, else `git diff --no-index`.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Global flags go BEFORE --then, since everything after it is the verb's:")
	fmt.Fprintln(os.Stderr, "  magus run build -o json --then outputs")
}

// chainPlan is the parsed verb grammar: what to do with the result, decided BEFORE
// the target runs.
//
// It exists because the verbs used to be interpreted after the run, so
// `magus run build --then bogusverb` built the whole project and only then exited 2
// on a typo it could have rejected in the first millisecond. Everything here is
// checkable without a result; the one thing that is not - whether `file <path>`
// names an artifact the target actually produced - stays in chainFile, where the
// artifact list exists.
type chainPlan struct {
	verb   string // outputs | file | value
	path   string // file <path>
	action string // "" | contents | export | hash | history | diff
	dst    string // export --path <dst>
}

// prepareChain parses the post---then verbs. proceed is false when the verbs asked
// for help, which is answered without running anything.
func prepareChain(argv []string) (plan chainPlan, proceed bool, err error) {
	if len(argv) == 0 {
		chainUsage()
		return plan, false, usagef("magus run: --then needs a verb (want outputs, file, or value)")
	}
	rest := argv[1:]
	plan.verb = argv[0]
	switch plan.verb {
	case "-h", "--help", "help":
		chainUsage()
		return plan, false, nil
	case "value":
		if len(rest) > 0 {
			chainUsage()
			return plan, false, usagef("magus run: `value` takes no further verbs, got %q", rest[0])
		}
	case "outputs":
		if len(rest) > 0 {
			if rest[0] != "export" {
				chainUsage()
				return plan, false, usagef("magus run: unknown verb %q after outputs (want export)", rest[0])
			}
			plan.action = "export"
			if plan.dst, err = chainPathFlag(rest[1:], "outputs export"); err != nil {
				return plan, false, err
			}
		}
	case "file":
		if len(rest) == 0 {
			chainUsage()
			return plan, false, usagef("magus run: `file` needs a path")
		}
		plan.path = filepath.ToSlash(rest[0])
		if len(rest) > 1 {
			plan.action = rest[1]
		}
		switch plan.action {
		case "", "contents", "history", "diff", "hash":
			// These take nothing further, so anything trailing is a mistake worth
			// naming. The commonest is a global flag put after the verb
			// (`--then file x history -o json`), which the grammar sends to the verb
			// and which was previously accepted and silently dropped - the same
			// accepted-and-ignored failure the --then separator exists to avoid.
			if len(rest) > 2 {
				chainUsage()
				return plan, false, usagef("magus run: `file <path> %s` takes no further arguments, got %q "+
					"(global flags go BEFORE --then)", plan.action, rest[2])
			}
		case "export":
			if plan.dst, err = chainPathFlag(rest[2:], "file export"); err != nil {
				return plan, false, err
			}
		default:
			chainUsage()
			return plan, false, usagef("magus run: unknown verb %q after file (want contents, export, hash, history, or diff)", plan.action)
		}
	default:
		chainUsage()
		return plan, false, usagef("magus run: unknown --then verb %q (want outputs, file, or value)", plan.verb)
	}
	return plan, true, nil
}

// runChain applies the parsed verbs to what target produced.
func runChain(ctx context.Context, m *magus.Magus, opts OutputOptions, target string, selection []types.Target, plan chainPlan, returns types.Returns) error {
	if plan.verb == "value" {
		return chainValue(m, opts, selection, returns)
	}
	artifacts, err := m.ResolveTargetOutputs(ctx, m.ResolveProjects(selection), target)
	if err != nil {
		return err
	}
	if plan.verb == "outputs" {
		return chainOutputs(ctx, m, opts, artifacts, plan)
	}
	return chainFile(ctx, m, opts, artifacts, plan)
}

// chainOutputs implements `--then outputs [export --path <dir>]`.
func chainOutputs(ctx context.Context, m *magus.Magus, opts OutputOptions, artifacts []magus.TargetArtifact, plan chainPlan) error {
	if plan.action == "export" {
		dir := plan.dst
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
		roles, err := artifactRoles(ctx, m, artifacts)
		if err != nil {
			return err
		}
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

// chainFile implements `--then file <path> [contents | export --path <dst> | history | diff]`.
//
// The action was validated by prepareChain, so the switch below trusts it.
func chainFile(ctx context.Context, m *magus.Magus, opts OutputOptions, artifacts []magus.TargetArtifact, plan chainPlan) error {
	want := plan.path
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

	switch plan.action {
	case "hash":
		// The content hash of what is on disk NOW, which is what makes a comparison
		// scriptable: it is the same sha256 the store keys blobs by, so it can be set
		// against any row of `history` without materializing anything.
		body, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", hex.EncodeToString(sha256Of(body)))
		return nil
	case "history":
		return chainFileHistory(ctx, m, opts, match.ProjectPath, match.Path)
	case "diff":
		return chainFileDiff(ctx, m, match.ProjectPath, match.Path, abs)
	case "contents":
		f, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		_, err = io.Copy(os.Stdout, f)
		return err
	case "export":
		if err := copyArtifact(abs, plan.dst); err != nil {
			return err
		}
		fmt.Println(plan.dst)
		return nil
	}
	if opts.Format == outputJSON || opts.Format == outputYAML || opts.Format == outputTemplate {
		roles, err := artifactRoles(ctx, m, []magus.TargetArtifact{*match})
		if err != nil {
			return err
		}
		return emitFormatted(opts, runArtifact{
			Path: match.Path, Glob: match.Glob, Role: roles[match.Path],
		})
	}
	fmt.Println(abs)
	return nil
}

// chainPathFlag reads the required --path value for an export verb, and rejects
// everything else.
//
// It is hand-rolled rather than a FlagSet because the chain grammar is
// positional and `--then` hands it a raw tail. That makes it the one argument
// reader in the CLI that does not inherit Go's flag semantics for free, so it
// has to reproduce them deliberately. Two ways it previously did not, both found
// by writing the grammar down as a test:
//
//   - It accepted `--path v`, `-path v` and `--path=v` but NOT `-path=v`. Three
//     spellings out of four is worse than one, because the missing one fails
//     looking like a bad value rather than a bad syntax.
//   - It scanned for --path anywhere and IGNORED every other argument, so
//     `export --path out -o json` silently dropped the `-o json` and
//     `export stray --path out` silently dropped `stray`. That is precisely the
//     accepted-and-ignored failure the `--then` separator exists to prevent, and
//     the sibling verbs (history, diff, contents) already rejected it.
//
// Repeat flags take the LAST value, matching Go's flag package rather than
// inventing a stricter rule for one command.
func chainPathFlag(argv []string, verb string) (string, error) {
	dst, seen := "", false
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case isFlagNamed(a, "path"):
			if i+1 >= len(argv) {
				return "", usagef("magus run: %s: --path needs a value", verb)
			}
			dst, seen = argv[i+1], true
			i++
		case flagValueOf(a, "path") != "":
			dst, seen = flagValueOf(a, "path"), true
		default:
			chainUsage()
			return "", usagef("magus run: %s: unexpected argument %q (want only --path <dir>; "+
				"global flags go BEFORE --then)", verb, a)
		}
	}
	if !seen {
		chainUsage()
		return "", usagef("magus run: %s requires --path <dir>", verb)
	}
	return dst, nil
}

// artifactRoles classifies artifacts so a structured result says whether each file
// is generated, which is the question that follows "where is it".
func artifactRoles(ctx context.Context, m *magus.Magus, artifacts []magus.TargetArtifact) (map[string]string, error) {
	paths := make([]string, len(artifacts))
	for i, a := range artifacts {
		paths[i] = a.Path
	}
	roles, err := m.DescribeFiles(ctx, paths)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(roles))
	for _, f := range roles {
		out[f.Path] = f.Role
	}
	return out, nil
}

// copyArtifact copies src to dst, creating parent directories.
//
// It writes a temporary file beside dst and renames it into place, which is not
// ceremony - it is what makes three separate ways of destroying data impossible:
//
//   - dst == src. Opening dst with O_TRUNC truncated the source before the read,
//     so `--then outputs export --path .` from the workspace root emptied every
//     artifact it claimed to copy AND exited 0.
//   - A failed read. Truncating first meant an io.Copy error left a half-written
//     file where a valid artifact had been, with the original already gone.
//   - A symlink planted at dst (by an earlier run, or a shared CI export dir).
//     O_CREATE follows it and writes through to wherever it points; rename
//     replaces the link itself.
//
// The mode is set on the temp file rather than left to O_CREATE, because O_CREATE
// only applies its mode when it actually creates: exporting over an existing file
// kept the OLD permissions, so an exported binary quietly lost +x.
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

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".magus-export-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Any failure past this point must not leave the temp file behind; the rename
	// on the success path clears it, so this only fires when something went wrong.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
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
func chainValue(m *magus.Magus, opts OutputOptions, selection []types.Target, returns types.Returns) error {
	projects := m.ResolveProjects(selection)

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

// artifactVersion is one row of `--then file <p> history`, shaped for -o json.
type artifactVersion struct {
	Blob    string `json:"blob"    yaml:"blob"`
	Short   string `json:"short"   yaml:"short"`
	Size    int64  `json:"size"    yaml:"size"`
	Target  string `json:"target"  yaml:"target"`
	Created string `json:"created" yaml:"created"`
	Entry   string `json:"entry"   yaml:"entry"`
}

// chainFileHistory implements `--then file <path> history`: every cached version of
// that artifact, newest first.
//
// The store is content-addressed, so this answers "when did this file's content last
// actually change" without consulting the VCS - which matters precisely for the files
// the VCS is a poor witness for. A generated artifact's git history tells you when
// someone committed a regeneration; this tells you when the bytes changed.
func chainFileHistory(ctx context.Context, m *magus.Magus, opts OutputOptions, projectPath, wsPath string) error {
	versions, err := m.ListArtifacts(ctx, projectPath, wsPath)
	if err != nil {
		return err
	}
	out := make([]artifactVersion, 0, len(versions))
	for _, v := range versions {
		out = append(out, artifactVersion{
			Blob: v.Output.Blob, Short: v.ShortBlob(), Size: v.Output.Size, Target: v.Target,
			Created: v.CreatedAt.UTC().Format(time.RFC3339), Entry: v.EntryHash,
		})
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	}
	if len(out) == 0 {
		// Not an error: an artifact built once and never rebuilt has no history to
		// show, and neither does one whose versions have been evicted.
		fmt.Printf("%s: no cached versions\n", wsPath)
		return nil
	}
	for _, v := range out {
		fmt.Printf("%s  %-12s  %8d  %s\n", v.Created, v.Short, v.Size, v.Target)
	}
	return nil
}

// chainFileDiff implements `--then file <path> diff`: compare the artifact on disk
// against the most recent DIFFERENT cached version.
//
// magus does not render the diff. It materializes the cached side into a temp file
// and hands both paths to the tool the user already uses, resolved from
// MAGUS_DIFFTOOL, then DIFFTOOL, then `git diff --no-index`. The emit-never-render
// rule that keeps a graph renderer out of magus applies here for the same reason: a
// built-in differ would be a worse version of a tool the user has already chosen.
func chainFileDiff(ctx context.Context, m *magus.Magus, projectPath, wsPath, workingCopy string) error {
	versions, err := m.ListArtifacts(ctx, projectPath, wsPath)
	if err != nil {
		return err
	}
	// Skip any version whose bytes match what is on disk: diffing a file against
	// itself prints nothing and reads as "no cached history", which is a different
	// and wrong conclusion.
	current, err := os.ReadFile(workingCopy)
	if err != nil {
		return err
	}
	currentBlob := hex.EncodeToString(sha256Of(current))
	var prev *cache.ArtifactVersion
	for i := range versions {
		// A symlink record stores a target, not content, so there is nothing to
		// compare and materializing it would only produce a dangling link in the
		// temp dir. Report the recorded target instead of handing a differ a file
		// that is not there.
		if versions[i].Output.Symlink != "" {
			fmt.Fprintf(os.Stderr, "magus run: %s is a symlink to %q; diff compares content, not link targets\n",
				wsPath, versions[i].Output.Symlink)
			return nil
		}
		if versions[i].Output.Blob != currentBlob {
			prev = &versions[i]
			break
		}
	}
	if prev == nil {
		fmt.Fprintf(os.Stderr, "magus run: %s matches every cached version; nothing to diff\n", wsPath)
		return nil
	}

	dir, err := os.MkdirTemp("", "magus-diff-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// Named after the artifact, not "old", so the difftool's own header and the
	// editor tab both say which file this is.
	staged := filepath.Join(dir, filepath.Base(wsPath))
	if err := m.GetArtifact(ctx, *prev, staged); err != nil {
		return err
	}

	name, args := difftool()
	args = append(args, staged, workingCopy)
	fmt.Fprintf(os.Stderr, "magus run: %s %s (cached %s) vs the working tree\n",
		wsPath, prev.ShortBlob(), prev.CreatedAt.UTC().Format(time.RFC3339))
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		// A differ exits non-zero to mean "they differ", which is the normal answer
		// here, not a failure. Only a launch failure is worth reporting.
		if errors.As(err, &exit) {
			return nil
		}
		return fmt.Errorf("magus run: could not run %q (set MAGUS_DIFFTOOL to a command that takes two paths): %w", name, err)
	}
	return nil
}

// sha256Of is the one place a content hash is computed, so `hash` and the
// same-content check in diff cannot disagree about what identity means.
func sha256Of(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

// difftool resolves the command to compare two paths with.
//
// MAGUS_DIFFTOOL wins so a magus-specific choice is possible; DIFFTOOL is the
// conventional variable others already set; `git diff --no-index` is the fallback
// because git is already a hard dependency of a magus workspace, so it is the one
// differ guaranteed present. The value is split on spaces so `delta --side-by-side`
// works without a shell.
func difftool() (string, []string) {
	for _, env := range []string{"MAGUS_DIFFTOOL", "DIFFTOOL"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			parts := strings.Fields(v)
			return parts[0], parts[1:]
		}
	}
	return "git", []string{"diff", "--no-index"}
}
