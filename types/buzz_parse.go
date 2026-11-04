package types

// BuzzFile is buzz.parse's record of one Buzz source file. Lines are 1-based.
// Error is "" when the file parsed; otherwise it carries why the file could not
// be read or parsed, and the lists are empty.
type BuzzFile struct {
	Path       string
	Error      string
	Functions  []BuzzFunction
	Statements []BuzzStatement
	Tests      []BuzzTest
}

// BuzzFunction is one function in a parsed file: a top-level fun, an object
// method, or a function literal (Name ""). ObjectName names a method's
// object. ErrorType is the !> annotation as written, "" when the function
// declares none. Throws lists the line of each throw statement whose innermost
// enclosing function is this one.
type BuzzFunction struct {
	Name       string
	ObjectName string
	Line       int
	EndLine    int
	ErrorType  string
	Throws     []int
}

// BuzzStatement is one top-level statement. Kind is the keyword it opens with,
// such as import, fun, object or test. A declaration without one is "decl".
// Kind is "expr" or "assign" for a statement that opens with an expression.
type BuzzStatement struct {
	Kind    string
	Name    string
	Line    int
	EndLine int
}

// BuzzTest is one test block. Throws lists the line of each throw statement
// directly inside it, outside any function literal.
type BuzzTest struct {
	Name    string
	Line    int
	EndLine int
	Throws  []int
}
