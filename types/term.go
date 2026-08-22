package types

// TermStyle names a terminal text style a term\colorize call applies.
//
// A named type with a declared case list rather than a raw SGR string, for the
// reason [SignAlgorithm] and [PlatformStyle] give: the underlying form is
// "\x1b[2;32m", which nobody should be asked to type or proofread, and a wrong
// code is not an error - it is output that renders as garbage on someone else's
// terminal. The cases are exactly the SGR codes internal/interactive/tty already
// defines, so this invents no palette; it names the one magus renders with.
//
// The zero value is "no styling", which Colorize already treats as pass-through.
// That makes a conditionally-computed style safe to pass without branching.
type TermStyle string

const (
	// TermBold is emphasis without color, readable on any background.
	TermBold TermStyle = "1"
	// TermDim lowers a line's signal without hiding it.
	TermDim TermStyle = "2"
	// TermRed marks a failure.
	TermRed TermStyle = "31"
	// TermGreen marks a success.
	TermGreen TermStyle = "32"
	// TermYellow marks a warning.
	TermYellow TermStyle = "33"
	// TermDimGreen is the low-signal success magus renders a cache hit with:
	// it happened, and it is not what you are reading the output for.
	TermDimGreen TermStyle = "2;32"
	// TermDimGrey is for text that is present for reference rather than to be read.
	TermDimGrey TermStyle = "2;37"
	// TermBrightGreen is the high-signal success, for the result of a whole run.
	TermBrightGreen TermStyle = "1;32"
)

// TermSize is a terminal's dimensions in character cells, as term\size reports
// them.
//
// An object rather than two returns because a pair of bare ints at a call site is
// exactly the shape that gets swapped: os\platform's three-string return has the
// same problem and cannot be fixed without breaking callers. Both fields are 0
// when the size cannot be determined - piped output, no controlling terminal - so
// a caller checks one field rather than interpreting an error.
type TermSize struct {
	Width  int
	Height int
}
