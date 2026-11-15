package knowledge

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

// RefsEnv is what rendering a refs answer borrows from the command that owns
// stdout, --tee and the stale-index advice.
type RefsEnv struct {
	// Root is the resolved workspace root source lines are read under.
	Root string
	// Stdout takes the answer; Notices takes the error-level lines that follow it.
	Stdout, Notices io.Writer
	// Classify returns the classifier generated files are told apart by, or nil
	// when no workspace loaded.
	Classify func() textindex.ClassifyFunc
	// Format and Names write the structured and the -o name forms.
	Format func(opts output.Options, v any) error
	Names  func(w io.Writer, names []string) error
	// Verdict, Staleness, StaleNotice, Unverified and IndexCause are the advice an
	// answer carries when its index is absent, behind or disagrees with the tree.
	Verdict     func(w io.Writer, ans types.KnowledgeAnswer)
	Staleness   func(w io.Writer, ans types.KnowledgeAnswer) error
	StaleNotice func(ans types.KnowledgeAnswer) string
	Unverified  func(out types.KnowledgeOccurrencesOutput) string
	IndexCause  func(ans types.KnowledgeAnswer) types.KnowledgeAnswer
}

// OccurrenceRead is what a symbol's occurrence read found: the verified sites,
// the spellings they were checked against, and every declared index that
// yielded nothing usable.
type OccurrenceRead struct {
	Files      []types.SymbolOccurrenceFile
	Names      []string
	Unreadable []types.KnowledgeSymbolGap
}

// WriteRefsText writes out, a resolved symbol's references, as `magus refs` prints them
// in text form, and returns the error that sets the exit status.
func WriteRefsText(w io.Writer, env RefsEnv, out types.KnowledgeRefsOutput) error {
	fmt.Fprintf(w, "symbol: %s", out.Symbol)
	if out.Label != "" {
		fmt.Fprintf(w, "  (%s)", out.Label)
	}
	fmt.Fprintln(w)
	if len(out.Defs) > 0 {
		fmt.Fprintln(w, "defined in:")
		for _, d := range out.Defs {
			fmt.Fprintf(w, "  %s\n", d.File)
		}
	}
	if len(out.Refs) == 0 {
		fmt.Fprintln(w, "no references found")
		env.Verdict(w, out.Answer)
		if err := env.Staleness(w, out.Answer); err != nil {
			return err
		}
		// "nothing uses this" is a NEGATIVE claim, so it follows the verdict the same way
		// an unresolved name does: exit 1 when magus could not verify it. Absent stays 0
		// here, unlike the unresolved branch above: the symbol resolved and its empty
		// reference list is a real, verified answer, not a request magus could not carry out.
		if out.Answer.Verdict == types.VerdictUnknown {
			return exit.Reported(exit.Failure)
		}
		return nil
	}
	fmt.Fprintf(w, "referenced in %d file(s), %d occurrence(s):\n", out.FileCount, out.RefCount)
	for _, r := range out.Refs {
		fmt.Fprintf(w, "  %s  (%d)%s\n", r.File, r.Count, linesSuffix(r.Lines))
	}
	// Under the rows, never instead of them. A found answer from a stale index is the
	// dangerous one: it looks complete, and nothing else on this path would say otherwise.
	return env.Staleness(w, out.Answer)
}

