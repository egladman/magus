package textrank

// The fields a ranked document may carry. A field the weight file names no weight for is
// indexed but never scored.
const (
	FieldName      = "name"
	FieldPath      = "path"
	FieldDoc       = "doc"
	FieldSignature = "signature"
	FieldBody      = "body"
)

// SplitIdentifier splits an identifier into its lowercased words, in order: camelCase,
// PascalCase with acronyms (HTTPServer -> http, server), snake_case, kebab-case, dots and
// slashes.
func SplitIdentifier(s string) []string {
	panic("textrank: SplitIdentifier not implemented")
}

// Tokens lowercases text and splits it on non-word runes. Every identifier-shaped word
// yields the whole word and then its SplitIdentifier parts, so a query for either finds
// it. Nothing is stemmed.
func Tokens(text string) []string {
	panic("textrank: Tokens not implemented")
}
