package emit

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

const (
	headerWord = "hole"
	minCount   = 1
	maxCount   = 8
	bom        = "\xef\xbb\xbf"
)

// Problem is one refusal with its place. Line and Column are 1-based, the column in
// bytes. Line 0 belongs to the whole file.
type Problem struct {
	Code    types.DiagnosticCode
	Line    int
	Column  int
	Message string
}

// String renders the problem as "name:line:col: [code] message".
func (p Problem) String(name string) string {
	if p.Line == 0 {
		return fmt.Sprintf("%s: [%s] %s", name, p.Code, p.Message)
	}
	return fmt.Sprintf("%s:%d:%d: [%s] %s", name, p.Line, p.Column, p.Code, p.Message)
}

// Problems is every refusal found in one file, in file order.
type Problems []Problem

// Lines renders each problem on its own line.
func (ps Problems) Lines(name string) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String(name)
	}
	return out
}

// Hole is one parsed hole, before a value is bound to it.
type Hole struct {
	Name       string
	Conversion string
	Spec       string
	Line       int
	Column     int
}

// Parsed is a template file not yet bound to values. Strings holds one more item than
// Holes. Count is the brace count line 1 declared.
type Parsed struct {
	Count   int
	Strings []string
	Holes   []Hole
}

// Parse reads a template file without values, so it cannot spot a hole typed with
// the wrong count. The parse is returned only when there are no problems.
func Parse(src []byte) (Parsed, Problems) {
	return parse(src, nil)
}

// Load parses a template file and binds every hole to values, reporting all problems
// at once. values must name every hole and nothing else, and none may be nil. The
// template is returned only when there are no problems; name becomes its Source.
func Load(name string, src []byte, values map[string]any) (types.Template, Problems) {
	if values == nil {
		values = map[string]any{}
	}
	p, problems := parse(src, values)
	if len(problems) == 0 {
		problems = bind(p, values)
	}
	sortProblems(problems)
	if len(problems) > 0 {
		return types.Template{}, problems
	}
	t := types.Template{Strings: p.Strings, Source: name}
	for _, h := range p.Holes {
		t.Interpolations = append(t.Interpolations, types.Interpolation{
			Value:      values[h.Name],
			Expression: h.Name,
			Conversion: h.Conversion,
			FormatSpec: h.Spec,
		})
	}
	return t, nil
}

func bind(p Parsed, values map[string]any) Problems {
	var out Problems
	read := make(map[string]bool, len(p.Holes))
	nullReported := map[string]bool{}
	for _, h := range p.Holes {
		read[h.Name] = true
		v, ok := values[h.Name]
		switch {
		case !ok:
			out = append(out, Problem{types.EmitMissingValue, h.Line, h.Column,
				fmt.Sprintf("hole %q has no value: add %q to values", h.Name, h.Name)})
		case v == nil && !nullReported[h.Name]:
			nullReported[h.Name] = true
			out = append(out, Problem{types.EmitNullValue, h.Line, h.Column,
				fmt.Sprintf("value %q is null: compute it before load, or drop the hole", h.Name)})
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !read[k] {
			out = append(out, Problem{types.EmitUnusedValue, 0, 0,
				fmt.Sprintf("value %q is read by no hole: remove it from values", k)})
		}
	}
	return out
}

func sortProblems(ps Problems) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.Line != b.Line {
			return b.Line == 0 || a.Line != 0 && a.Line < b.Line
		}
		return a.Column < b.Column
	})
}

type scanner struct {
	n      int
	known  map[string]any
	out    Parsed
	text   strings.Builder
	probs  Problems
	lineNo int
}

func parse(src []byte, known map[string]any) (Parsed, Problems) {
	probs := checkBytes(src)

	lines := strings.Split(strings.TrimPrefix(string(src), bom), "\n")
	count, hp := parseHeader(lines[0])
	if hp != nil {
		probs = append(probs, *hp)
		sortProblems(probs)
		return Parsed{}, probs
	}

	s := &scanner{n: count, known: known}
	s.out.Count = count
	for i := 1; i < len(lines); i++ {
		s.lineNo = i + 1
		s.scanLine(lines[i])
		if i < len(lines)-1 {
			s.text.WriteByte('\n')
		}
	}
	s.out.Strings = append(s.out.Strings, s.text.String())
	probs = append(probs, s.probs...)
	sortProblems(probs)
	if len(probs) > 0 {
		return Parsed{}, probs
	}
	return s.out, nil
}

