package coldread

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/tools/go/analysis"
)

const (
	restateMessage = "restate: comment only restates the next line; delete it, or say what the line cannot"
	stepsMessage   = "steps: comment narrates a numbered step; name the phase with a function, " +
		"or state the ordering constraint it protects"
	historyMessage = "history: comment narrates a change (%q); describe the code as it stands " +
		"and leave its history to the commit message"
	docstubMessage = "docstub: doc comment only repeats the name %s; state the contract a caller " +
		"relies on (edge cases, errors, ownership), or keep it to what the name cannot say"
	commentedCodeMessage = "commentedcode: comment holds commented-out Go; delete it, version control keeps it"
)

// restateMaxWords caps restate at the comments short enough to be pure labels.
// A longer comment made only of the next line's words is rare, and when it
// happens it is usually a sentence that says something the tokens do not.
const restateMaxWords = 5

// checkIntent runs every check but aside over one file.
func (l linter) checkIntent(pass *analysis.Pass, f *ast.File, tf *token.File) {
	docs := declDocs(f)
	bodies := funcBodies(f)

	// Without the source, restate and commentedcode cannot tell a comment on its
	// own line from a trailing one and stay silent. A read that disagrees with
	// the file set's size is treated the same way, so offsets never overrun.
	var src []byte
	if pass.ReadFile != nil {
		if b, err := pass.ReadFile(tf.Name()); err == nil && len(b) == tf.Size() {
			src = b
		}
	}

	report := func(pos token.Pos, c Check, message string) {
		pass.Report(analysis.Diagnostic{Pos: pos, Category: string(c), Message: message})
	}

	numbered := map[*ast.BlockStmt][]*ast.CommentGroup{}

	for _, group := range f.Comments {
		if marked(group) {
			continue
		}

		sym, isDoc := docs[group]

		if isDoc && l.enabled[CheckDocstub] && docStub(group, sym) {
			report(group.Pos(), CheckDocstub, fmt.Sprintf(docstubMessage, sym.name))
		}

		own, next := placement(group, tf, src)

		if !isDoc && own && l.enabled[CheckRestate] && restates(group, next) {
			report(group.Pos(), CheckRestate, restateMessage)
		}

		// A comment over a line ending in a comma labels an element of a table or
		// an argument list, and a label numbered "1." is a list, not a sequence.
		if body := enclosing(bodies, group); body != nil && l.enabled[CheckSteps] && !strings.HasSuffix(next, ",") {
			switch stepShape(group) {
			case stepNamed:
				report(group.Pos(), CheckSteps, stepsMessage)
			case stepNumbered:
				numbered[body] = append(numbered[body], group)
			case stepNone:
			}
		}

		if l.enabled[CheckHistory] {
			for _, ln := range proseLines(group) {
				if at, phrase := historyPhrase(ln.text); at >= 0 {
					report(ln.pos+token.Pos(at), CheckHistory, fmt.Sprintf(historyMessage, phrase))
				}
			}
		}

		// A trailing comment is an annotation on its line, and there code-shaped
		// text ("field!=value", "CUP: row;col") is the point.
		if own && l.enabled[CheckCommentedCode] {
			if pos, ok := commentedCode(group); ok {
				report(pos, CheckCommentedCode, commentedCodeMessage)
			}
		}
	}

	// One numbered comment is a list; two in one function sequence its body.
	for _, groups := range numbered {
		if len(groups) < 2 {
			continue
		}

		for _, group := range groups {
			report(group.Pos(), CheckSteps, stepsMessage)
		}
	}
}

// markers open a line no intent check reports. Each carries meaning a person or
// tool acts on, which is the opposite of noise.
var markers = []string{"TODO", "FIXME", "BUG", "compat(until:", "compat:", "Deprecated:"}

// marked reports whether any line of group opens with a marker. The whole group
// is skipped, since a marker's explanation often continues on the lines below it.
func marked(group *ast.CommentGroup) bool {
	for _, ln := range groupLines(group) {
		text := strings.TrimSpace(ln.text)
		for _, m := range markers {
			if !strings.HasPrefix(text, m) {
				continue
			}

			// TODOS or BUGGY is a word, not the marker.
			rest := text[len(m):]
			if strings.HasSuffix(m, ":") || rest == "" || !unicode.IsLetter(rune(rest[0])) {
				return true
			}
		}
	}

	return false
}

// symbol is what a declaration doc comment documents.
type symbol struct {
	name string
	recv string
}

