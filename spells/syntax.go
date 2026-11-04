package spells

// Syntax groups the lexical facts a spell declares about its language, each
// optional and each declared only when the spell can declare it honestly.
type Syntax struct {
	Comments *CommentSyntax `json:"comments,omitempty"`
	Stubs    *StubSyntax    `json:"stubs,omitempty"`
	// Operators lists operators of two or more characters, so a scan reads
	// `<=` whole, never as `<`. Tokens Decisions and Mutations name are
	// read whole unlisted.
	Operators []string        `json:"operators,omitempty"`
	Decisions *DecisionSyntax `json:"decisions,omitempty"`
	Mutations *MutationSyntax `json:"mutations,omitempty"`
}

// DecisionSyntax declares the tokens that each add one path through a
// function. Complexity is 1 plus their count outside comments and strings.
type DecisionSyntax struct {
	// Keywords match whole words: if, for, case.
	Keywords []string `json:"keywords,omitempty"`
	// Operators match exact tokens: &&, ||, ??.
	Operators []string `json:"operators,omitempty"`
}

// MutationSyntax declares the mutants a function body yields.
type MutationSyntax struct {
	Swaps []Swap `json:"swaps,omitempty"`
	// Bodies replace the whole body, cut as Stubs.BodyStyle cuts it. Each
	// yields a mutant whose operator is BodyMutation.
	Bodies []string `json:"bodies,omitempty"`
	// Arid are RE2 patterns. A line matching one is never swapped: a
	// logging call makes mutants no test should have to kill.
	Arid []string `json:"arid,omitempty"`
}

// Swap replaces one token with another. Name is what a `magus:mutant` marker
// names. The fields are not From and To because `from` is reserved in Buzz.
type Swap struct {
	Name    string `json:"name"`
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

// BodyMutation is the operator of a mutant that replaces a whole body. No
// swap may take the name.
const BodyMutation = "body"
