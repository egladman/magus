package knowledge

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

func TextHits(matches []textindex.Match) []types.TextHit {
	hits := make([]types.TextHit, len(matches))
	for i, m := range matches {
		hits[i] = types.TextHit{Path: m.Path, Line: m.Line}
	}
	return hits
}

// MissPresence reduces matches to the counts and the capped site list a symbol miss reports.
func MissPresence(root string, matches []textindex.Match) types.KnowledgeTextPresence {
	return types.CountTextPresence(root, TextHits(matches))
}

// RefsMiss is what refs learned about a name no symbol node matches.
type RefsMiss struct {
	Name     string
	Answer   types.KnowledgeAnswer
	Searched int      // files the text search read
	Notes    []string // what the text search skipped or excluded, from textindex.PresenceNotes
}

// ReportRefsMiss renders m. Under a structured format its record goes to stdout, so a
// script reads the verdict and the index cause; the verdict block goes to stderr in every
// format.
func ReportRefsMiss(stderr io.Writer, env RefsEnv, opts output.Options, m RefsMiss) error {
	if opts.Format != output.Text && opts.Format != output.Name {
		if err := env.Format(opts, types.KnowledgeRefsOutput{
			Definition: types.KnowledgeRefsDefinition,
			APIVersion: types.KnowledgeAPIVersion,
			Symbol:     m.Name,
			Answer:     m.Answer,
		}); err != nil {
			return err
		}
	}
	fmt.Fprintf(stderr, "magus refs: no node matches %q\n", m.Name)
	PrintVerdict(stderr, m.Answer, "")
	if m.Answer.Text == nil && len(m.Notes) > 0 {
		fmt.Fprintf(stderr, "  no text match in %d file(s) searched (%s)\n", m.Searched, strings.Join(m.Notes, "; "))
	}
	if t := m.Answer.Text; t != nil {
		fmt.Fprintf(stderr, "  not a symbol, but present as TEXT: %d occurrence(s) in %d file(s) of %d searched",
			t.Hits, t.Files, m.Searched)
		// One parenthetical, not a second line: a count that does not say what it
		// declined to read, or declined to exclude, is one a reader cannot tell from
		// a small or an unfiltered answer.
		if len(m.Notes) > 0 {
			fmt.Fprintf(stderr, " (%s)", strings.Join(m.Notes, "; "))
		}
		fmt.Fprintln(stderr)
		for _, site := range t.Sites {
			fmt.Fprintf(stderr, "    %s\n", site)
		}
		if t.More > 0 {
			fmt.Fprintf(stderr, "    ... and %d more\n", t.More)
		}
		fmt.Fprintln(stderr, "  magus indexes symbols, not text; grep is the tool for a string literal or a comment")
	}
	return nil
}

// refsSymbol picks the symbol `refs` answers for. A name several workspace definitions carry
// is refused, listing each candidate's id, rather than answered. Answering for whichever
// ranked first shows one symbol's references as though they were the name's, which is a
// narrower answer than the text search refs stands in for. Answering for all of them
// would merge different symbols' sites under one count, which a rename or an edit cannot
// tell apart. The refusal costs one more call, with an exact id that also routes to that
// symbol's shards alone. A name one definition carries picks it; a name none carries, or an
// exact id, goes to the graph's own resolution, which also finds a dependency's symbol.
func refsSymbol(w io.Writer, g *Graph, ref string) (string, error) {
	symbol, ambiguous := PickSymbol(g, ref)
	if len(ambiguous) > 0 {
		return "", RefuseAmbiguous(w, ref, ambiguous)
	}
	return symbol, nil
}

// PickSymbol is refsSymbol's choice without the refusal: the symbol to answer for, or the
// candidates when ref names more than one workspace definition.
func PickSymbol(g *Graph, ref string) (symbol string, ambiguous []string) {
	named := g.SymbolsNamed(ref)
	switch len(named) {
	case 0:
		return ref, nil
	case 1:
		return named[0], nil
	}
	return "", named
}

// RefuseAmbiguous lists the symbols ref names, each as the command that asks for it alone, and
// exits 2.
func RefuseAmbiguous(w io.Writer, ref string, named []string) error {
	fmt.Fprintf(w, "magus refs: %q names %d symbols defined in this workspace; ask for one by id:\n", ref, len(named))
	for _, id := range named {
		// Single-quoted: a symbol id holds spaces and backticks, which a paste must not
		// hand to the shell.
		fmt.Fprintf(w, "  %s\n", hint.Refs.With(fmt.Sprintf("'%s'", strings.ReplaceAll(id, "'", `'\''`))))
	}
	return exit.Reported(exit.Usage)
}

// MergeStoredSymbols merges ref's symbol shards as the store holds them. A copy
// of mergeWorkspaceSymbolsForRef's routing without its reassembly, which is
// what a stale read must not pay.
func MergeStoredSymbols(ctx context.Context, store *Store, g *Graph, ref string) error {
	if strings.HasPrefix(ref, types.KindSymbol+":") {
		return store.MergeSymbolShardsByID(ctx, g, []string{ref})
	}
	if names := store.SymbolShardsForLabel(ref); len(names) > 0 {
		routed := NewGraph()
		if err := store.MergeSymbolShardsNamed(ctx, routed, names); err != nil {
			return err
		}
		if len(routed.SymbolsNamed(ref)) > 0 {
			UnionInto(g, routed)
			return nil
		}
	}
	return store.MergeSymbolShards(ctx, g)
}

// BuzzImporters answers, from g's import edges, which workspace-relative files import file.
func BuzzImporters(g *Graph) func(file string) []string {
	byTarget := map[string][]string{}
	for _, e := range g.Edges() {
		if e.Relation != types.RelationImports {
			continue
		}
		from, isFile := strings.CutPrefix(e.Source, types.KindFile+":")
		to, toFile := strings.CutPrefix(e.Target, types.KindFile+":")
		if isFile && toFile {
			byTarget[to] = append(byTarget[to], from)
		}
	}
	return func(file string) []string { return byTarget[file] }
}
