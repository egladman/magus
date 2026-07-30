package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	store "github.com/egladman/magus/internal/memory"
)

func memoryCmd(_ context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		memoryUsage()
		return nil
	}
	switch args[0] {
	case "list":
		return memoryList(root, args[1:])
	case "get":
		return memoryGet(root, args[1:])
	case "put":
		return memoryPut(root, args[1:])
	case "delete":
		return memoryDelete(root, args[1:])
	case "verify":
		return memoryVerify(root, args[1:])
	default:
		return usagef("magus memory: unknown subcommand %q (want list, get, put, delete, or verify)", args[0])
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
	fmt.Fprintln(os.Stderr, "  list     show entries and any repair warnings")
	fmt.Fprintln(os.Stderr, "  get      show one entry")
	fmt.Fprintln(os.Stderr, "  put      create or replace a named entry")
	fmt.Fprintln(os.Stderr, "  delete   remove one entry")
	fmt.Fprintln(os.Stderr, "  verify   check malformed, stale, and broken-linked entries")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Use `magus memory <subcommand> -h` for flags. The same entries are available")
	fmt.Fprintln(os.Stderr, "through the magus_memory MCP tool and the console.")
}

type memoryListOutput struct {
	Records []store.Record `json:"records"`
	Issues  []store.Issue  `json:"issues"`
}

func memoryList(root string, args []string) error {
	_, err := cmdParse("memory list", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory list [flags]")
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
	if opts.Format != outputText {
		if err := emitFormatted(opts, out); err != nil {
			return err
		}
		return memoryIssuesError(issues)
	}
	if len(recs) == 0 {
		fmt.Println("No handoff entries. Add an explicit decision or plan with `magus memory put`.")
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
	var refs, references stringList
	var typ, status, body string
	pos, err := cmdParse("memory put", args, func(fs *flag.FlagSet) {
		fs.Var(&refs, "ref", "Entry ref in 'kind: target' form; repeat for multiple refs")
		fs.Var(&references, "reference", "Name of another entry this one relates to; repeat as needed")
		fs.StringVar(&typ, "type", "", "Entry type: pointer, decision, or plan")
		fs.StringVar(&status, "status", "", "Lifecycle label, e.g. accepted, active, done, stale")
		fs.StringVar(&body, "body", "", "Short why/caption (decision and plan only)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory put <name> --type <pointer|decision|plan> --ref 'kind: target' [--ref ...] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Create or replace a visible handoff entry. Use refs for things magus can re-open;")
			fmt.Fprintln(os.Stderr, "only a decision or plan may carry a short why in --body.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Examples:")
			fmt.Fprintln(os.Stderr, "  magus memory put installer-key --type decision --ref 'doc: docs/guides/download.md#verification' --body 'Keep one bootstrap key; rotation ships a compatibility release first.'")
			fmt.Fprintln(os.Stderr, "  magus memory put next-release --type plan --ref 'command: magus affected ci' --status active --body 'Run the release gate after docs regenerate.'")
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
	rec, err := store.Put(root, store.Record{
		Name: pos[0], Type: store.RecordType(typ), Status: status, Body: body,
		Refs: parsed, References: references,
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitFormatted(opts, rec)
	}
	fmt.Printf("Saved handoff entry %q. Verify it with `magus memory verify`.\n", rec.Name)
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
	if opts.Format != outputText {
		return emitFormatted(opts, out)
	}
	fmt.Printf("Deleted handoff entry %q.\n", pos[0])
	return nil
}

func memoryVerify(root string, args []string) error {
	_, err := cmdParse("memory verify", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus memory verify [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Check every entry for malformed frontmatter, invalid shape, stale status, and")
			fmt.Fprintln(os.Stderr, "references to deleted entries. Errors exit non-zero; stale entries are warnings.")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	report, err := store.Verify(root)
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
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

func printMemoryRecord(rec store.Record) {
	fmt.Printf("%s (%s)\n", rec.Name, rec.Type)
	if rec.Status != "" {
		fmt.Printf("status: %s\n", rec.Status)
	}
	if rec.Body != "" {
		fmt.Printf("why: %s\n", rec.Body)
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
