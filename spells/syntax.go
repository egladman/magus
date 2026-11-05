package spells

// Syntax groups the lexical facts a spell declares about its language, each
// optional and each declared only when the spell can declare it honestly.
type Syntax struct {
	Comments *CommentSyntax `json:"comments,omitempty"`
	Stubs    *StubSyntax    `json:"stubs,omitempty"`
	// Operators lists operators of two or more characters, so a scan reads
	// `<=` whole, never as `<`. Tokens Decisions names are read whole
	// unlisted.
	Operators []string        `json:"operators,omitempty"`
	Decisions *DecisionSyntax `json:"decisions,omitempty"`
}

// DecisionSyntax declares the tokens that each add one path through a
// function. Complexity is 1 plus their count outside comments and strings.
type DecisionSyntax struct {
	// Keywords match whole words: if, for, case.
	Keywords []string `json:"keywords,omitempty"`
	// Operators match exact tokens: &&, ||, ??.
	Operators []string `json:"operators,omitempty"`
}
