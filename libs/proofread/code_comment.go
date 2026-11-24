package proofread

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

const (
	// RuleCodeCommentBudget reports a code comment block over the words or
	// prose lines its place earns.
	RuleCodeCommentBudget Rule = "code-comment-budget"
	// RuleCodeCommentSentence reports a code comment sentence over the
	// sentence budget.
	RuleCodeCommentSentence Rule = "code-comment-sentence"
	// RuleBanner reports a banner line in a code comment.
	RuleBanner Rule = "banner"
	// RuleCommentHistory reports a code comment that narrates a change. It is
	// history with the stylesheet phrases added, kept apart because it denies
	// by default where history ships off.
	RuleCommentHistory Rule = "comment-history"
	// RuleProvenance reports a date, a pull request or issue number, a commit
	// hash or a retold failure in a code comment.
	RuleProvenance Rule = "provenance"
	// RuleDocPath reports a code comment that cites a design page, a decision
	// record or a Markdown file.
	RuleDocPath Rule = "doc-path"
	// RuleCommentCaps reports an ordinary word written in capitals for
	// emphasis in a code comment.
	RuleCommentCaps Rule = "comment-caps"
	// RuleCommentFirstPerson reports a first-person pronoun in a code comment.
	RuleCommentFirstPerson Rule = "comment-first-person"
)

var codeCommentChecks = []check{
	{rule: RuleCodeCommentBudget, reads: []Format{FormatCode}, judge: codeCommentBudget},
	{rule: RuleCodeCommentSentence, reads: []Format{FormatCode}, judge: codeCommentSentence},
	{rule: RuleBanner, reads: []Format{FormatCode}, judge: banner},
	{rule: RuleCommentHistory, reads: []Format{FormatCode}, judge: commentHistory},
	{rule: RuleProvenance, reads: []Format{FormatCode}, judge: provenance},
	{rule: RuleDocPath, reads: []Format{FormatCode}, judge: docPath},
	{rule: RuleCommentCaps, reads: []Format{FormatCode}, judge: commentCaps},
	{rule: RuleCommentFirstPerson, reads: []Format{FormatCode}, judge: commentFirstPerson},
}

var codeCommentTexts = map[Rule]ruleText{
	RuleCodeCommentBudget: {
		code:      "PRF6010",
		dimension: DimensionEconomy,
		catches:   "a code comment block over the words or prose lines its place earns",
		why: "House style: magus's exported docs ran to 83 words at p90 against 74 in the Go standard " +
			"library. A trailing comment earns 12 words on 1 line, one inside a body 30 on 3, a doc on an " +
			"unexported declaration 40 on 4, and an exported doc or a file header 60 on 8. Blank lines, " +
			"code, tables, URLs and directives count for nothing. A project may only tighten them.",
	},
	RuleCodeCommentSentence: {
		code:      "PRF6011",
		dimension: DimensionEconomy,
		catches:   "a code comment sentence over 25 words",
		why: "House style: 29% of magus's exported docs held a sentence over 25 words, against 11.6% in the " +
			"Go standard library. The excess was sentence length, not the number of sentences.",
	},
	RuleBanner: {
		code:      "PRF6012",
		dimension: DimensionStructure,
		catches:   "a banner line of dashes, equals signs or slashes in a code comment",
		why: "House style: a banner frames a section, and a file that wants sections wants splitting. " +
			"It costs a line and says nothing.",
	},
	RuleCommentHistory: {
		code:      "PRF6013",
		dimension: DimensionEconomy,
		catches:   "a code comment that narrates a change rather than the code",
		why: "House style: a comment that says what the code replaced, or what it was before, only makes " +
			"sense against the diff. The history rule's phrases and the stylesheet ones both count here.",
	},
	RuleProvenance: {
		code:      "PRF5012",
		dimension: DimensionConventions,
		catches:   "a date, a pull request or issue number, a commit hash or an incident in a code comment",
		why: "House style: where the code came from is the log's to say. A date or a number ages the " +
			"moment it is written; a measurement of what the code does stays.",
	},
	RuleDocPath: {
		code:      "PRF5013",
		dimension: DimensionConventions,
		catches:   "a code comment citing docs/design, docs/decisions, an ADR number or a Markdown file",
		why: "House style: a comment that sends the reader to a design page argues the change instead " +
			"of stating the code. A Go doc link to a symbol stays.",
	},
	RuleCommentCaps: {
		code:      "PRF5014",
		dimension: DimensionConventions,
		catches:   "an ordinary word in capitals for emphasis in a code comment",
		why: "House style: SHOUTING a word adds no fact. An acronym and a name the file's code spells " +
			"pass.",
	},
	RuleCommentFirstPerson: {
		code:      "PRF5015",
		dimension: DimensionStance,
		catches:   "I, me, my, we, us or our in a code comment",
		why:       "House style: a comment states what the code does and why, not who chose it or how it felt.",
	},
}

