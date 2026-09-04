package spells

// Language is what mgs_getLanguage declares: the canonical language name and,
// when the spell can declare it honestly, the language's comment and string
// syntax. One method, one typed answer - the syntax is not a separate
// declaration a spell can forget beside the name.
type Language struct {
	Name string `json:"name,omitempty"`
	// Extensions are the file extensions (lowercase, with dot) that ARE this
	// language. Explicit rather than derived from the spell's glob claims:
	// go's spell also claims .s, .c, .h and .txtar, none of which are Go.
	Extensions []string       `json:"extensions,omitempty"`
	Comments   *CommentSyntax `json:"comments,omitempty"`
}

// CommentSyntax declares one language's comment and string lexical facts, the
// way a spell declares its other toolchain facts: data in the spell's own
// Buzz (mgs_getLanguage's typed answer), consumed by a generic engine
// (internal/ci's comment stripper), never per-language Go.
//
// The shape is seeded from scc's languages.json (github.com/boyter/scc, MIT
// license) - the schema is adopted, not the dataset: a spell declares only
// what it can declare HONESTLY. The bash spell deliberately declares nothing:
// heredocs make comment tokens content in a way this shape cannot express,
// and a false "comment-only" costs trust in every gate refusal after it. A
// file whose extension no spell claims always classifies as code.
type CommentSyntax struct {
	// LineComments open a comment that runs to end of line.
	LineComments []string `json:"lineComments,omitempty"`
	// BlockComments are open/close pairs.
	BlockComments []CommentBlock `json:"blockComments,omitempty"`
	// Nested block comments honor nesting (Rust); otherwise the first closer
	// ends the comment.
	Nested bool `json:"nested,omitempty"`
	// Quotes are the string forms; inside one, comment tokens are content.
	Quotes []Quote `json:"quotes,omitempty"`
	// Directives are comments that are CODE: a comment whose body (after the
	// opener and leading spaces) starts with one of these prefixes is never
	// stripped, so editing it is never comment-only.
	Directives []string `json:"directives,omitempty"`
}

// CommentBlock is one block-comment form: its opening and closing tokens.
type CommentBlock struct {
	Open  string `json:"open,omitempty"`
	Close string `json:"close,omitempty"`
}

// Quote is one string form: its opening and closing tokens and whether a
// backslash escapes the closer.
type Quote struct {
	Open  string `json:"open,omitempty"`
	Close string `json:"close,omitempty"`
	// IgnoreEscape: a backslash before the closer does NOT keep the string
	// open (raw strings, and forms where backslash is literal).
	IgnoreEscape bool `json:"ignoreEscape,omitempty"`
}

// CommentSyntaxIndex routes a file extension (lowercase, with dot) to the
// syntax of the spell whose language claims it. Later spells never override
// an earlier claim, so built-ins win over a workspace spell redeclaring an
// extension; the classifier's message names the winning spell either way.
func CommentSyntaxIndex(list []*Spell) map[string]CommentSyntax {
	out := map[string]CommentSyntax{}
	for _, s := range list {
		syn := s.Comments()
		if syn == nil {
			continue
		}
		for _, ext := range s.LanguageExtensions() {
			if _, taken := out[ext]; !taken {
				out[ext] = *syn
			}
		}
	}
	return out
}