// declDocs maps each declaration doc comment to the symbol it documents. Field
// docs are left out: restate judges those, since a field's doc sits directly
// above the field the way a body comment sits above its line.
func declDocs(f *ast.File) map[*ast.CommentGroup]symbol {
	docs := map[*ast.CommentGroup]symbol{}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Doc != nil {
				docs[d.Doc] = symbol{name: d.Name.Name, recv: recvName(d)}
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				name, doc := specDoc(spec)
				if doc == nil && len(d.Specs) == 1 {
					doc = d.Doc
				}

				// A spec naming several values still carries a doc, so it stays out
				// of restate even though docstub has no one name to compare.
				if doc != nil {
					docs[doc] = symbol{name: name}
				}
			}

			// A grouped block's doc describes the group, never one name, so it is
			// still a declaration doc but has no symbol to stub.
			if d.Doc != nil && len(d.Specs) != 1 {
				docs[d.Doc] = symbol{}
			}
		}
	}

	return docs
}

func specDoc(spec ast.Spec) (string, *ast.CommentGroup) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Name.Name, s.Doc
	case *ast.ValueSpec:
		if len(s.Names) == 1 {
			return s.Names[0].Name, s.Doc
		}

		return "", s.Doc
	case *ast.ImportSpec:
		return "", s.Doc
	}

	return "", nil
}

func recvName(d *ast.FuncDecl) string {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return ""
	}

	t := d.Recv.List[0].Type
	for {
		switch e := t.(type) {
		case *ast.StarExpr:
			t = e.X
		case *ast.IndexExpr:
			t = e.X
		case *ast.IndexListExpr:
			t = e.X
		case *ast.Ident:
			return e.Name
		default:
			return ""
		}
	}
}

// funcBodies returns the outermost function bodies, so numbered comments count
// per function however deeply they nest in closures.
func funcBodies(f *ast.File) []*ast.BlockStmt {
	var out []*ast.BlockStmt

	ast.Inspect(f, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				out = append(out, fn.Body)
			}

			return false
		case *ast.FuncLit:
			out = append(out, fn.Body)

			return false
		}

		return true
	})

	return out
}

func enclosing(bodies []*ast.BlockStmt, group *ast.CommentGroup) *ast.BlockStmt {
	for _, b := range bodies {
		if b.Lbrace < group.Pos() && group.End() <= b.Rbrace {
			return b
		}
	}

	return nil
}

// docStub reports whether a one-line doc comment says nothing but the symbol's
// name and stub vocabulary, the shape gocritic's docStub targets:
// "Foo is a Foo", "NewFoo creates a new Foo", "Foo ...".
func docStub(group *ast.CommentGroup, sym symbol) bool {
	if sym.name == "" {
		return false
	}

	text := strings.TrimSpace(group.Text())
	if text == "" || strings.Contains(text, "\n") {
		return false
	}

	known := map[string]bool{}
	for _, w := range identWords(sym.name) {
		known[w] = true
	}

	for _, w := range identWords(sym.recv) {
		known[w] = true
	}

	for _, w := range words(text) {
		if !known[w] && !stubWords[w] && !stopWords[w] {
			return false
		}
	}

	return true
}

// stubWords is the vocabulary a stub pads a name with, stemmed. "implement" is
// absent on purpose: "String implements fmt.Stringer" names the contract.
var stubWords = setOf("return", "create", "new", "instance", "function", "func", "method",
	"type", "struct", "constructor", "represent", "define", "get", "set", "value", "object",
	"variable", "constant", "const", "var", "given", "specified", "provided", "construct",
	"build", "make", "initialize", "init")

var stopWords = setOf("a", "an", "the", "this", "that", "these", "those", "to", "of", "for",
	"in", "on", "at", "by", "with", "from", "into", "onto", "and", "or", "is", "are", "be",
	"it", "its", "as", "we", "our", "then", "here", "there", "all", "each", "every", "if",
	"so", "any", "some", "just")

// placement reports whether group starts on a line of its own, and the trimmed
// text of the line after it ends. src is tf's content; without it placement
// reports false and "".
func placement(group *ast.CommentGroup, tf *token.File, src []byte) (own bool, next string) {
	if src == nil {
		return false, ""
	}

	lead := src[tf.Offset(tf.LineStart(tf.Line(group.Pos()))):tf.Offset(group.Pos())]
	own = strings.TrimSpace(string(lead)) == ""

	last := tf.Line(group.End())
	if last+1 > tf.LineCount() {
		return own, ""
	}

	return own, lineText(tf, src, last+1)
}