// CommentBudget is what one comment block may hold at one place: words of
// prose, and prose lines.
type CommentBudget struct {
	Words int `json:"words"`
	Lines int `json:"lines"`
}

// CommentBudgets are the budgets [KindCodeComment] holds comments to, by
// place, and the longest sentence any comment may hold.
type CommentBudgets struct {
	Trailing    CommentBudget
	Inline      CommentBudget
	Doc         CommentBudget
	ExportedDoc CommentBudget
	Header      CommentBudget
	Sentence    int
}

// StrictCommentBudgets returns the budgets proofread ships, which a project
// may only tighten.
func StrictCommentBudgets() CommentBudgets {
	return CommentBudgets{
		Trailing:    CommentBudget{Words: 12, Lines: 1},
		Inline:      CommentBudget{Words: 30, Lines: 3},
		Doc:         CommentBudget{Words: 40, Lines: 4},
		ExportedDoc: CommentBudget{Words: 60, Lines: 8},
		Header:      CommentBudget{Words: 60, Lines: 8},
		Sentence:    25,
	}
}

func (b CommentBudgets) at(p codePlace) CommentBudget {
	switch p {
	case codeTrailing:
		return b.Trailing
	case codeInline:
		return b.Inline
	case codeDoc:
		return b.Doc
	case codeExportedDoc:
		return b.ExportedDoc
	case codeHeader:
		return b.Header
	}

	return b.Inline
}

