// Package errmsg holds the text of plain Go errors to the standard
// library's convention: an error is a sentence fragment, and as it wraps, each
// caller chains its context in front with ": ". A message written as prose
// breaks that chain.
//
// It judges the message argument of errors.New and the format of fmt.Errorf,
// wherever they are called, including inside a call that builds a coded
// diagnostic: that inner error is still a plain error. It judges what an
// error type's own Error() method returns the same way. A coded diagnostic's
// own constructor is never judged here. String constants and concatenations
// resolve to their text; an operand only known at run time is opaque.
//
// It also judges a log/slog call: an error built into its message, or its
// text passed as an attribute keyed "error" or "err", is reported, because
// the error belongs in attr.Error where the display can name its origin once.
//
// Capitalization and trailing punctuation are staticcheck's ST1005.
package errmsg

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	// RuleOrigin reports a leading "name: " naming another package of the
	// module: the error that originates a failure names its own origin. It
	// needs [Options.Module] to know the module's packages.
	RuleOrigin Rule = "error-origin"
	// RuleStutter reports a wrap opening with its own package's "name: "
	// around a variable last assigned from a call into that package, whose
	// error already names it.
	RuleStutter Rule = "error-stutter"
	// RuleNotice reports an error built into the message of a log/slog call
	// that carries one of [Options.NoticeAttrs]: the error rides as an
	// attribute, so the display can name its origin once.
	RuleNotice Rule = "error-notice"
	// RuleLog reports an error built into the message of any other log/slog
	// call, which the display prints the same way.
	RuleLog Rule = "error-log"
	// RuleAttr reports an error's text passed as a log/slog attribute keyed
	// "error" or "err": the error itself rides attr.Error.
	RuleAttr Rule = "error-attr"
)

// Rules lists every rule in report order.
var Rules = []Rule{RuleJoin, RuleNewline, RuleSentences, RuleWrap, RuleOrigin, RuleStutter, RuleNotice, RuleLog, RuleAttr}

var messages = map[Rule]string{
	RuleJoin:      `a clause joined by %q: chain context with ": " or use a comma`,
	RuleNewline:   `a newline in an error string: keep it one line and chain context with ": "`,
	RuleSentences: `a second sentence in an error string: write a fragment and chain context with ": "`,
	RuleWrap:      `%w only opens the format as "%w: " or closes it as ": %w"`,
	RuleOrigin:    `a %q prefix names another package of this module: name the operation, and let the origin name itself`,
	RuleStutter:   `a %q prefix on an error this package already returned: say what this call was doing`,
	RuleNotice:    `an error in a notice's message: say what failed, and attach the error with attr.Error`,
	RuleLog:       `an error in a log message: say what failed, and attach the error with attr.Error`,
	RuleAttr:      `an error's text in an "error" attribute: attach the error itself with attr.Error, not err.Error()`,
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

	// Operations are leading names error-origin passes although a package of
	// the module shares them, because error text uses them for an operation,
	// as in "run: ". Each must name a package: one that does not exempts
	// nothing.
	Operations []string `json:"operations"`

	// NoticeAttrs are the functions, as [types.Func.FullName] spells them,
	// whose attribute marks a log record as a notice to a person. error-notice
	// judges a log/slog call passing one; error-log judges every other.
	NoticeAttrs []string `json:"notice-attrs"`

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
			return nil, fmt.Errorf("errmsg: rule %q is not one of %q", r, Rules)
		}
	}
	allowed := make(source.Globs, 0, len(opts.Allow))
	for _, a := range opts.Allow {
		if a.File == "" || a.Reason == "" {
			return nil, fmt.Errorf("errmsg: allow entry %+v needs both file and reason", a)
		}
		if a.Rule != "" && !slices.Contains(Rules, a.Rule) {
			return nil, fmt.Errorf("errmsg: allow entry %+v names no rule of %q", a, Rules)
		}
		allowed = append(allowed, a.File)
	}
	if err := opts.Files.Validate("errmsg"); err != nil {
		return nil, err
	}
	if err := allowed.Validate("errmsg"); err != nil {
		return nil, err
	}
	var packages map[string]bool
	if err := source.InModule("errmsg", opts.Module, func(root string) error {
		if err := opts.Files.RequireMatches("errmsg", "files", root); err != nil {
			return err
		}
		if err := allowed.RequireMatches("errmsg", "allow", root); err != nil {
			return err
		}
		var err error
		if packages, err = packageNames(root); err != nil {
			return err
		}
		for _, op := range opts.Operations {
			if !packages[op] {
				return fmt.Errorf("errmsg: operation %q names no package under %s: it exempts nothing, fix the setting", op, root)
			}
			delete(packages, op)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "errmsg",
		Doc:  "judge error text (errors.New, fmt.Errorf, Error methods) as a fragment chained with \": \"",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts, packages) },
	}, nil
}

