package spells

// Syntax groups the lexical facts a spell declares about its language, each
// optional and each declared only when the spell can declare it honestly:
// how comments and strings are written, and how a placeholder body is.
type Syntax struct {
	Comments *CommentSyntax `json:"comments,omitempty"`
	Stubs    *StubSyntax    `json:"stubs,omitempty"`
}
