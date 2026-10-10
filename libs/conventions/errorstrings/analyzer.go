// Package errorstrings holds the text of plain Go errors to the standard
// library's convention: an error is a sentence fragment, and as it wraps, each
// caller chains its context in front with ": ". A message written as prose
// breaks that chain.
//
// It judges the message argument of errors.New and the format of fmt.Errorf,
// wherever they are called, including inside a call that builds a coded
// diagnostic: that inner error is still a plain error. A coded diagnostic's
// own constructor is never judged here. String constants and concatenations
// resolve to their text; an operand only known at run time is opaque.
//
// Capitalization and trailing punctuation are staticcheck's ST1005.
package errorstrings

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// Rule names one thing an error string must not do.
type Rule string

// The rules, each reported once per message.
const (
	// RuleJoin reports a clause joined by `"; "`, `" - "` or an em dash.
	RuleJoin Rule = "error-join"
	// RuleNewline reports a newline.
	RuleNewline Rule = "error-newline"
	// RuleSentences reports a period followed by more text.
	RuleSentences Rule = "error-sentences"
	// RuleWrap reports a %w anywhere but a closing ": %w".
	RuleWrap Rule = "error-wrap"
)

// Rules lists every rule in report order.
var Rules = []Rule{RuleJoin, RuleNewline, RuleSentences, RuleWrap}

