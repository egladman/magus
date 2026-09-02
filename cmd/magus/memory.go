package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/hint"
	store "github.com/egladman/magus/internal/memory"
)

func memoryCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		memoryUsage()
		return nil
	}
	switch args[0] {
	case "ls":
		return memoryList(root, args[1:])
	case "get":
		return memoryGet(root, args[1:])
	case "put":
		return memoryPut(root, args[1:])
	case "delete":
		return memoryDelete(root, args[1:])
	case "verify":
		return memoryVerify(ctx, root, args[1:])
	case "list":
		// Renamed to ls in v0.4.0, for one spelling of "enumerate" across the whole
		// surface (`magus ls`, `magus run ls`). Named rather than left to the generic
		// unknown-subcommand error so the message says what to type instead.
		return usagef("magus memory: `list` is now `ls` (run `%s`)", hint.MemoryLs)
	default:
		return usagef("magus memory: unknown subcommand %q (want ls, get, put, delete, or verify)", args[0])
	}
}

func memoryUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus memory <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Manage the per-repository handoff journal outside the checkout. Entries are")
	fmt.Fprintln(os.Stderr, "visible to people and agents across sessions and worktrees; they are not")
	fmt.Fprintln(os.Stderr, "automatic model memory.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  ls       show entries and any repair warnings")
	fmt.Fprintln(os.Stderr, "  get      show one entry")
	fmt.Fprintln(os.Stderr, "  put      create a named entry, or update the fields you name on one")
	fmt.Fprintln(os.Stderr, "  delete   remove one entry")
	fmt.Fprintln(os.Stderr, "  verify   check malformed, stale, and broken-linked entries")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Use `magus memory <subcommand> -h` for flags. The same entries are available")
	fmt.Fprintln(os.Stderr, "through the "+hint.ToolMemory.String()+" MCP tool and the console.")
}

type memoryListOutput struct {
	Records []store.Record `json:"records" jsonl:"primary"`
	Issues  []store.Issue  `json:"issues"`
}