// packageNames returns the name of every package under root but main, nested
// modules included, read from the package clause of each non-test file: a
// build-ignored script in a directory can declare a package of its own.
func packageNames(root string) (map[string]bool, error) {
	files, err := source.GoFiles(root)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, rel := range files {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(root, filepath.FromSlash(rel)), nil, parser.PackageClauseOnly)
		if err != nil {
			return nil, fmt.Errorf("errmsg: %w", err)
		}
		if f.Name.Name != "main" {
			names[f.Name.Name] = true
		}
	}
	return names, nil
}

func run(pass *analysis.Pass, opts Options, packages map[string]bool) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		rel, ok := source.Rel(pass, opts.Module, f)
		if !ok || source.IsTest(pass, f) || (len(opts.Files) > 0 && !opts.Files.Match(rel)) {
			continue
		}
		emit := func(pos token.Pos, findings []Finding) {
			for _, rule := range findings {
				if (len(opts.Rules) == 0 || slices.Contains(opts.Rules, rule.Rule)) && !allowed(opts, rel, rule.Rule) {
					pass.Report(analysis.Diagnostic{
						Pos:      pos,
						Category: string(rule.Rule),
						Message:  source.Hint(fmt.Sprintf("%s: %s", rule.Rule, rule.Message), opts.Hint),
					})
				}
			}
		}
		report := func(e ast.Expr, format bool) {
			text, pos, ok := resolve(pass, e)
			if !ok {
				return
			}
			findings := Judge(text, format)
			if name := prefix(text); name != "" && name != pass.Pkg.Name() && packages[name] {
				findings = append(findings, Finding{RuleOrigin, fmt.Sprintf(messages[RuleOrigin], name+": ")})
			}
			emit(pos, findings)
		}
		var body *ast.BlockStmt
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if msg, rest := logMessage(pass, n); msg != nil {
					if carriesError(pass, msg) {
						rule := RuleLog
						if passesAny(pass, rest, opts.NoticeAttrs) {
							rule = RuleNotice
						}
						emit(msg.Pos(), []Finding{{rule, messages[rule]}})
					}
					for i := 0; i+1 < len(rest); i++ {
						if isErrorKey(pass, rest[i]) && isErrorText(pass, rest[i+1]) {
							emit(rest[i+1].Pos(), []Finding{{RuleAttr, messages[RuleAttr]}})
						}
					}
				}
				if fn, ok := typeutil.Callee(pass.TypesInfo, n).(*types.Func); ok && len(n.Args) == 2 &&
					(fn.FullName() == "log/slog.String" || fn.FullName() == "log/slog.Any") &&
					isErrorKey(pass, n.Args[0]) && isErrorText(pass, n.Args[1]) {
					emit(n.Args[1].Pos(), []Finding{{RuleAttr, messages[RuleAttr]}})
				}
				switch callee(pass, n) {
				case "errors.New":
					report(n.Args[0], false)
				case "fmt.Errorf":
					report(n.Args[0], true)
					if body != nil && stutters(pass, body, n) {
						emit(n.Args[0].Pos(), []Finding{{RuleStutter, fmt.Sprintf(messages[RuleStutter], pass.Pkg.Name()+": ")}})
					}
				}
			case *ast.FuncDecl:
				body = n.Body
				if errorMethod(pass, n) {
					judgeReturns(pass, n.Body, report)
				}
			}
			return true
		})
	}
	return nil
}

