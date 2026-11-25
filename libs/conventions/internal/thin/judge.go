package thin

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"unicode/utf8"

	"github.com/egladman/magus/libs/conventions/internal/deviation"
	"github.com/egladman/magus/libs/conventions/internal/gitdiff"
	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

// Rule is what one analyzer judges.
type Rule struct {
	// Keep accepts a helper of the analyzer's body shape and names that shape
	// for the diagnostic, such as "returns one composite literal".
	Keep func(pass *analysis.Pass, h *Helper) (shape string, ok bool)

	// MaxSites is the most use sites a reported helper has.
	MaxSites int

	// MaxRepeatedText is the most runes of string literal a body with two sites
	// or more may hold. Inlining writes the text twice, and copies drift.
	MaxRepeatedText int

	// ValueUseExempt skips a helper some site passes as a value.
	ValueUseExempt bool

	// Changes scopes the rule to a change. A helper is reported only when a
	// changed line meets its declaration, so the tree's helpers are bystanders.
	Changes Changes

	Ledgers *deviation.Ledgers
	Hint    string
}

// Changes reports the changed lines of a file, as [gitdiff.Changes] does.
type Changes interface {
	Lines(path string) ([]gitdiff.Span, error)
}

// Departure is a helper the rule reports, with the shape Keep named.
type Departure struct {
	*Helper
	Shape string
}

// Judge returns the helpers of pass that depart from rule, that a change
// touches, and that no exit accepts. It reports each exit naming a file of
// pass whose function does not depart, which is stale.
func Judge(pass *analysis.Pass, rule Rule) ([]Departure, error) {
	files, err := source.Files(pass)
	if err != nil {
		return nil, err
	}
	ignored := files[len(pass.Files):]
	shapes := map[*Helper]string{}
	helpers := find(pass, ignored, func(h *Helper) bool {
		shape, ok := rule.Keep(pass, h)
		shapes[h] = shape
		return ok
	})
	var out []Departure
	departing := map[string]bool{}
	for _, h := range helpers {
		if h.Uses() > rule.MaxSites || (rule.ValueUseExempt && h.ValueUse()) {
			continue
		}
		if h.Uses() > 1 && textRunes(h.Expr) > rule.MaxRepeatedText {
			continue
		}
		ledger, rel, ok, err := rule.Ledgers.Place(pass, h.File)
		if err != nil {
			return nil, err
		}
		if ok && ledger.Accepts(rel, h.Func.Name()) {
			departing[rel+"#"+h.Func.Name()] = true
			continue
		}
		touched, err := touches(pass, rule.Changes, h)
		if err != nil {
			return nil, err
		}
		if touched {
			out = append(out, Departure{Helper: h, Shape: shapes[h]})
		}
	}
	// An exit for a file this build excludes cannot be judged here; the
	// platform that compiles it reads it.
	for _, f := range pass.Files {
		ledger, rel, ok, err := rule.Ledgers.Place(pass, f)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, x := range ledger.In(rel) {
			if !departing[rel+"#"+x.Symbol] {
				pass.Reportf(f.Package, "%s", source.Hint(fmt.Sprintf(
					"the exit for %s in %s names no helper this rule reports: delete it", x.Symbol, rel), rule.Hint))
			}
		}
	}
	return out, nil
}

func touches(pass *analysis.Pass, changes Changes, h *Helper) (bool, error) {
	changed, err := changes.Lines(source.Name(pass, h.File))
	if err != nil {
		return false, fmt.Errorf("%s: %w", pass.Analyzer.Name, err)
	}
	from, to := pass.Fset.Position(h.Decl.Pos()).Line, pass.Fset.Position(h.Decl.End()).Line
	return gitdiff.Overlaps(changed, from, to), nil
}

// Report reports d at each site with the edit that inlines it. Sites in
// excluded files go to the declaration, as does the deleting edit when nothing
// references it.
func Report(pass *analysis.Pass, d Departure, hint string) {
	name := d.Func.Name()
	if d.Uses() == 0 {
		pass.Report(analysis.Diagnostic{
			Pos:     d.Decl.Name.Pos(),
			Message: source.Hint(fmt.Sprintf("%s %s and nothing references it: delete it", name, d.Shape), hint),
			SuggestedFixes: []analysis.SuggestedFix{{
				Message:   "delete " + name,
				TextEdits: remove(pass, d.Helper),
			}},
		})
		return
	}
	message := source.Hint(fmt.Sprintf("%s %s and has %s: inline it", name, d.Shape, sites(d.Uses())), hint)
	if d.Guarded {
		message = source.Hint(fmt.Sprintf("%s %s and has %s: inline it, keeping its nil check at the site", name, d.Shape, sites(d.Uses())), hint)
	}
	for _, s := range d.Sites {
		diag := analysis.Diagnostic{Pos: s.Ident.Pos(), Message: message}
		if edits := inline(pass, d.Helper, s); edits != nil {
			diag.SuggestedFixes = []analysis.SuggestedFix{{Message: "inline " + name, TextEdits: edits}}
		}
		pass.Report(diag)
	}
	if d.Elsewhere > 0 {
		pass.Report(analysis.Diagnostic{Pos: d.Decl.Name.Pos(), Message: source.Hint(
			fmt.Sprintf("%s %s and has %s, %d of them in files this build excludes: inline it at each", name, d.Shape, sites(d.Uses()), d.Elsewhere), hint)})
	}
}

func sites(n int) string {
	if n == 1 {
		return "one use site"
	}
	return fmt.Sprintf("%d use sites", n)
}

func textRunes(e ast.Expr) int {
	n := 0
	ast.Inspect(e, func(n2 ast.Node) bool {
		if lit, ok := n2.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				n += utf8.RuneCountInString(s)
			}
		}
		return true
	})
	return n
}