// EmitOccurrences renders `magus refs <symbol> --occurrences`: every exact source range
// the symbol appears at, verified against the tree. refs has already resolved the symbol
// and computed the coverage answer; this re-reads the declared indexes for the ranges the
// graph edge does not keep.
//
// It reports; it does not edit. The output is what a caller needs to make the edit itself,
// which is the same division every other magus verb keeps between naming what a change
// touches and touching it.
func EmitOccurrences(ctx context.Context, env RefsEnv, opts output.Options, refs types.KnowledgeRefsOutput, occurrences *OccurrenceRead, source RefsWindow, page RefsPage) error {
	if occurrences == nil {
		// The probe itself failed, so magus cannot say where the symbol appears. Reporting
		// an empty list here would read as "nowhere", which is the one answer it has no
		// basis for.
		slog.ErrorContext(ctx, "cannot read the symbol indexes", attr.Notice("magus refs"))
		return exit.Reported(exit.Failure)
	}
	read := *occurrences
	files := page.Scoped(read.Files)

	out := types.KnowledgeOccurrencesOutput{
		Definition: types.KnowledgeOccurrencesDefinition,
		APIVersion: types.KnowledgeAPIVersion,
		Symbol:     refs.Symbol,
		Label:      refs.Label,
		Names:      read.Names,
		Files:      files,
		FileCount:  len(files),
	}
	if len(read.Names) > 0 {
		out.Name = read.Names[0]
	}
	// The refs verdict describes the refs answer, and this is a different read of a
	// different source: an index that is merely declared satisfies refs' coverage check and
	// can still fail to decode here. So the gaps are recomposed rather than inherited, and
	// an index this read could not open downgrades the verdict even when refs was clean.
	out.Answer = types.ClassifyAnswer(len(read.Files) > 0, refs.Answer.Reason, append(append([]types.KnowledgeSymbolGap(nil), refs.Answer.Gaps...), read.Unreadable...))
	if out.Answer.IndexBehind() {
		if out.Answer.IndexCause = refs.Answer.IndexCause; out.Answer.IndexCause == nil {
			out.Answer = env.IndexCause(out.Answer)
		}
	}
	for _, f := range files {
		out.OccurrenceCount += len(f.Occurrences)
		if f.Stale {
			out.StaleFiles++
		}
		for _, occ := range f.Occurrences {
			if occ.Status == types.SymbolOccurrenceVerified {
				out.VerifiedCount++
			}
		}
	}
	var shown int
	out.Files, shown = page.Cut(files)
	if note := markOccurrenceFiles(ctx, out.Files, env.Classify()); note != "" {
		slog.WarnContext(ctx, note, attr.Notice("magus refs"))
	}
	if source.Show {
		source.fillSources(env.Root, out.Files)
	}
	scopeNote := page.scopeNote(read.Files, out.OccurrenceCount)
	pageNote := page.note(shown, out.OccurrenceCount)
	if opts.Format != output.Text {
		for _, note := range []string{scopeNote, pageNote} {
			if note != "" {
				slog.WarnContext(ctx, note, attr.Notice("magus refs"))
			}
		}
	}

	switch opts.Format {
	case output.JSON, output.YAML, output.JSONL, output.Template:
		if err := env.Format(opts, out); err != nil {
			return err
		}
		// stale_files and verified_count are in the record, and a reader who does not
		// compare them against occurrence_count gets a list of ranges that no longer
		// point at the symbol. The notice rides stderr, where it cannot corrupt the
		// record, and the exit status matches the two arms below.
		if out.VerifiedCount < out.OccurrenceCount {
			slog.ErrorContext(ctx, strings.TrimSuffix(env.Unverified(out), "\n"), attr.Notice(""))
			return exit.Reported(exit.Failure)
		}
		return nil
	case output.Name:
		// file:line:col, the form every editor and `xargs` already understands. Only
		// verified sites: -o name has nowhere to put a status, and emitting an unverified
		// range in a list that looks actionable is exactly the confusion the status exists
		// to prevent.
		var names []string
		for _, f := range out.Files {
			for _, occ := range f.Occurrences {
				if occ.Status == types.SymbolOccurrenceVerified {
					names = append(names, fmt.Sprintf("%s:%d:%d", f.File, occ.Line, occ.Column))
				}
			}
		}
		if err := env.Names(env.Stdout, names); err != nil {
			return err
		}
		// Filtering to verified sites is what makes this format safe to pipe, and it is also
		// what makes a wholly stale index print NOTHING: byte-identical to a symbol with no
		// occurrences at all. This is the format a script reads, so the difference has to
		// live in the exit status, which is the only channel it has left.
		if out.VerifiedCount < out.OccurrenceCount {
			slog.ErrorContext(ctx, fmt.Sprintf("%d of %d site(s) did not verify and were not listed; refresh with `%s`",
				out.OccurrenceCount-out.VerifiedCount, out.OccurrenceCount, hint.GraphBuild), attr.Notice("magus refs"))
			return exit.Reported(exit.Failure)
		}
		// Deliberately NOT exitForVerdict here. The coverage verdict is `unknown` whenever any
		// project declares no index, which in a polyglot workspace is the steady state (a
		// TypeScript project holds no Go symbols), so folding it in would make this format
		// exit non-zero on every successful run and teach a caller to ignore the status. The
		// exit code carries one meaning: sites were found and withheld.
		return nil
	}

	fmt.Fprintf(env.Stdout, "symbol: %s", out.Symbol)
	if out.Label != "" {
		fmt.Fprintf(env.Stdout, "  (%s)", out.Label)
	}
	fmt.Fprintln(env.Stdout)
	if scopeNote != "" {
		fmt.Fprintln(env.Stdout, scopeNote)
	}
	if out.OccurrenceCount == 0 {
		fmt.Fprintln(env.Stdout, "no occurrences found")
		env.Verdict(env.Stdout, out.Answer)
		if out.Answer.Verdict == types.VerdictUnknown {
			return exit.Reported(exit.Failure)
		}
		return nil
	}
	fmt.Fprintf(env.Stdout, "name: %s\n", out.Name)
	fmt.Fprintf(env.Stdout, "%d occurrence(s) in %d file(s), %d verified:\n", out.OccurrenceCount, out.FileCount, out.VerifiedCount)
	for _, f := range out.Files {
		fmt.Fprintf(env.Stdout, "  %s", f.File)
		if f.Generated {
			fmt.Fprint(env.Stdout, "  (generated: regenerate it, never hand-edit)")
		}
		if f.Test {
			fmt.Fprint(env.Stdout, "  (test)")
		}
		if f.Stale {
			fmt.Fprint(env.Stdout, "  (stale: this file changed after it was indexed)")
		}
		fmt.Fprintln(env.Stdout)
		for _, occ := range f.Occurrences {
			fmt.Fprintf(env.Stdout, "    %d:%d-%d:%d  %s", occ.Line, occ.Column, occ.EndLine, occ.EndColumn, occ.Status)
			if occ.Definition {
				fmt.Fprint(env.Stdout, "  definition")
			}
			if occ.Status != types.SymbolOccurrenceVerified && occ.Text != "" {
				// "want one of": the check is against the whole spelling set, so naming a
				// single expectation would misreport what would actually have verified.
				fmt.Fprintf(env.Stdout, "  (found %q, want one of %q)", occ.Text, out.Names)
			}
			fmt.Fprintln(env.Stdout)
			writeSourceBlock(env.Stdout, "      ", occ.Source, occ.SourceStartLine, occ.Line, occ.SourceNote)
		}
	}
	if pageNote != "" {
		fmt.Fprintln(env.Stdout, pageNote)
	}
	if out.VerifiedCount < out.OccurrenceCount {
		// The count alone does not say what to do about it, and the wrong response (edit
		// the good ones, skip the rest) produces a half-renamed tree that still compiles
		// in some languages.
		fmt.Fprint(env.Stdout, env.Unverified(out))
		env.Verdict(env.Stdout, out.Answer)
		return exit.Reported(exit.Failure)
	}
	env.Verdict(env.Stdout, out.Answer)
	return nil
}