// logMessage is the message argument of call and the arguments after it when
// call is a log/slog logging call, or nil.
func logMessage(pass *analysis.Pass, call *ast.CallExpr) (ast.Expr, []ast.Expr) {
	fn, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "log/slog" {
		return nil, nil
	}
	idx := slogMessageArg(fn)
	if idx < 0 || idx >= len(call.Args) {
		return nil, nil
	}
	return call.Args[idx], call.Args[idx+1:]
}

// passesAny reports whether args holds a call to one of funcs.
func passesAny(pass *analysis.Pass, args []ast.Expr, funcs []string) bool {
	return slices.ContainsFunc(args, func(a ast.Expr) bool {
		inner, ok := ast.Unparen(a).(*ast.CallExpr)
		if !ok {
			return false
		}
		f, ok := typeutil.Callee(pass.TypesInfo, inner).(*types.Func)
		return ok && slices.Contains(funcs, f.FullName())
	})
}

// isErrorKey reports whether e is the constant "error" or "err".
func isErrorKey(pass *analysis.Pass, e ast.Expr) bool {
	tv, ok := pass.TypesInfo.Types[e]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return false
	}
	k := constant.StringVal(tv.Value)
	return k == "error" || k == "err"
}

// isErrorText reports whether e calls an error's Error method.
func isErrorText(pass *analysis.Pass, e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Error" && isError(pass, sel.X)
}

// isError reports whether e's type implements error.
func isError(pass *analysis.Pass, e ast.Expr) bool {
	errType := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	t := pass.TypesInfo.TypeOf(e)
	return t != nil && types.Implements(t, errType)
}

// slogMessageArg is the index of the message argument of a log/slog logging
// function or *slog.Logger method, or -1 for anything else.
func slogMessageArg(fn *types.Func) int {
	name := fn.Name()
	switch {
	case name == "Log" || name == "LogAttrs":
		return 2
	case slices.Contains([]string{"DebugContext", "InfoContext", "WarnContext", "ErrorContext"}, name):
		return 1
	case slices.Contains([]string{"Debug", "Info", "Warn", "Error"}, name):
		return 0
	}
	return -1
}

// carriesError reports whether msg calls an error's Error method, or formats
// an error-typed operand.
func carriesError(pass *analysis.Pass, msg ast.Expr) bool {
	isErr := func(e ast.Expr) bool { return isError(pass, e) }
	found := false
	ast.Inspect(msg, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" && len(call.Args) == 0 && isErr(sel.X) {
			found = true
			return false
		}
		if fn, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func); ok && fn.Pkg() != nil && fn.Pkg().Path() == "fmt" {
			found = slices.ContainsFunc(call.Args, isErr)
		}
		return !found
	})
	return found
}

// callee is the full name of the function call invokes with at least one
// argument, or "" for anything else.
func callee(pass *analysis.Pass, call *ast.CallExpr) string {
	if len(call.Args) == 0 {
		return ""
	}
	fn, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok {
		return ""
	}
	return fn.FullName()
}

// errorMethod reports whether decl is an `Error() string` method, the text an
// error type of our own prints wherever it wraps.
func errorMethod(pass *analysis.Pass, decl *ast.FuncDecl) bool {
	if decl.Recv == nil || decl.Name.Name != "Error" || decl.Body == nil {
		return false
	}
	fn, ok := pass.TypesInfo.Defs[decl.Name].(*types.Func)
	if !ok {
		return false
	}
	sig, ok := fn.Type().(*types.Signature)
	return ok && sig.Params().Len() == 0 && sig.Results().Len() == 1 &&
		types.Identical(sig.Results().At(0).Type(), types.Typ[types.String])
}