var messages = map[Rule]string{
	RuleJoin:      `a clause joined by %q: chain context with ": " or use a comma`,
	RuleNewline:   `a newline in an error string: keep it one line and chain context with ": "`,
	RuleSentences: `a second sentence in an error string: write a fragment and chain context with ": "`,
	RuleWrap:      `%w only opens the format as "%w: " or closes it as ": %w"`,
}

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.Files] and [AllowEntry.File] are
	// relative to.
	Module string `json:"module"`

	// Files are the sources held to the rules. Empty holds every file. A test
	// file is never held: its errors are fixtures.
	Files source.Globs `json:"files"`

	// Rules are the rules reported. Empty reports every rule.
	Rules []Rule `json:"rules"`

	// Allow exempts a file from one rule, or from every rule when Rule is
	// empty.
	Allow []AllowEntry `json:"allow"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// AllowEntry exempts one file.
type AllowEntry struct {
	// File is a [source.Globs] pattern over module-relative paths.
	File string `json:"file"`

	// Rule is the one rule exempted, or empty for all of them.
	Rule Rule `json:"rule"`

	// Reason says why the file is exempt.
	Reason string `json:"reason"`
}

// New returns the analyzer configured by opts, erroring on a malformed glob,
// an unknown rule, and an allow entry with no reason or no file it matches.
func New(opts Options) (*analysis.Analyzer, error) {
	for _, r := range opts.Rules {
		if !slices.Contains(Rules, r) {
			return nil, fmt.Errorf("errorstrings: rule %q is not one of %q", r, Rules)
		}
	}
	allowed := make(source.Globs, 0, len(opts.Allow))
	for _, a := range opts.Allow {
		if a.File == "" || a.Reason == "" {
			return nil, fmt.Errorf("errorstrings: allow entry %+v needs both file and reason", a)
		}
		if a.Rule != "" && !slices.Contains(Rules, a.Rule) {
			return nil, fmt.Errorf("errorstrings: allow entry %+v names no rule of %q", a, Rules)
		}
		allowed = append(allowed, a.File)
	}
	if err := opts.Files.Validate("errorstrings"); err != nil {
		return nil, err
	}
	if err := allowed.Validate("errorstrings"); err != nil {
		return nil, err
	}
	if err := source.InModule("errorstrings", opts.Module, func(root string) error {
		if err := opts.Files.RequireMatches("errorstrings", "files", root); err != nil {
			return err
		}
		return allowed.RequireMatches("errorstrings", "allow", root)
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "errorstrings",
		Doc:  "judge errors.New and fmt.Errorf text as a fragment chained with \": \"",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		rel, ok := source.Rel(pass, opts.Module, f)
		if !ok || source.IsTest(pass, f) || (len(opts.Files) > 0 && !opts.Files.Match(rel)) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			fn, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
			if !ok {
				return true
			}
			var format bool
			switch fn.FullName() {
			case "errors.New":
			case "fmt.Errorf":
				format = true
			default:
				return true
			}
			if text, pos, ok := resolve(pass, call.Args[0]); ok {
				for _, rule := range Judge(text, format) {
					if (len(opts.Rules) == 0 || slices.Contains(opts.Rules, rule.Rule)) && !allowed(opts, rel, rule.Rule) {
						pass.Report(analysis.Diagnostic{
							Pos:      pos,
							Category: string(rule.Rule),
							Message:  source.Hint(fmt.Sprintf("%s: %s", rule.Rule, rule.Message), opts.Hint),
						})
					}
				}
			}
			return true
		})
	}
	return nil
}

func allowed(opts Options, rel string, rule Rule) bool {
	for _, a := range opts.Allow {
		if (a.Rule == "" || a.Rule == rule) && source.Globs([]string{a.File}).Match(rel) {
			return true
		}
	}
	return false
}

// opaque stands in for an operand only known at run time. It is no letter,
// space or punctuation, so no rule reads it as text of its own.
const opaque = "\x00"

// resolve returns the text e builds and the position of its first known part,
// or false when no part of it is known.
func resolve(pass *analysis.Pass, e ast.Expr) (string, token.Pos, bool) {
	e = ast.Unparen(e)
	if tv, ok := pass.TypesInfo.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value), e.Pos(), true
	}
	bin, ok := e.(*ast.BinaryExpr)
	if !ok || bin.Op != token.ADD {
		return "", token.NoPos, false
	}
	left, lpos, lok := resolve(pass, bin.X)
	right, rpos, rok := resolve(pass, bin.Y)
	switch {
	case lok && rok:
		return left + right, lpos, true
	case lok:
		return left + opaque, lpos, true
	case rok:
		return opaque + right, rpos, true
	}
	return "", token.NoPos, false
}

// Finding is one rule an error string breaks.
type Finding struct {
	Rule    Rule
	Message string
}

// verb matches one fmt verb, flags, width, precision and index included, and
// the escaped percent.
var verb = regexp.MustCompile(`%[-+# 0]*(?:\[\d+\])?(?:\d+|\*)?(?:\.(?:\d+|\*)?)?(?:\[\d+\])?[a-zA-Z%]`)

// abbreviations end in a period that ends no sentence.
var abbreviations = []string{"e.g.", "i.e.", "etc.", "vs.", "cf.", "incl.", "approx."}

// Judge returns the rules text breaks, in [Rules] order. A format's verbs are
// read as fmt reads them, so a precision such as "%.2f" holds no period.
func Judge(text string, format bool) []Finding {
	var findings []Finding
	plain := text
	if format {
		plain = verb.ReplaceAllStringFunc(text, func(v string) string {
			if v == "%%" {
				return "%"
			}
			return opaque
		})
	}
	prose := unquote(plain)
	for _, sep := range []string{"; ", " - ", "—"} {
		if strings.Contains(prose, sep) {
			findings = append(findings, Finding{RuleJoin, fmt.Sprintf(messages[RuleJoin], sep)})
			break
		}
	}
	if strings.Contains(plain, "\n") {
		findings = append(findings, Finding{RuleNewline, messages[RuleNewline]})
	}
	if secondSentence(prose) {
		findings = append(findings, Finding{RuleSentences, messages[RuleSentences]})
	}
	if format && misplacedWrap(text) {
		findings = append(findings, Finding{RuleWrap, messages[RuleWrap]})
	}
	return findings
}

// unquote blanks every backticked, double-quoted or single-quoted span, so
// quoted text is never read as the message's own clauses. A single quote opens
// a span only after a non-letter and closes one only before a non-letter, so
// an apostrophe opens nothing.
func unquote(s string) string {
	b := []byte(s)
	letter := func(i int) bool { return i >= 0 && i < len(b) && wordByte(b[i]) }
	for i := 0; i < len(b); i++ {
		q := b[i]
		if q != '`' && q != '"' && q != '\'' {
			continue
		}
		if q == '\'' && letter(i-1) {
			continue
		}
		end := -1
		for j := i + 1; j < len(b); j++ {
			if b[j] == q && (q != '\'' || !letter(j+1)) {
				end = j
				break
			}
		}
		if end < 0 {
			continue
		}
		for j := i + 1; j < end; j++ {
			b[j] = 'x'
		}
		i = end
	}
	return string(b)
}

// secondSentence reports a period, not part of an ellipsis or an
// abbreviation, followed by a space and more text.
func secondSentence(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] != '.' || s[i+1] != ' ' || strings.TrimSpace(s[i+1:]) == "" {
			continue
		}
		if i > 0 && s[i-1] == '.' {
			continue
		}
		if slices.ContainsFunc(abbreviations, func(a string) bool { return abbreviated(s[:i+1], a) }) {
			continue
		}
		return true
	}
	return false
}

// abbreviated reports whether s ends in the word abbr.
func abbreviated(s, abbr string) bool {
	if len(s) < len(abbr) || !strings.EqualFold(s[len(s)-len(abbr):], abbr) {
		return false
	}
	before := len(s) - len(abbr) - 1
	return before < 0 || !wordByte(s[before])
}

// wordByte reports an ASCII letter or digit.
func wordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// misplacedWrap reports a %w verb anywhere but the two places Go puts one: the very
// end of format right after ": " (the cause), or the very start right before ": "
// (a sentinel the detail qualifies, as in "%w: %s").
func misplacedWrap(format string) bool {
	for _, loc := range verb.FindAllStringIndex(format, -1) {
		if format[loc[1]-1] != 'w' {
			continue
		}
		closes := loc[1] == len(format) && strings.HasSuffix(format[:loc[0]], ": ")
		opens := loc[0] == 0 && strings.HasPrefix(format[loc[1]:], ": ")
		if !closes && !opens {
			return true
		}
	}
	return false
}