// EmitDefinitions renders `magus refs <symbol> --definition`. It exits 1 when a range no
// longer holds what was indexed or cannot be read: the lines printed for it are not the
// symbol's, and a caller editing by them would edit the wrong text.
func EmitDefinitions(ctx context.Context, env RefsEnv, opts output.Options, out types.KnowledgeDefinitionsOutput) error {
	var stale, verified int
	for _, s := range out.Definitions {
		switch s.Status {
		case types.DefinitionChanged, types.DefinitionUnreadable:
			stale++
		case types.DefinitionVerified:
			verified++
		}
	}
	staleErr := func() error {
		if stale > 0 {
			return exit.Reported(exit.Failure)
		}
		return nil
	}
	// An index older than its project's latest edit may miss a definition added since,
	// which is what reportIndexStaleness fails an answer for. A verified digest proves the
	// site it names, so an answer made only of those carries the notice and exits 0: in a
	// checkout being edited some index is always behind, and an exit that is always 1
	// teaches callers to ignore it.
	settle := func(nw io.Writer) error {
		if verified > 0 && verified == len(out.Definitions) {
			fmt.Fprint(nw, env.StaleNotice(out.Answer))
			return nil
		}
		if err := env.Staleness(nw, out.Answer); err != nil {
			return err
		}
		return staleErr()
	}

	switch opts.Format {
	case output.JSON, output.YAML, output.JSONL, output.Template:
		if err := env.Format(opts, out); err != nil {
			return err
		}
		if stale > 0 {
			slog.ErrorContext(ctx, fmt.Sprintf("%d definition(s) changed since indexing; refresh with `%s`", stale, hint.GraphBuild), attr.Notice("magus refs"))
		}
		return settle(env.Notices)
	case output.Name:
		var names []string
		for _, s := range out.Definitions {
			if s.StartLine > 0 && s.Status != types.DefinitionChanged && s.Status != types.DefinitionUnreadable {
				names = append(names, definitionRange(s))
			}
		}
		if err := env.Names(env.Stdout, names); err != nil {
			return err
		}
		if stale > 0 {
			slog.ErrorContext(ctx, fmt.Sprintf("%d definition(s) changed since indexing and were not listed; refresh with `%s`", stale, hint.GraphBuild), attr.Notice("magus refs"))
		}
		return staleErr()
	}

	fmt.Fprintf(env.Stdout, "symbol: %s", out.Symbol)
	if out.Label != "" {
		fmt.Fprintf(env.Stdout, "  (%s)", out.Label)
	}
	fmt.Fprintln(env.Stdout)
	if len(out.Definitions) == 0 {
		fmt.Fprintln(env.Stdout, "no definition in this workspace")
		env.Verdict(env.Stdout, out.Answer)
		if out.Answer.Verdict == types.VerdictUnknown {
			return exit.Reported(exit.Failure)
		}
		return nil
	}
	for i, s := range out.Definitions {
		if i > 0 && s.SourceStartLine > 0 {
			fmt.Fprintln(env.Stdout)
		}
		fmt.Fprint(env.Stdout, definitionRange(s))
		if s.StartLine == 0 {
			fmt.Fprint(env.Stdout, "  (the index recorded no line in this file)")
		} else {
			fmt.Fprintf(env.Stdout, "  %s", s.Status)
			if s.EndLine == 0 {
				fmt.Fprint(env.Stdout, "  (the index recorded no end line; the declaration line alone)")
			}
			if s.Status == types.DefinitionChanged {
				fmt.Fprintf(env.Stdout, "  (edited since indexing; refresh with `%s`)", hint.GraphBuild)
			}
		}
		fmt.Fprintln(env.Stdout)
		writeSourceBlock(env.Stdout, "", s.Source, s.SourceStartLine, s.StartLine, s.SourceNote)
	}
	return settle(env.Stdout)
}
