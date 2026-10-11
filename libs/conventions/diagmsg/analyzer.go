// Package diagmsg judges the messages a program prints to whoever runs it,
// by the [prose.KindMessage] rules: a verdict, one next command, and a ref for
// the rationale, never a paragraph.
//
// A message is found where it is built: the argument a configured function
// takes it as, the value of a configured struct field, any string opening
// with a configured prefix, or, with [Options.Slog], a log/slog call's message,
// which is held to the message-tag rule alone. String constants and concatenations resolve to
// their text; a format verb, or an operand only known at run time, counts as
// one rune.
//
// The message-tag rule also catches a tag of two or three words, "diff
// session: ", unless a word of it shows the opening is a sentence ("cannot
// open the file: "). A message opening with one of [Options.Prefixes] is
// labeled on purpose and passes.
package diagmsg

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"github.com/egladman/magus/libs/conventions/prose"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.Files] and [AllowEntry.File] are
	// relative to.
	Module string `json:"module"`

	// Files are the sources held to the rules. Empty holds every file. A test
	// file is never held: its messages are fixtures.
	Files source.Globs `json:"files"`

	// MaxRunes caps a message's length; 0 is [prose.MessageRunes].
	MaxRunes int `json:"max-runes"`

	// Calls are the functions whose argument is a message.
	Calls []Call `json:"calls"`

	// Fields are the struct fields whose value is a message. A field left out,
	// such as a rationale field beside the verdict, is never judged.
	Fields []Field `json:"fields"`

	// Prefixes mark a message by its opening text, wherever it is built.
	Prefixes []string `json:"prefixes"`

	// Slog judges the message of every log/slog call, the package's Debug,
	// Info, Warn and Error functions, their Context forms, and the same methods
	// on a *slog.Logger, by [prose.RuleMessageTag] alone: a record names what
	// logged it with an attribute, never a tag in its text. [Options.Allow]
	// never exempts one, so a file staged for its other messages cannot hide a
	// tagged log line.
	Slog bool `json:"slog"`

	// Rules are the rules reported. Empty reports every message rule.
	Rules []prose.Rule `json:"rules"`

	// Allow exempts a file from one rule, or from every rule when Rule is
	// empty. It never exempts a log/slog message; see [Options.Slog].
	Allow []AllowEntry `json:"allow"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// Call names a function and which of its arguments is a message.
type Call struct {
	// Func is the function as [types.Func.FullName] spells it:
	// "example.com/m/types.Errorf", or "(*example.com/m/diag.Domain).Errorf"
	// for a method.
	Func string `json:"func"`

	// Arg is the message's 0-based argument index.
	Arg int `json:"arg"`

	// Format marks the argument a format string, whose verbs count as one
	// rune each.
	Format bool `json:"format"`
}

// Field names a struct field whose value is a message.
type Field struct {
	// Type is the struct's package path and name: "example.com/m/guard.Verdict".
	Type string `json:"type"`

	// Field is the field's name.
	Field string `json:"field"`
}

// AllowEntry exempts one file.
type AllowEntry struct {
	// File is a [source.Globs] pattern over module-relative paths.
	File string `json:"file"`

	// Rule is the one rule exempted, or empty for all of them.
	Rule prose.Rule `json:"rule"`

	// Reason says why the file is exempt.
	Reason string `json:"reason"`
}

// New returns the analyzer configured by opts, erroring when it would judge
// nothing, on a malformed call, field or glob, on a rule that is no message
// rule, and on an allow entry with no reason or no file it matches.
func New(opts Options) (*analysis.Analyzer, error) {
	if len(opts.Calls) == 0 && len(opts.Fields) == 0 && len(opts.Prefixes) == 0 && !opts.Slog {
		return nil, errors.New("diagmsg: calls, fields and prefixes are all empty and slog is off: the rule judges nothing")
	}
	if opts.MaxRunes < 0 {
		return nil, fmt.Errorf("diagmsg: max-runes %d is negative", opts.MaxRunes)
	}
	for _, c := range opts.Calls {
		if !strings.Contains(c.Func, ".") || c.Arg < 0 {
			return nil, fmt.Errorf("diagmsg: call %+v needs a qualified func and an argument index", c)
		}
	}
	for _, f := range opts.Fields {
		if !strings.Contains(f.Type, ".") || f.Field == "" {
			return nil, fmt.Errorf("diagmsg: field %+v needs a qualified type and a field name", f)
		}
	}
	if slices.Contains(opts.Prefixes, "") {
		return nil, errors.New("diagmsg: an empty prefix would mark every string")
	}
	messageRules := prose.KindRules(prose.KindMessage)
	for _, r := range opts.Rules {
		if !slices.Contains(messageRules, r) {
			return nil, fmt.Errorf("diagmsg: rule %q is not one of %q", r, messageRules)
		}
	}
	allowed := make(source.Globs, 0, len(opts.Allow))
	for _, a := range opts.Allow {
		if a.File == "" || a.Reason == "" {
			return nil, fmt.Errorf("diagmsg: allow entry %+v needs both file and reason", a)
		}
		if a.Rule != "" && !slices.Contains(messageRules, a.Rule) {
			return nil, fmt.Errorf("diagmsg: allow entry %+v names no message rule", a)
		}
		allowed = append(allowed, a.File)
	}
	if err := opts.Files.Validate("diagmsg"); err != nil {
		return nil, err
	}
	if err := allowed.Validate("diagmsg"); err != nil {
		return nil, err
	}
	if err := source.InModule("diagmsg", opts.Module, func(root string) error {
		if err := opts.Files.RequireMatches("diagmsg", "files", root); err != nil {
			return err
		}
		return allowed.RequireMatches("diagmsg", "allow", root)
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "diagmsg",
		Doc:  "judge the messages a program prints by the prose message rules",
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
		j := judge{pass: pass, opts: opts, rel: rel, seen: map[token.Pos]bool{}}
		j.sites(f)
		if len(opts.Prefixes) > 0 {
			j.prefixed(f)
		}
	}
	return nil
}

type judge struct {
	pass *analysis.Pass
	opts Options
	rel  string
	// seen holds the messages already judged, so a prefixed message built at a
	// configured site is reported once.
	seen map[token.Pos]bool
}

// sites judges every message passed to a configured call or set on a
// configured field.
func (j *judge) sites(f *ast.File) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			fn, ok := typeutil.Callee(j.pass.TypesInfo, n).(*types.Func)
			if !ok {
				return true
			}
			for _, c := range j.opts.Calls {
				if fn.FullName() == c.Func && c.Arg < len(n.Args) {
					j.message(n.Args[c.Arg], c.Format)
				}
			}
			if arg, ok := slogMessages[fn.FullName()]; ok && j.opts.Slog && arg < len(n.Args) {
				if text, pos, ok := j.text(n.Args[arg], false); ok {
					j.report(text, pos, true)
				}
			}
		case *ast.CompositeLit:
			for _, elt := range n.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && j.field(j.pass.TypesInfo.Uses[key]) {
					j.message(kv.Value, false)
				}
			}
		case *ast.AssignStmt:
			if len(n.Lhs) != len(n.Rhs) {
				return true
			}
			for i, lhs := range n.Lhs {
				if sel, ok := lhs.(*ast.SelectorExpr); ok && j.field(j.pass.TypesInfo.Uses[sel.Sel]) {
					j.message(n.Rhs[i], false)
				}
			}
		}
		return true
	})
}

// slogMessages maps each log/slog call [Options.Slog] judges to its message
// argument's index.
var slogMessages = func() map[string]int {
	m := map[string]int{}
	for _, level := range []string{"Debug", "Info", "Warn", "Error"} {
		for _, owner := range []string{"log/slog.", "(*log/slog.Logger)."} {
			m[owner+level] = 0
			m[owner+level+"Context"] = 1
		}
	}
	return m
}()

// field reports whether obj is one of the configured fields.
func (j *judge) field(obj types.Object) bool {
	v, ok := obj.(*types.Var)
	if !ok || !v.IsField() || v.Pkg() == nil {
		return false
	}
	for _, f := range j.opts.Fields {
		dot := strings.LastIndex(f.Type, ".")
		if v.Name() == f.Field && v.Pkg().Path() == f.Type[:dot] && fieldOwner(v) == f.Type[dot+1:] {
			return true
		}
	}
	return false
}

// fieldOwner names the package-level struct type declaring v, or "".
func fieldOwner(v *types.Var) string {
	scope := v.Pkg().Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		st, ok := tn.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := range st.NumFields() {
			if st.Field(i) == v {
				return name
			}
		}
	}
	return ""
}

// prefixed judges every string expression opening with a configured prefix.
// A concatenation is judged whole, from its outermost +.
func (j *judge) prefixed(f *ast.File) {
	formats := map[ast.Expr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && len(call.Args) > 0 && j.formatter(call) {
			formats[ast.Unparen(call.Args[0])] = true
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		e, ok := n.(ast.Expr)
		if !ok || !j.stringExpr(e) {
			return true
		}
		if _, isLit := e.(*ast.BasicLit); !isLit {
			if bin, isBin := e.(*ast.BinaryExpr); !isBin || bin.Op != token.ADD {
				return true
			}
		}
		text, pos, ok := j.text(e, formats[e])
		if ok && slices.ContainsFunc(j.opts.Prefixes, func(p string) bool { return strings.HasPrefix(text, p) }) {
			j.report(text, pos, false)
		}
		return false
	})
}

func (j *judge) stringExpr(e ast.Expr) bool {
	tv, ok := j.pass.TypesInfo.Types[e]
	if !ok {
		return false
	}
	b, ok := tv.Type.Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// formatter reports whether call is a fmt function taking a format first.
func (j *judge) formatter(call *ast.CallExpr) bool {
	fn, ok := typeutil.Callee(j.pass.TypesInfo, call).(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "fmt" {
		return false
	}
	switch fn.Name() {
	case "Sprintf", "Errorf":
		return true
	}
	return false
}

// message judges e when it resolves to text.
func (j *judge) message(e ast.Expr, format bool) {
	if text, pos, ok := j.text(e, format); ok {
		j.report(text, pos, false)
	}
}

// opaque stands in for a format verb or an operand only known at run time.
const opaque = "%"

// verb matches one fmt verb, flags, width, precision and index included.
var verb = regexp.MustCompile(`%[-+# 0]*(?:\[\d+\])?(?:\d+|\*)?(?:\.(?:\d+|\*)?)?(?:\[\d+\])?[a-zA-Z%]`)

// text resolves e to the message it builds and the position of its first
// literal, or reports false when no part of it is known. A call to
// fmt.Sprintf or fmt.Errorf resolves to its format, and errors.New to its
// argument.
func (j *judge) text(e ast.Expr, format bool) (string, token.Pos, bool) {
	e = ast.Unparen(e)
	if tv, ok := j.pass.TypesInfo.Types[e]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		s := constant.StringVal(tv.Value)
		if format {
			s = verb.ReplaceAllString(s, opaque)
		}
		return s, e.Pos(), true
	}
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", token.NoPos, false
		}
		left, lpos, lok := j.text(e.X, format)
		right, rpos, rok := j.text(e.Y, format)
		switch {
		case lok && rok:
			return left + right, lpos, true
		case lok:
			return left + opaque, lpos, true
		case rok:
			return opaque + right, rpos, true
		}
	case *ast.CallExpr:
		if len(e.Args) == 0 {
			return "", token.NoPos, false
		}
		if j.formatter(e) {
			return j.text(e.Args[0], true)
		}
		if fn, ok := typeutil.Callee(j.pass.TypesInfo, e).(*types.Func); ok && fn.FullName() == "errors.New" {
			return j.text(e.Args[0], false)
		}
	}
	return "", token.NoPos, false
}

