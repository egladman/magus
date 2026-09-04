package spells

import "strings"

// CommentSyntax declares one language's comment and string lexical facts, the
// way a spell declares its other toolchain facts: data, consumed by a generic
// engine (internal/ci/redundancy's comment stripper), never per-language code.
//
// The shape and the shipped values are seeded from scc's languages.json
// (github.com/boyter/scc, MIT license) - the schema is adopted, not the
// dataset: only languages magus has spells for are declared, and only where
// the declaration is HONEST. Go and Buzz are deliberately absent: magus owns
// real lexers for both (go/scanner, gopherbuzz) and uses them instead. Shell
// is deliberately absent too: heredocs make comment tokens content in a way a
// declaration of this shape cannot express, and a false "comment-only" costs
// trust in every refusal after it. Tree-sitter was evaluated as the road to
// more languages and rejected for now (the cgo-free routes are pre-release
// and heavyweight); a spell-declared mgs_ syntax fact is the nearer seam.
type CommentSyntax struct {
	// LineComments open a comment that runs to end of line.
	LineComments []string
	// BlockComments are open/close pairs.
	BlockComments [][2]string
	// Nested block comments honor nesting (Rust); otherwise the first closer
	// ends the comment.
	Nested bool
	// Quotes are the string forms; inside one, comment tokens are content.
	Quotes []Quote
	// Directives are comments that are CODE: a comment whose body (after the
	// opener and leading spaces) starts with one of these prefixes is never
	// stripped, so editing it is never comment-only.
	Directives []string
}

// Quote is one string form: its opening and closing tokens and whether a
// backslash escapes the closer.
type Quote struct {
	Open  string
	Close string
	// IgnoreEscape: a backslash before the closer does NOT keep the string
	// open (raw strings, and forms where backslash is literal).
	IgnoreEscape bool
}

// commentSyntaxByLanguage keys the shipped declarations by the canonical
// language name a spell's mgs_getLanguage returns.
var commentSyntaxByLanguage = map[string]CommentSyntax{
	"python": {
		LineComments: []string{"#"},
		// Triple-quoted docstrings are STRINGS: runtime-visible values, so an
		// edit to one is a code edit. Listing them as quotes is what makes a
		// "#" inside one read as content.
		Quotes: []Quote{
			{Open: `"""`, Close: `"""`},
			{Open: "'''", Close: "'''"},
			{Open: `"`, Close: `"`},
			{Open: "'", Close: "'"},
		},
		Directives: []string{"type:", "noqa"},
	},
	"typescript": {
		LineComments:  []string{"//"},
		BlockComments: [][2]string{{"/*", "*/"}},
		Quotes: []Quote{
			{Open: "`", Close: "`"},
			{Open: `"`, Close: `"`},
			{Open: "'", Close: "'"},
		},
		Directives: []string{"@ts-", "eslint-", "<reference"},
	},
	"javascript": {
		LineComments:  []string{"//"},
		BlockComments: [][2]string{{"/*", "*/"}},
		Quotes: []Quote{
			{Open: "`", Close: "`"},
			{Open: `"`, Close: `"`},
			{Open: "'", Close: "'"},
		},
		Directives: []string{"@ts-", "eslint-"},
	},
	"protobuf": {
		LineComments:  []string{"//"},
		BlockComments: [][2]string{{"/*", "*/"}},
		Quotes: []Quote{
			{Open: `"`, Close: `"`},
			{Open: "'", Close: "'"},
		},
	},
	"rust": {
		LineComments:  []string{"//"},
		BlockComments: [][2]string{{"/*", "*/"}},
		Nested:        true,
		Quotes: []Quote{
			{Open: `r#"`, Close: `"#`, IgnoreEscape: true},
			{Open: `r"`, Close: `"`, IgnoreEscape: true},
			{Open: `"`, Close: `"`},
		},
	},
}

// commentSyntaxByExtension routes a file extension (lowercase, with dot) to
// the language whose declaration covers it.
var commentSyntaxByExtension = map[string]string{
	".py":    "python",
	".ts":    "typescript",
	".tsx":   "typescript",
	".mts":   "typescript",
	".cts":   "typescript",
	".js":    "javascript",
	".jsx":   "javascript",
	".mjs":   "javascript",
	".cjs":   "javascript",
	".proto": "protobuf",
	".rs":    "rust",
}

// CommentSyntaxForExtension returns the declared comment syntax covering a
// file extension (".py"), and whether one exists. Absence means the language
// declared nothing, and a caller must classify its files as code rather than
// guess delimiters.
func CommentSyntaxForExtension(ext string) (CommentSyntax, bool) {
	lang, ok := commentSyntaxByExtension[strings.ToLower(ext)]
	if !ok {
		return CommentSyntax{}, false
	}
	s, ok := commentSyntaxByLanguage[lang]
	return s, ok
}
