package spells

// StubSyntax declares how a language writes a placeholder body for a
// declaration whose real body lands in a later branch of a split. It is data
// in the spell's own Buzz (mgs_getLanguage's typed answer), the way
// CommentSyntax is: SCIP says where a declaration is and what kind it is, this
// says what to put in its body, and the splitter that combines the two is
// Buzz. magus's Go only decodes and carries it.
//
// A spell declares stubs only when every listed kind can take Body without an
// import or a zero value: a panic/throw body type-checks under any signature.
type StubSyntax struct {
	// Kinds are the SCIP SymbolInformation kinds ("Function", "Method") that
	// may be stubbed. Any other kind is pulled into the layer whole.
	Kinds []string `json:"kinds,omitempty"`
	// BodyStyle is how the body is delimited in source: "brace" (the body is
	// the final {...} of the declaration) or "indent" (the body is the block
	// after the header's last top-level colon).
	BodyStyle string `json:"bodyStyle,omitempty"`
	// Body is a Mustache template (the template module) for the replacement
	// body. For "brace" it includes the braces; for "indent" it is the block's
	// statements at no indentation, which the splitter indents to the block's
	// depth. Its holes are Name, Qualified, Kind and Branch; write them
	// unescaped ({{&Name}}), since Mustache's default {{Name}} HTML-escapes
	// quotes, and a hole Mustache cannot parse ({{.Name}}) renders empty.
	Body string `json:"body,omitempty"`
}

// The body styles a StubSyntax may declare.
const (
	StubBodyBrace  = "brace"
	StubBodyIndent = "indent"
)