// restates reports whether group is a single comment line whose every word
// already appears in next, the code line below it, and at least one of them as
// an identifier rather than only a keyword.
//
// A next line ending in a comma is left alone: the comment then labels a run of
// table rows or arguments ("// Reads." over a block of cases), not one line.
func restates(group *ast.CommentGroup, next string) bool {
	if len(group.List) != 1 || !strings.HasPrefix(group.List[0].Text, "//") ||
		isDirective(group.List[0].Text[2:]) {
		return false
	}

	switch {
	case next == "", strings.HasPrefix(next, "//"), strings.HasPrefix(next, "/*"),
		next == "}", next == ")", next == "{", strings.HasSuffix(next, ","):
		return false
	}

	comment := words(group.List[0].Text[2:])
	if len(comment) == 0 || len(comment) > restateMaxWords {
		return false
	}

	idents, keywords := codeWords(next)

	named := false

	for _, w := range comment {
		switch {
		case idents[w]:
			named = true
		case keywords[w]:
		default:
			return false
		}
	}

	return named
}

func lineText(tf *token.File, src []byte, n int) string {
	end := len(src)
	if n+1 <= tf.LineCount() {
		end = tf.Offset(tf.LineStart(n + 1))
	}

	return strings.TrimSpace(string(src[tf.Offset(tf.LineStart(n)):end]))
}

// codeWords splits a line of Go into the words its identifiers and literals
// spell, and separately the keywords and the verbs its operators read as
// ("increment" for ++), which alone never make a restatement.
func codeWords(code string) (idents, keywords map[string]bool) {
	idents, keywords = map[string]bool{}, map[string]bool{}

	fset := token.NewFileSet()

	var s scanner.Scanner
	s.Init(fset.AddFile("", -1, len(code)), []byte(code), nil, 0)

	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return idents, keywords
		}

		switch {
		case tok.IsKeyword():
			keywords[tok.String()] = true
		case tok == token.INC:
			keywords["increment"] = true
		case tok == token.DEC:
			keywords["decrement"] = true
		case tok == token.DEFINE, tok == token.ASSIGN:
			keywords["set"], keywords["assign"] = true, true
		case tok == token.IDENT, tok == token.STRING, tok == token.CHAR, tok == token.INT:
			for _, w := range words(lit) {
				idents[w] = true
			}
		}
	}
}

// words splits text into lowercased, stemmed words with the stop words
// dropped, splitting identifiers written in prose the way identWords does.
func words(text string) []string {
	var out []string

	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		for _, w := range identWords(field) {
			if !stopWords[w] {
				out = append(out, w)
			}
		}
	}

	return out
}

// identWords splits an identifier at case and underscore boundaries, keeping an
// acronym whole: HTTPServer is http and server. The words come back stemmed.
func identWords(ident string) []string {
	var out []string

	runes := []rune(ident)
	start := 0

	flush := func(end int) {
		if end > start {
			out = append(out, stem(strings.ToLower(string(runes[start:end]))))
		}

		start = end
	}

	for i, r := range runes {
		switch {
		case r == '_':
			flush(i)
			start = i + 1
		case i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]):
			flush(i)
		case i > 0 && i+1 < len(runes) && unicode.IsUpper(r) && unicode.IsUpper(runes[i-1]) &&
			unicode.IsLower(runes[i+1]):
			flush(i)
		}
	}

	flush(len(runes))

	return out
}

// stem folds a plural or third-person s, which is all restate and docstub need
// to match "creates" to create and "users" to user.
func stem(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}

	return w
}

type stepKind int

const (
	stepNone stepKind = iota
	stepNamed
	stepNumbered
)

// stepShape classifies group by its first non-blank line. A numbered line is
// one go/doc/comment would read as a numbered list item.
func stepShape(group *ast.CommentGroup) stepKind {
	for _, ln := range groupLines(group) {
		text := strings.TrimSpace(ln.text)
		switch {
		case text == "":
			continue
		case namedStep(text):
			return stepNamed
		case isDigit(text[0]) && listMarker(text) > 0:
			return stepNumbered
		}

		return stepNone
	}

	return stepNone
}

// namedStep reports whether text opens with "step" and a whole number, in any
// case and with or without a space: "Step 1:", "step2". "step 1a" is a label.
func namedStep(text string) bool {
	if len(text) < len("step") || !strings.EqualFold(text[:len("step")], "step") {
		return false
	}

	rest := strings.TrimLeft(text[len("step"):], " \t")

	n := 0
	for n < len(rest) && isDigit(rest[n]) {
		n++
	}

	if n == 0 {
		return false
	}

	if n == len(rest) {
		return true
	}

	b := rest[n]

	return b != '_' && !('a' <= b && b <= 'z') && !('A' <= b && b <= 'Z')
}