// ParseCommentBudgets reads a JSON object that tightens the strict budgets.
// A place key (trailing, inline, doc, exported-doc or header) holds words and
// lines; sentence holds one number. A key left out keeps its strict value. A
// value under 1, a value over its strict one or an unknown key is an error.
func ParseCommentBudgets(data []byte) (CommentBudgets, error) {
	var in struct {
		Trailing    *CommentBudget `json:"trailing"`
		Inline      *CommentBudget `json:"inline"`
		Doc         *CommentBudget `json:"doc"`
		ExportedDoc *CommentBudget `json:"exported-doc"`
		Header      *CommentBudget `json:"header"`
		Sentence    *int           `json:"sentence"`
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&in); err != nil {
		return CommentBudgets{}, fmt.Errorf("read comment budgets: %w", err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return CommentBudgets{}, errors.New("read comment budgets: data after the object")
	}

	out := StrictCommentBudgets()

	var errs []error

	tighten := func(name string, set *CommentBudget, strict *CommentBudget) {
		if set == nil {
			return
		}

		if set.Words != 0 {
			errs = append(errs, tightened(name+".words", set.Words, strict.Words))
			strict.Words = set.Words
		}

		if set.Lines != 0 {
			errs = append(errs, tightened(name+".lines", set.Lines, strict.Lines))
			strict.Lines = set.Lines
		}
	}

	tighten("trailing", in.Trailing, &out.Trailing)
	tighten("inline", in.Inline, &out.Inline)
	tighten("doc", in.Doc, &out.Doc)
	tighten("exported-doc", in.ExportedDoc, &out.ExportedDoc)
	tighten("header", in.Header, &out.Header)

	if in.Sentence != nil {
		errs = append(errs, tightened("sentence", *in.Sentence, out.Sentence))
		out.Sentence = *in.Sentence
	}

	if err := errors.Join(errs...); err != nil {
		return CommentBudgets{}, err
	}

	return out, nil
}

func tightened(name string, set, strict int) error {
	switch {
	case set < 1:
		return fmt.Errorf("comment budget %s is %d: want 1 or more", name, set)
	case set > strict:
		return fmt.Errorf("comment budget %s is %d, over the strict %d: a project may only tighten it", name, set, strict)
	}

	return nil
}

// WithCommentBudgets holds [KindCodeComment] to b in place of
// [StrictCommentBudgets]. A caller reading b from a user parses it with
// [ParseCommentBudgets], which refuses a loosened budget.
func WithCommentBudgets(b CommentBudgets) Option { return func(o *options) { o.budgets = &b } }

// WithLanguage names the language of the source [KindCodeComment] judges.
// Without it that kind judges nothing, since no comment can be found.
func WithLanguage(l SourceLanguage) Option { return func(o *options) { o.language = l } }

func (o options) commentBudgets() CommentBudgets {
	if o.budgets != nil {
		return *o.budgets
	}

	return StrictCommentBudgets()
}

// judgeCode judges the comment blocks of src by the rules for
// [KindCodeComment]. Findings come in [Rules] order, then in line order, each
// with its line in src.
func judgeCode(src string, s *KindSpec, o options) []Finding {
	if o.language == "" {
		return nil
	}

	var out []Finding

	for _, c := range readCode(src, o.language).comments {
		out = append(out, codeFindings(c, s, o)...)
	}

	order := Rules()
	slices.SortStableFunc(out, func(a, b Finding) int {
		if d := slices.Index(order, a.Rule) - slices.Index(order, b.Rule); d != 0 {
			return d
		}

		return a.Line - b.Line
	})

	return out
}

// codeFindings judges one block and moves each finding to its source line.
// The block is judged with its delimiters gone, so no column is the file's.
func codeFindings(c codeComment, s *KindSpec, o options) []Finding {
	lines := strings.Split(c.text, "\n")

	out := run(input{
		symbol: Symbol{Doc: c.text},
		spec:   s,
		prose:  readProse(lines, false),
		lines:  lines,
		source: lines,
		opts:   o,
		code:   &c,
	})

	for i := range out {
		if out[i].Line == 0 {
			out[i].Line = c.start
		} else {
			out[i].Line += c.start - 1
		}

		out[i].Column, out[i].EndLine, out[i].EndColumn = 0, 0, 0
	}

	return out
}

// measureProse counts the words and the lines of prose: a table row, a URL
// and a line of only URLs count for nothing.
func measureProse(prose []proseLine) (words, lines int) {
	for _, ln := range prose {
		body := strings.TrimSpace(ln.body())
		if strings.HasPrefix(body, "|") {
			continue
		}

		n := 0

		for _, tok := range strings.Fields(body) {
			if !strings.Contains(tok, "://") {
				n++
			}
		}

		if n > 0 {
			words += n
			lines++
		}
	}

	return words, lines
}

var placeNames = map[codePlace]string{
	codeTrailing:    "after code",
	codeInline:      "inside a body",
	codeDoc:         "on an unexported declaration",
	codeExportedDoc: "on an exported declaration",
	codeHeader:      "heading the file",
}

func codeCommentBudget(in input) []Finding {
	b := in.opts.commentBudgets().at(in.code.place)
	words, lines := measureProse(in.prose)

	if words <= b.Words && lines <= b.Lines {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Comment %s holds %d words on %d lines against a budget of %d words on %d: "+
			"keep the why the code cannot show, in short sentences.", placeNames[in.code.place], words, lines, b.Words, b.Lines),
	}}
}

var token = regexp.MustCompile(`\S+`)