func memoryList(root string, args []string) error {
	_, err := cmdParse("memory ls", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory ls [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "List handoff-journal entries. Warnings identify stale entries without hiding them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	recs, issues, err := store.Inspect(root)
	if err != nil {
		return err
	}
	out := memoryListOutput{Records: recs, Issues: issues}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		names := make([]string, len(recs))
		for i, rec := range recs {
			names[i] = rec.Name
		}
		if err := emitNames(names); err != nil {
			return err
		}
		return memoryIssuesError(issues)
	}
	if opts.Format != outputText {
		if err := emitFormatted(opts, out); err != nil {
			return err
		}
		return memoryIssuesError(issues)
	}
	if len(recs) == 0 {
		fmt.Println("No handoff entries. Add an explicit decision or plan with `" + hint.MemoryPut.String() + "`.")
	} else {
		for _, rec := range recs {
			status := rec.Status
			if status == "" {
				status = "-"
			}
			fmt.Printf("%s  %s  %s  %s\n", rec.Name, rec.Type, status, time.Unix(rec.Updated, 0).UTC().Format(time.RFC3339))
		}
	}
	return printMemoryIssues(issues)
}

func memoryGet(root string, args []string) error {
	pos, err := cmdParse("memory get", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory get <name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Show one named handoff-journal entry.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("magus memory get: requires exactly one entry name")
	}
	rec, err := store.Get(root, pos[0])
	if err != nil {
		return fmt.Errorf("magus memory get %q: %w", pos[0], err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		return emitNames([]string{rec.Name})
	}
	if opts.Format != outputText {
		return emitFormatted(opts, rec)
	}
	printMemoryRecord(rec)
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ", ") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func memoryPut(root string, args []string) error {
	// The two repeatable flags stay hand-bound - the registry declares them
	// FlagCustom so they reach the man page, which never listed them - and the
	// rest bind from the registry.
	var refs, references stringList
	var pf *gen.MemoryPutFlags
	pos, err := cmdParse("memory put", args, func(fs *flag.FlagSet) {
		fs.Var(&refs, gen.FlagMemoryPutRef, "Entry ref in 'kind: target' form; repeat for multiple refs")
		fs.Var(&references, gen.FlagMemoryPutReference, "Name of another entry this one relates to; repeat as needed")
		pf = gen.BindMemoryPut(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory put <name> --type <pointer|decision|plan|elimination> --ref 'kind: target' [--ref ...] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Create a visible handoff entry. Use refs for things magus can re-open;")
			fmt.Fprintln(os.Stderr, "a pointer carries no prose, and every other type takes a short why in --body.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "On an entry that already exists this writes only the flags you pass; the rest")
			fmt.Fprintln(os.Stderr, "keep what is stored. An omitted flag therefore cannot clear a field, and a type")
			fmt.Fprintln(os.Stderr, "change is refused: delete the entry and create it again for either.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "An elimination records a hypothesis an investigation killed, and needs --excerpt.")
			fmt.Fprintln(os.Stderr, "An output ref resolves only from the checkout that minted it, so copy the evidence")
			fmt.Fprintln(os.Stderr, "in and the entry outlives the ref.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Examples:")
			fmt.Fprintln(os.Stderr, "  magus memory put installer-key --type decision --ref 'doc: docs/setup.md#verification' --body 'Keep one bootstrap key; rotation ships a compatibility release first.'")
			fmt.Fprintln(os.Stderr, "  magus memory put next-release --type plan --ref 'command: magus affected ci' --status active --body 'Run the release gate after docs regenerate.'")
			fmt.Fprintln(os.Stderr, "  magus memory put cache-key-drift --type elimination --ref 'output: out1a2b3c' --body 'Not the cache key: both runs hashed the same inputs.' --excerpt 'key inputs identical, 0 differing lines'")
			fmt.Fprintln(os.Stderr, "  magus memory put next-release --amend --status done")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("magus memory put: requires exactly one entry name")
	}
	parsed, err := store.ParseRefs(strings.Join(refs, "\n"))
	if err != nil {
		return err
	}
	// No mask: the flags the caller typed ARE the mask, which is the reading AIP-134 gives
	// an absent one. --amend is the CLI's spelling of allow_missing=false, because a
	// negated boolean flag does not read as a sentence.
	rec, err := store.Update(root, store.Record{
		Name: pos[0], Type: store.RecordType(pf.Type), Status: pf.Status, Body: pf.Body,
		Excerpt: pf.Excerpt, Refs: parsed, References: references,
	}, store.UpdateOptions{AllowMissing: !pf.Amend})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("magus memory put: the journal holds no entry named %q; drop --amend to create it, or see what is there with `%s`", pos[0], hint.MemoryLs)
		}
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		return emitNames([]string{rec.Name})
	}
	if opts.Format != outputText {
		return emitFormatted(opts, rec)
	}
	fmt.Printf("Saved handoff entry %q. Verify it with `%s`.\n", rec.Name, hint.MemoryVerify)
	return nil
}

func memoryDelete(root string, args []string) error {
	pos, err := cmdParse("memory delete", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory delete <name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Remove one handoff-journal entry. This is strict: a missing name is likely a typo.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("magus memory delete: requires exactly one entry name")
	}
	if err := store.Delete(root, pos[0], false); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	out := struct {
		Deleted string `json:"deleted"`
	}{Deleted: pos[0]}
	if opts.Format == outputName {
		return emitNames([]string{pos[0]})
	}
	if opts.Format != outputText {
		return emitFormatted(opts, out)
	}
	fmt.Printf("Deleted handoff entry %q.\n", pos[0])
	return nil
}

func memoryVerify(ctx context.Context, root string, args []string) error {
	_, err := cmdParse("memory verify", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory verify [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Check every entry for malformed frontmatter, invalid shape, stale status,")
			fmt.Fprintln(os.Stderr, "references to deleted entries, and evidence refs that no longer resolve.")
			fmt.Fprintln(os.Stderr, "Errors exit non-zero; stale entries and decayed evidence are warnings.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	// Resolving evidence needs the cache, and the cache needs an open workspace. ls, get
	// and put stay workspace-free, so the journal is readable while the magusfile is the
	// thing being repaired.
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	report, err := store.Verify(root, func(ref store.Ref) error {
		if ref.Kind != store.RefKindOutput {
			return nil
		}
		_, err := m.OutputDescriptorByRef(ref.Target)
		return err
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// A verification has no name of its own, so the one-token-per-line identity is what
	// the reader would act on next: the entry each issue is about (its file, for an issue
	// about the store rather than a record). A clean journal prints nothing.
	if opts.Format == outputName {
		if err := emitNames(memoryIssueSubjects(report.Issues)); err != nil {
			return err
		}
		return memoryIssuesError(report.Issues)
	}
	if opts.Format != outputText {
		if err := emitFormatted(opts, report); err != nil {
			return err
		}
		return memoryIssuesError(report.Issues)
	}
	if len(report.Issues) == 0 {
		fmt.Printf("[pass] handoff journal: %d entries verified\n", report.Records)
		return nil
	}
	return printMemoryIssues(report.Issues)
}

// memoryIssueSubjects names what each issue is about, for `-o name`.
func memoryIssueSubjects(issues []store.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, i := range issues {
		if i.Record != "" {
			out = append(out, i.Record)
			continue
		}
		out = append(out, i.Path)
	}
	return out
}

func printMemoryRecord(rec store.Record) {
	fmt.Printf("%s (%s)\n", rec.Name, rec.Type)
	if rec.Status != "" {
		fmt.Printf("status: %s\n", rec.Status)
	}
	if rec.Body != "" {
		fmt.Printf("why: %s\n", rec.Body)
	}
	// Indented as a block so a reader tells captured tool output from the record's own
	// fields at a glance.
	if rec.Excerpt != "" {
		fmt.Println("evidence:")
		for _, line := range strings.Split(rec.Excerpt, "\n") {
			fmt.Printf("  %s\n", line)
		}
	}
	fmt.Println("refs:")
	for _, ref := range rec.Refs {
		fmt.Printf("  %s: %s\n", ref.Kind, ref.Target)
	}
	if len(rec.References) != 0 {
		fmt.Printf("related entries: %s\n", strings.Join(rec.References, ", "))
	}
	fmt.Printf("updated: %s\n", time.Unix(rec.Updated, 0).UTC().Format(time.RFC3339))
}

func printMemoryIssues(issues []store.Issue) error {
	for _, issue := range issues {
		glyph := "[warn]"
		if issue.Severity == "error" {
			glyph = "[fail]"
		}
		fmt.Printf("%s %s: %s\n  %s\n", glyph, issue.Path, issue.Message, issue.Hint)
	}
	return memoryIssuesError(issues)
}

func memoryIssuesError(issues []store.Issue) error {
	var failures int
	for _, issue := range issues {
		if issue.Severity == "error" {
			failures++
		}
	}
	if failures != 0 {
		return fmt.Errorf("magus memory verify: %d invalid handoff entr%s", failures, pluralSuffix(failures, "y", "ies"))
	}
	return nil
}

func pluralSuffix(n int, singular, plural string) string {
	if n == 1 {
		return singular
	}
	return plural
}