// checkBytes reports the first of each kind of bad byte on each line, so a CRLF file
// costs one problem per line.
func checkBytes(src []byte) Problems {
	var out Problems
	if strings.HasPrefix(string(src), bom) {
		out = append(out, Problem{types.EmitBadBytes, 1, 1, "byte-order mark: the template is UTF-8 without one"})
	}
	line, col := 1, 1
	crSeen, nulSeen, badSeen := 0, 0, 0
	for i := 0; i < len(src); {
		b := src[i]
		switch {
		case b == '\n':
			line++
			col = 1
			i++
			continue
		case b == '\r' && crSeen != line:
			crSeen = line
			out = append(out, Problem{types.EmitBadBytes, line, col, "carriage return: the template has LF line endings only"})
		case b == 0 && nulSeen != line:
			nulSeen = line
			out = append(out, Problem{types.EmitBadBytes, line, col, "NUL byte"})
		case b >= utf8.RuneSelf:
			r, size := utf8.DecodeRune(src[i:])
			if r == utf8.RuneError && size == 1 && badSeen != line {
				badSeen = line
				out = append(out, Problem{types.EmitBadBytes, line, col, "invalid UTF-8"})
			}
			i += size
			col += size
			continue
		}
		i++
		col++
	}
	return out
}

func parseHeader(line string) (int, *Problem) {
	bad := func(msg string) (int, *Problem) {
		return 0, &Problem{types.EmitBadHeader, 1, 1, msg}
	}
	open := 0
	for open < len(line) && line[open] == '{' {
		open++
	}
	rest := line[open:]
	if !strings.HasPrefix(rest, headerWord) {
		return bad("line 1 must show the delimiter as a hole: {{hole}}")
	}
	rest = rest[len(headerWord):]
	cl := 0
	for cl < len(rest) && rest[cl] == '}' {
		cl++
	}
	switch {
	case rest[cl:] != "":
		return bad("line 1 holds more than the delimiter hole")
	case open != cl:
		return bad(fmt.Sprintf("line 1 opens with %d braces and closes with %d", open, cl))
	case open < minCount || open > maxCount:
		return bad(fmt.Sprintf("line 1 uses %d braces: use %d to %d", open, minCount, maxCount))
	}
	return open, nil
}

func (s *scanner) fail(code types.DiagnosticCode, col int, msg string) {
	s.probs = append(s.probs, Problem{code, s.lineNo, col, msg})
}

func (s *scanner) scanLine(line string) {
	n := s.n
	i := 0
	for i < len(line) {
		if line[i] != '{' {
			j := strings.IndexByte(line[i:], '{')
			if j < 0 {
				s.text.WriteString(line[i:])
				return
			}
			s.text.WriteString(line[i : i+j])
			i += j
			continue
		}
		m := 0
		for i+m < len(line) && line[i+m] == '{' {
			m++
		}
		switch {
		case m < n:
			s.checkWrongCount(line, i, m)
			s.text.WriteString(line[i : i+m])
			i += m
		case m >= 2*n:
			s.fail(types.EmitBadBraces, i+1, fmt.Sprintf(
				"%d opening braces in a row: text holds at most %d with delimiter %d; raise the count on line 1", m, n-1, n))
			s.text.WriteString(line[i : i+m])
			i += m
		default:
			// The last n braces open the hole; the rest are text.
			s.text.WriteString(line[i : i+m-n])
			i = s.scanHole(line, i+m-n, i+m)
		}
	}
}