func codeCommentSentence(in input) []Finding {
	most := in.opts.commentBudgets().Sentence

	var out []Finding

	for _, para := range paragraphs(in.prose, keep) {
		n, from := 0, 0

		report := func() {
			if n > most {
				out = append(out, Finding{
					Message: fmt.Sprintf("Sentence runs %d words against a budget of %d: split it.", n, most),
					Line:    para.lineAt(from),
				})
			}
		}

		for _, at := range token.FindAllStringIndex(para.text, -1) {
			tok := para.text[at[0]:at[1]]
			if n == 0 {
				from = at[0]
			}

			if !strings.Contains(tok, "://") {
				n++
			}

			if endsSentence(tok) {
				report()

				n = 0
			}
		}

		report()
	}

	return out
}

func banner(in input) []Finding {
	out := make([]Finding, 0, len(in.code.banners))

	for _, at := range in.code.banners {
		out = append(out, Finding{
			Message: "Drop the banner line: a file that wants sections wants splitting.", Line: at + 1,
		})
	}

	return out
}

var codeHistoryPatterns = slices.Concat(historyPatterns, commentHistoryPatterns)

// commentHistory reports the first history phrase on each prose line.
func commentHistory(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		if at, phrase := phraseIn(ln.text, codeHistoryPatterns); at >= 0 {
			out = append(out, Finding{Message: fmt.Sprintf(historyMessage, phrase), Match: phrase, Line: ln.line})
		}
	}

	return out
}

// spanFinding is one pattern a wording rule scans prose for.
type spanFinding struct {
	re      *regexp.Regexp
	message string
	// keepMatch filters a match by its text, nil for keeping all.
	keepMatch func(m string) bool
}

// scanSpans reports each match of each pattern in in's prose, quoted and
// backticked text masked. A pattern's first group, when it matched, is the
// span reported.
func scanSpans(in input, spans []spanFinding) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, blankBackticks) {
		text := blankQuoted(para.text)

		for _, s := range spans {
			for _, at := range s.re.FindAllStringSubmatchIndex(text, -1) {
				from, to := at[0], at[1]
				if len(at) > 2 && at[2] >= 0 {
					from, to = at[2], at[3]
				}

				m := text[from:to]
				if s.keepMatch != nil && !s.keepMatch(m) {
					continue
				}

				out = append(out, Finding{Message: fmt.Sprintf(s.message, m), Match: m, Line: para.lineAt(from)})
			}
		}
	}

	return out
}

