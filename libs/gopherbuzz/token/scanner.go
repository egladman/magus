package token

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// LiteralScanner is one string-literal form a host adds to the scanner. It is
// asked only at a token start whose first byte is Prefix, and it yields only a
// String token. The scanner refuses a claim no wider than the identifier at
// that position, so a literal never takes an identifier or a keyword.
type LiteralScanner struct {
	// Name names the form in errors and in Scanner.Name. It is non-empty and
	// holds no '+'.
	Name string
	// Prefix is the ASCII letter that opens the form.
	Prefix byte
	// Scan reads the literal opening src. An n of 0 declines: the scanner
	// carries on as upstream Buzz. A positive n makes src[:n] one String token
	// holding lit; a claim past the end of src is refused. An error means the
	// form opened and is malformed, reported as "buzz: <err> at line L:C".
	Scan func(src string) (lit string, n int, err error)
}

// Scanner scans Buzz source with the literal forms a host added. The zero value
// and a nil *Scanner scan upstream Buzz. A Scanner is immutable and safe for
// concurrent use. Caches key on its pointer, so a host builds one and shares it.
type Scanner struct {
	literals []LiteralScanner
	prefixes [utf8.RuneSelf]bool
	name     string
}

// NewScanner returns a Scanner that adds literals, tried in the order given. It
// refuses an empty, duplicate or '+'-holding Name, a Prefix that is not an
// ASCII letter, and a nil Scan.
func NewScanner(literals ...LiteralScanner) (*Scanner, error) {
	s := &Scanner{literals: slices.Clone(literals)}
	names := make([]string, 0, len(literals))
	var errs []error
	for _, ls := range literals {
		switch {
		case ls.Name == "" || strings.Contains(ls.Name, "+"):
			errs = append(errs, fmt.Errorf("token: literal name %q must be non-empty and hold no '+'", ls.Name))
		case slices.Contains(names, ls.Name):
			errs = append(errs, fmt.Errorf("token: literal %q is registered twice", ls.Name))
		case !isASCIILetter(ls.Prefix):
			errs = append(errs, fmt.Errorf("token: literal %q has prefix %q; a prefix is an ASCII letter", ls.Name, ls.Prefix))
		case ls.Scan == nil:
			errs = append(errs, fmt.Errorf("token: literal %q has no Scan", ls.Name))
		}
		names = append(names, ls.Name)
		if isASCIILetter(ls.Prefix) {
			s.prefixes[ls.Prefix] = true
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	slices.Sort(names)
	s.name = strings.Join(names, "+")
	return s, nil
}

// Name returns the literal names, sorted and joined with "+". It is "" for a
// nil or zero Scanner.
func (s *Scanner) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

func isASCIILetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// claim asks each literal whose prefix opens the source at sc.pos. n is 0 when
// none claims it.
func (sc *scanner) claim() (lit string, n int, err error) {
	rest := sc.src[sc.pos:]
	ident := identWidth(rest)
	for _, ls := range sc.host.literals {
		if ls.Prefix != rest[0] {
			continue
		}
		lit, n, err := ls.Scan(rest)
		switch {
		case err != nil:
			return "", 0, fmt.Errorf("buzz: %w at line %d:%d", err, sc.line, sc.col)
		case n == 0:
			continue
		case n < 0 || n > len(rest):
			return "", 0, fmt.Errorf("buzz: literal %q claimed %d bytes past the end of the source at line %d:%d", ls.Name, n, sc.line, sc.col)
		case n <= ident:
			return "", 0, fmt.Errorf("buzz: literal %q claimed an identifier at line %d:%d", ls.Name, sc.line, sc.col)
		}
		return lit, n, nil
	}
	return "", 0, nil
}

// skip advances over n claimed bytes, which may span lines.
func (sc *scanner) skip(n int) {
	text := sc.src[sc.pos : sc.pos+n]
	sc.pos += n
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		sc.line += strings.Count(text, "\n")
		sc.col = len(text) - i
		return
	}
	sc.col += n
}

// inIdent reports whether sc.pos continues an identifier begun after from, so
// a prefix letter there is not a token start.
func (sc *scanner) inIdent(from int) bool {
	if sc.pos <= from {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(sc.src[from:sc.pos])
	return isIdentContinue(r)
}

// identWidth is the byte length of the identifier run that opens src.
func identWidth(src string) int {
	for i, r := range src {
		if !isIdentContinue(r) {
			return i
		}
	}
	return len(src)
}