// scanHole reads a hole whose opening braces start at open and whose inside starts at
// inner. It returns where the line continues.
func (s *scanner) scanHole(line string, open, inner int) int {
	n := s.n
	end := strings.IndexByte(line[inner:], '}')
	if end < 0 {
		s.fail(types.EmitBadBraces, open+1, "opening braces with no close on the line")
		s.text.WriteString(line[open:inner])
		return inner
	}
	end += inner
	c := 0
	for end+c < len(line) && line[end+c] == '}' {
		c++
	}
	switch {
	case c < n:
		s.fail(types.EmitBadBraces, open+1, fmt.Sprintf("hole closes with %d braces: delimiter is %d", c, n))
		s.text.WriteString(line[open : end+c])
		return end + c
	case c >= 2*n:
		s.fail(types.EmitBadBraces, end+1, fmt.Sprintf(
			"%d closing braces in a row: text holds at most %d with delimiter %d", c, n-1, n))
		s.text.WriteString(line[open : end+c])
		return end + c
	}
	h, msg := parseBody(line[inner:end])
	if msg != "" {
		s.fail(types.EmitBadHole, inner+1, msg)
		s.text.WriteString(line[open : end+c])
		return end + c
	}
	h.Line, h.Column = s.lineNo, open+1
	s.out.Strings = append(s.out.Strings, s.text.String())
	s.text.Reset()
	s.out.Holes = append(s.out.Holes, h)
	// The first n closing braces close the hole; the rest are text.
	s.text.WriteString(line[end+n : end+c])
	return end + c
}

// checkWrongCount refuses a group of m < n braces whose inside is a name the values
// hold: a hole typed with the wrong delimiter count.
func (s *scanner) checkWrongCount(line string, at, m int) {
	if s.known == nil {
		return
	}
	inner := at + m
	end := strings.IndexByte(line[inner:], '}')
	if end < 0 {
		return
	}
	end += inner
	if !strings.HasPrefix(line[end:], strings.Repeat("}", m)) {
		return
	}
	h, msg := parseBody(line[inner:end])
	if msg != "" {
		return
	}
	if _, ok := s.known[h.Name]; ok {
		s.fail(types.EmitBadBraces, at+1, fmt.Sprintf(
			"%q is a value, written with %d braces: this file's delimiter is %d", h.Name, m, s.n))
	}
}

func isNameStart(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isNameByte(b byte) bool { return isNameStart(b) || b >= '0' && b <= '9' }

func isWS(b byte) bool { return b == ' ' || b == '\t' }

// parseBody reads ws name [!conv] [:spec] ws. A non-empty message is the refusal.
// The spec is verbatim, trailing space included.
func parseBody(body string) (Hole, string) {
	i := 0
	for i < len(body) && isWS(body[i]) {
		i++
	}
	if i == len(body) {
		return Hole{}, "empty hole: name the value it reads"
	}
	if !isNameStart(body[i]) {
		return Hole{}, fmt.Sprintf("hole name starts with %q: use ASCII letters, digits and _, not starting with a digit", body[i])
	}
	start := i
	for i < len(body) && isNameByte(body[i]) {
		i++
	}
	h := Hole{Name: body[start:i]}
	if i < len(body) {
		switch body[i] {
		case '.', '[':
			return Hole{}, fmt.Sprintf("hole %q reads a dotted or indexed name: compute one name in Buzz", strings.TrimSpace(body[start:]))
		case '=':
			return Hole{}, "'=' after a hole name: debug formatting is not supported"
		}
	}
	if i < len(body) && body[i] == '!' {
		if i+1 >= len(body) || !strings.ContainsRune("rsa", rune(body[i+1])) {
			return Hole{}, "conversion must be r, s or a"
		}
		h.Conversion = body[i+1 : i+2]
		i += 2
	}
	if i < len(body) && body[i] == ':' {
		spec := body[i+1:]
		if strings.ContainsAny(spec, "{}") {
			return Hole{}, "brace in the spec"
		}
		h.Spec = spec
		return h, ""
	}
	for i < len(body) && isWS(body[i]) {
		i++
	}
	if i < len(body) {
		return Hole{}, fmt.Sprintf("unexpected %q after the hole name", body[i])
	}
	return h, ""
}