// report judges text by every rule, or a log/slog message by the tag rule
// alone and past every allow entry.
func (j *judge) report(text string, pos token.Pos, slogMessage bool) {
	if j.seen[pos] {
		return
	}
	j.seen[pos] = true
	findings := prose.JudgeMessage(text, j.opts.MaxRunes)
	if tag := j.phraseTag(text); tag != "" {
		findings = append(findings, prose.Finding{
			Rule:    prose.RuleMessageTag,
			Message: fmt.Sprintf("Drop the leading '%s' tag: open with the verdict.", tag),
			Match:   tag,
			Line:    1,
		})
	}
	for _, f := range findings {
		if len(j.opts.Rules) > 0 && !slices.Contains(j.opts.Rules, f.Rule) {
			continue
		}
		if (slogMessage && f.Rule != prose.RuleMessageTag) || (!slogMessage && j.allowed(f.Rule)) {
			continue
		}
		j.pass.Report(analysis.Diagnostic{
			Pos:      pos,
			Category: string(f.Rule),
			Message:  source.Hint(fmt.Sprintf("%s: %s", f.Rule, strings.TrimSuffix(f.Message, ".")), j.opts.Hint),
		})
	}
}

// phraseTagPattern matches two or three lowercase words and a colon opening a
// message, such as "diff session: " or "server check-drift: ". The prose
// rule has the one-word form.
var phraseTagPattern = regexp.MustCompile(`^\s*([a-z][a-z0-9-]*(?: [a-z][a-z0-9-]*){1,2}): `)