var provenanceSpans = []spanFinding{
	{re: regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`), message: "Drop the date %q: a comment states the code, and the log dates it."},
	{
		re:      regexp.MustCompile(`(?:^|[\s(\[])(#\d+)\b|(?i)\b((?:PR|pull request|issue)\s+#?\d+)\b`),
		message: "Drop %q: a pull request or an issue is the log's to cite.",
	},
	{
		re:        regexp.MustCompile(`\b[0-9a-f]{7,40}\b`),
		message:   "Drop the commit %q: the log carries where the code came from.",
		keepMatch: func(m string) bool { return strings.ContainsAny(m, "0123456789") && strings.ContainsAny(m, "abcdef") },
	},
	{
		re: regexp.MustCompile(`(?i)\b(?:incidents?|outages?|post-?mortems?|we (?:saw|hit|found|observed|noticed)|` +
			`(?:once|last time) (?:we|it|this) (?:saw|hit|broke|wedged|stalled))\b`),
		message: "Drop the account %q: say what the code guards against, not the time it went wrong.",
	},
}

func provenance(in input) []Finding { return scanSpans(in, provenanceSpans) }

var docPathSpans = []spanFinding{
	{
		re:      regexp.MustCompile(`\bdocs/(?:design|decisions)\b[\w./-]*|\bADR[- ]?\d+\b|(?i)\bdecision record \d+\b|[\w./-]*\w\.md\b`),
		message: "Drop the citation %q: state what the code does and why, and leave the argument to the docs.",
	},
}

func docPath(in input) []Finding { return scanSpans(in, docPathSpans) }

// capitalWord matches a word in capitals. Group 1 is a `$` before it, an
// environment variable, and group 3 a `.ext` after it, a file name.
var capitalWord = regexp.MustCompile(`(\$?)\b([A-Z]{2,})\b(\.[a-z]+)?`)

// shortShouts are the two- and three-letter words that read as shouting in
// capitals. A longer all-caps word shouts unless it is an acronym.
var shortShouts = wordSet("no", "not", "all", "any", "and", "or", "but", "nor", "yes", "why", "how", "who",
	"now", "new", "old", "one", "two", "off", "out", "may", "can", "bad", "big", "few", "own", "is", "be",
	"it", "if", "at", "on", "in", "to", "up", "so", "as", "by", "of", "do", "we", "me", "my", "the", "too",
	"use", "set", "run", "end", "add")

// acronyms are the all-caps words of four letters or more that name a thing.
var acronyms = wordSet("json", "jsonl", "ndjson", "yaml", "toml", "http", "https", "html", "ascii", "posix",
	"scip", "sarif", "rdjson", "uuid", "ulid", "grpc", "oauth", "cors", "csrf", "ansi", "smtp", "imap", "ldap",
	"amqp", "mqtt", "wasm", "llvm", "cuda", "stdin", "stdout", "stderr", "todo", "fixme", "sbom", "repl",
	"crlf", "ecma", "jsdoc", "tsdoc", "otlp", "otel", "pcre", "goos", "goarch", "gopath", "goflags", "path",
	"home", "term", "sigint", "sigterm", "sigkill", "sighup", "sigpipe", "eintr", "eagain", "enoent", "eperm",
	"eacces", "eexist", "ebadf", "econnrefused", "ietf", "ieee", "unix", "ecdsa", "hmac", "mime", "gzip",
	"zstd", "jpeg", "webp", "pkce", "saml", "oidc", "xpos", "upos", "penn", "fifo", "lifo", "sqlite",
	"tsconfig", "pnpm", "npmrc", "mise", "magus", "head", "fetch_head", "orig_head", "readme", "license", "baggage",
	"tmpdir", "user", "shell", "editor", "lang", "post", "patch", "delete", "options", "trace", "connect")

func commentCaps(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, blankBackticks) {
		text := blankQuoted(para.text)

		for _, at := range capitalWord.FindAllStringSubmatchIndex(text, -1) {
			m := text[at[4]:at[5]]
			if at[3] > at[2] || at[7] > at[6] || !shouts(m, in.code.idents) {
				continue
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("Write %q in lowercase: capitals add no fact, so say why it matters instead.", m),
				Match:   m, Line: para.lineAt(at[4]),
			})
		}
	}

	return out
}

// shouts reports whether the capital word m is an ordinary word shouted.
func shouts(m string, idents map[string]bool) bool {
	w := strings.ToLower(m)
	if idents[m] || acronyms[w] {
		return false
	}

	return len(m) > 3 || shortShouts[w]
}

var firstPersonCode = regexp.MustCompile(`\b(?:I|[Ww]e|us|[Oo]urs?|me|[Mm]y)\b`)

func commentFirstPerson(in input) []Finding {
	out := scanSpans(in, []spanFinding{{
		re:      firstPersonCode,
		message: "Drop %q: say what the code does and why, not who decided it.",
	}})

	return slices.DeleteFunc(out, func(f Finding) bool { return f.Match == "I" && strings.Contains(in.symbol.Doc, "I/O") })
}

// codeRestates reports a one-line inline comment whose words all name the
// statement under it.
func codeRestates(in input) []Finding {
	if in.code.place != codeInline || in.code.next == "" {
		return nil
	}

	_, lines := measureProse(in.prose)
	if lines != 1 {
		return nil
	}

	said := words(in.symbol.Doc)
	if len(said) == 0 {
		return nil
	}

	named := map[string]bool{}

	for _, id := range identPattern.FindAllString(stripStrings(in.code.next), -1) {
		for _, w := range identWords(id) {
			named[w] = true
		}
	}

	for _, w := range said {
		if !named[w] {
			return nil
		}
	}

	text := strings.TrimSpace(in.symbol.Doc)

	return []Finding{{
		Message: fmt.Sprintf("Comment only repeats the code under it (%s); say why, or delete it.", clip(in.code.next, 60)),
		Match:   text,
	}}
}