// judgeReturns judges what body's own return statements build: a string, or the
// format of a fmt.Sprintf. A function literal inside body returns for itself.
func judgeReturns(pass *analysis.Pass, body *ast.BlockStmt, report func(ast.Expr, bool)) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if len(n.Results) != 1 {
				return true
			}
			if call, ok := ast.Unparen(n.Results[0]).(*ast.CallExpr); ok && callee(pass, call) == "fmt.Sprintf" {
				report(call.Args[0], true)
				return true
			}
			report(n.Results[0], false)
		}
		return true
	})
}

// leading matches the "name: " an error opens with to say where it came from.
var leading = regexp.MustCompile(`^([a-z][a-z0-9_]*): `)

// prefix is the name text opens with, or "".
func prefix(text string) string {
	if m := leading.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return ""
}

// stutters reports whether call, a fmt.Errorf inside body, opens with its own
// package's name while its %w operand is a variable last assigned from a call
// into that package. A call written as the operand itself is not followed: it
// is usually a helper that annotates an error from elsewhere, such as one
// adding a subprocess's stderr.
func stutters(pass *analysis.Pass, body *ast.BlockStmt, call *ast.CallExpr) bool {
	text, _, ok := resolve(pass, call.Args[0])
	if !ok || prefix(text) != pass.Pkg.Name() {
		return false
	}
	id, ok := wrapOperand(text, call.Args[1:]).(*ast.Ident)
	if !ok {
		return false
	}
	obj := pass.TypesInfo.ObjectOf(id)
	if obj == nil {
		return false
	}
	inner, ok := ast.Unparen(lastAssigned(pass, body, obj, call.Pos())).(*ast.CallExpr)
	return ok && samePackage(pass, inner)
}

// wrapOperand returns the argument the last %w of format consumes, or nil
// when a "*" width or precision leaves the count to run time.
func wrapOperand(format string, args []ast.Expr) ast.Expr {
	var wrapped ast.Expr
	next := 0
	for _, v := range verb.FindAllString(format, -1) {
		if v == "%%" {
			continue
		}
		if strings.Contains(v, "*") {
			return nil
		}
		if m := argIndex.FindStringSubmatch(v); m != nil {
			n, _ := strconv.Atoi(m[1])
			next = n - 1
		}
		if strings.HasSuffix(v, "w") && next >= 0 && next < len(args) {
			wrapped = ast.Unparen(args[next])
		}
		next++
	}
	return wrapped
}

// argIndex matches the explicit argument index of a verb such as "%[2]w".
var argIndex = regexp.MustCompile(`\[(\d+)\][a-zA-Z]$`)

// lastAssigned returns the expression last assigned to obj in body before
// pos, or nil.
func lastAssigned(pass *analysis.Pass, body *ast.BlockStmt, obj types.Object, pos token.Pos) ast.Expr {
	var last ast.Expr
	var at token.Pos
	assign := func(lhs []*ast.Ident, rhs []ast.Expr, stmt token.Pos) {
		if stmt >= pos || stmt < at {
			return
		}
		for i, id := range lhs {
			if id == nil || pass.TypesInfo.ObjectOf(id) != obj {
				continue
			}
			switch {
			case len(rhs) == len(lhs):
				last, at = rhs[i], stmt
			case len(rhs) == 1:
				last, at = rhs[0], stmt
			}
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			ids := make([]*ast.Ident, len(n.Lhs))
			for i, e := range n.Lhs {
				ids[i], _ = e.(*ast.Ident)
			}
			assign(ids, n.Rhs, n.Pos())
		case *ast.ValueSpec:
			assign(n.Names, n.Values, n.Pos())
		}
		return true
	})
	return last
}

// samePackage reports whether call statically invokes a function or concrete
// method of the package under analysis.
func samePackage(pass *analysis.Pass, call *ast.CallExpr) bool {
	fn := typeutil.StaticCallee(pass.TypesInfo, call)
	return fn != nil && fn.Pkg() == pass.Pkg
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