// sentenceWords are words a component tag never holds, so a clause such as
// "cannot open the file: " or "unknown flag: " opening with one is a
// sentence chaining context, never a speaker's label. A word ending in "ed"
// or "ing", or an irregular past form such as "rebuilt", is a verb form for
// the same reason.
var sentenceWords = map[string]bool{
	"a": true, "an": true, "the": true, "this": true, "that": true, "these": true, "those": true,
	"to": true, "of": true, "in": true, "on": true, "at": true, "for": true, "from": true,
	"with": true, "by": true, "into": true, "and": true, "or": true, "but": true, "as": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "has": true, "have": true,
	"no": true, "not": true, "cannot": true, "can't": true, "could": true, "would": true,
	"should": true, "must": true, "unable": true, "unknown": true, "invalid": true,
	"missing": true, "bad": true, "unsupported": true, "unexpected": true, "error": true,
	"errors": true, "failure": true, "timeout": true, "too": true,
	"built": true, "rebuilt": true, "found": true, "sent": true, "kept": true, "made": true,
	"lost": true, "done": true, "written": true, "wrote": true, "ran": true,
}

// phraseTag returns the leading multi-word component tag of text, with its
// colon, or "". A message opening with one of [Options.Prefixes] is labeled
// on purpose, and a clause holding a sentence word is a sentence.
func (j *judge) phraseTag(text string) string {
	m := phraseTagPattern.FindStringSubmatch(text)
	if m == nil || slices.ContainsFunc(j.opts.Prefixes, func(p string) bool { return strings.HasPrefix(strings.TrimSpace(text), p) }) {
		return ""
	}
	for _, w := range strings.Fields(m[1]) {
		if sentenceWords[w] || strings.HasSuffix(w, "ed") || strings.HasSuffix(w, "ing") {
			return ""
		}
	}
	return m[1] + ":"
}

func (j *judge) allowed(rule prose.Rule) bool {
	for _, a := range j.opts.Allow {
		if (a.Rule == "" || a.Rule == rule) && source.Globs([]string{a.File}).Match(j.rel) {
			return true
		}
	}
	return false
}