// historyPatterns are phrases that only make sense against the previous version
// of the code. Bare "now", "fixed", "instead of" and "correctly" are left out:
// over this repo most of their hits describe the code as it stands, "instead of"
// above all, which is how a comment names the alternative it rejected.
var historyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bused to\b`),
	regexp.MustCompile(`(?i)\bpreviously\b`),
	regexp.MustCompile(`(?i)\bnow (?:correctly|properly)\b`),
	regexp.MustCompile(`(?i)\bfixe[sd] (?:a|the) bug\b`),
	regexp.MustCompile(`(?i)\bbug ?fix\b`),
	regexp.MustCompile(`(?i)\bthe old (?:code|behaviou?r|implementation|version)\b`),
}

// purposeLead is the word before a "used to" that states a purpose ("is used to
// sign") rather than a past habit ("it used to sign").
var purposeLead = setOf("is", "are", "was", "were", "be", "been", "being", "get", "gets", "got", "getting")

// historyPhrase returns the offset and text of the first history phrase in text,
// or -1. Backtick spans are literals and never match.
func historyPhrase(text string) (int, string) {
	masked := blankBackticks(text)

	best, phrase := -1, ""

	for _, re := range historyPatterns {
		for _, m := range re.FindAllStringIndex(masked, -1) {
			if strings.EqualFold(masked[m[0]:m[1]], "used to") && purposeLead[prevWord(masked, m[0])] {
				continue
			}

			if best < 0 || m[0] < best {
				best, phrase = m[0], masked[m[0]:m[1]]
			}

			break
		}
	}

	return best, phrase
}

func prevWord(s string, at int) string {
	end := at
	for end > 0 && s[end-1] == ' ' {
		end--
	}

	start := end
	for start > 0 && unicode.IsLetter(rune(s[start-1])) {
		start--
	}

	return strings.ToLower(s[start:end])
}

// hasCodeSignal reports whether code holds a token prose almost never carries:
// an operator such as := or ==, a brace, an explicit semicolon, a leading
// return, or a selector call like fmt.Println(. Requiring one keeps a line like
// "Retry (bounded)", which parses as a call, from reading as code.
func hasCodeSignal(code string) bool {
	fset := token.NewFileSet()

	var s scanner.Scanner
	s.Init(fset.AddFile("", -1, len(code)), []byte(code), nil, 0)

	// The last three tokens, for the selector call shape.
	var prev [3]token.Token

	for first := true; ; first = false {
		_, tok, lit := s.Scan()

		switch tok {
		case token.EOF:
			return false
		case token.DEFINE, token.NEQ, token.EQL, token.LAND, token.LOR, token.INC,
			token.LBRACE, token.RBRACE:
			return true
		case token.SEMICOLON:
			// An automatically inserted semicolon reads as "\n".
			if lit == ";" {
				return true
			}
		case token.RETURN:
			if first {
				return true
			}
		case token.LPAREN:
			if prev == [3]token.Token{token.IDENT, token.PERIOD, token.IDENT} {
				return true
			}
		}

		prev = [3]token.Token{prev[1], prev[2], tok}
	}
}

// commentedCode returns the position of the first run of lines in group that
// parses as Go statements or declarations. A run whose first line is indented
// is a preformatted example, which godoc renders on purpose, and is skipped,
// as is an example function's Output block.
func commentedCode(group *ast.CommentGroup) (token.Pos, bool) {
	lines := groupLines(group)

	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i].text) == "" {
			i++

			continue
		}

		j := i
		for j < len(lines) && strings.TrimSpace(lines[j].text) != "" {
			j++
		}

		run := lines[i:j]
		i = j

		first := run[0].text
		if strings.HasPrefix(first, " ") || strings.HasPrefix(first, "\t") ||
			strings.HasPrefix(first, "Output:") || strings.HasPrefix(first, "Unordered output:") {
			continue
		}

		texts := make([]string, len(run))
		for k, ln := range run {
			texts[k] = ln.text
		}

		code := strings.Join(texts, "\n")
		if hasCodeSignal(code) && parsesAsGo(code) {
			return run[0].pos, true
		}
	}

	return token.NoPos, false
}

func parsesAsGo(code string) bool {
	fset := token.NewFileSet()

	if _, err := parser.ParseFile(fset, "", "package p\nfunc _() {\n"+code+"\n}\n", parser.SkipObjectResolution); err == nil {
		return true
	}

	_, err := parser.ParseFile(fset, "", "package p\n"+code+"\n", parser.SkipObjectResolution)

	return err == nil
}

func setOf(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}

	return m
}
