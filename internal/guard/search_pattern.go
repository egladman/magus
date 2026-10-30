package guard

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
)

// asSearch reads `git grep` and programSearch's tools as grep. It leaves a git
// grep relocated by -C, --git-dir or --work-tree, which searches another tree.
// It leaves one carrying -c, which can change the pattern dialect.
func asSearch(c hint.Invocation) hint.Invocation {
	if g, ok := programSearch(c); ok {
		return g
	}
	if path.Base(c.Name) != "git" {
		return c
	}
	g := parseGit(c.Args)
	if g.sub != "grep" || len(g.dirs) > 0 || g.opaque || g.configured {
		return c
	}
	return hint.Invocation{Name: "grep", Args: append([]string{"-r"}, g.rest...)}
}

var (
	// awkAddressRe selects lines by one regex address and one plain action.
	// A range, a negation or a BEGIN block is another question.
	awkAddressRe     = regexp.MustCompile(`^\s*/((?:\\.|[^/\\])+)/\s*(?:\{[^{}]*\})?\s*$`)
	sedPrintRe       = regexp.MustCompile(`^\s*/((?:\\.|[^/\\])+)/p\s*$`)
	perlPrintIfRe    = regexp.MustCompile(`^\s*print(?:\s+\$_)?\s+if\s+(?:\$_\s*=~\s*)?m?/((?:\\.|[^/\\])+)/(i?);?\s*$`)
	perlMatchPrintRe = regexp.MustCompile(`^\s*m?/((?:\\.|[^/\\])+)/(i?)\s*(?:and|&&)\s*print;?\s*$`)
	awkVarOperandRe  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

// programSearch reads ack, and an awk, sed or perl program printing the lines
// a regex matches, as grep. Any other program reports false. A search with no
// file operand reads stdin, so placeSearch leaves its grep alone.
func programSearch(c hint.Invocation) (hint.Invocation, bool) {
	switch path.Base(c.Name) {
	case "awk", "gawk", "mawk":
		return awkSearch(c.Args)
	case "sed":
		return sedSearch(c.Args)
	case "perl":
		return perlSearch(c.Args)
	case "ack":
		return hint.Invocation{Name: "grep", Args: append([]string{"-r"}, c.Args...)}, true
	}
	return hint.Invocation{}, false
}

// delimitedGrep is the grep for pattern, a delimited regex, in files.
func delimitedGrep(flags []string, pattern string, files []string) hint.Invocation {
	args := append(slices.Clone(flags), "-e", strings.ReplaceAll(pattern, `\/`, "/"))
	return hint.Invocation{Name: "grep", Args: append(args, files...)}
}

func awkSearch(args []string) (hint.Invocation, bool) {
	var operands []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case a == "-f" || a == "--file" || strings.HasPrefix(a, "-f") || strings.HasPrefix(a, "--file="):
			return hint.Invocation{}, false
		case a == "-F" || a == "-v":
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1:
		default:
			operands = append(operands, a)
		}
	}
	if len(operands) < 2 {
		return hint.Invocation{}, false
	}
	m := awkAddressRe.FindStringSubmatch(operands[0])
	if m == nil || slices.ContainsFunc(operands[1:], awkVarOperandRe.MatchString) {
		return hint.Invocation{}, false
	}
	return delimitedGrep([]string{"-E"}, m[1], operands[1:]), true
}

func sedSearch(args []string) (hint.Invocation, bool) {
	var operands, flags []string
	quiet := false
	script := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case a == "-e" || a == "--expression":
			if i+1 >= len(args) || script != "" {
				return hint.Invocation{}, false
			}
			i++
			script = args[i]
		case a == "-n" || a == "--quiet" || a == "--silent":
			quiet = true
		case a == "-E" || a == "-r" || a == "--regexp-extended":
			flags = append(flags, "-E")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return hint.Invocation{}, false
		default:
			operands = append(operands, a)
		}
	}
	if script == "" && len(operands) > 0 {
		script, operands = operands[0], operands[1:]
	}
	m := sedPrintRe.FindStringSubmatch(script)
	if !quiet || m == nil {
		return hint.Invocation{}, false
	}
	return delimitedGrep(flags, m[1], operands), true
}

func perlSearch(args []string) (hint.Invocation, bool) {
	var operands []string
	program, numbered := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" || program != "" {
			operands = append(operands, a)
			continue
		}
		cluster := a[1:]
		for j := 0; j < len(cluster); j++ {
			switch cluster[j] {
			case 'n':
				numbered = true
			case 'l', 'a':
			case 'e':
				if rest := cluster[j+1:]; rest != "" {
					program = rest
				} else if i+1 < len(args) {
					i++
					program = args[i]
				}
				j = len(cluster)
			default:
				return hint.Invocation{}, false
			}
		}
	}
	if !numbered || program == "" {
		return hint.Invocation{}, false
	}
	m := perlPrintIfRe.FindStringSubmatch(program)
	if m == nil {
		m = perlMatchPrintRe.FindStringSubmatch(program)
	}
	if m == nil {
		return hint.Invocation{}, false
	}
	flags := []string{"-P"}
	if m[2] == "i" {
		flags = append(flags, "-i")
	}
	return delimitedGrep(flags, m[1], operands), true
}

// regexMode is how a search tool reads its pattern, which decides what separates two
// alternatives.
type regexMode int

const (
	regexBasic    regexMode = iota // `\|` separates
	regexExtended                  // `|` separates
	regexFixed                     // nothing does
)

func searchMode(c hint.Invocation) regexMode {
	name := path.Base(c.Name)
	switch {
	case name == "fgrep" || hasFlag(c.Args, 'F', "fixed-strings"),
		name == "ag" && hasFlag(c.Args, 'Q', "literal"):
		return regexFixed
	case name == "egrep" || name == "rg" || name == "ag":
		return regexExtended
	// rg's -E names an encoding, so only grep's spelling reaches here.
	case hasFlag(c.Args, 'E', "extended-regexp") || hasFlag(c.Args, 'P', "perl-regexp"):
		return regexExtended
	}
	return regexBasic
}

func splitAlternation(p string, mode regexMode) []string {
	switch mode {
	case regexBasic:
		return strings.Split(p, `\|`)
	case regexExtended:
		var out []string
		start := 0
		for i := 0; i < len(p); i++ {
			switch p[i] {
			case '\\':
				i++
			case '|':
				out = append(out, p[start:i])
				start = i + 1
			}
		}
		return append(out, p[start:])
	}
	return []string{p}
}

// whitespaceClassRe matches the regex spellings of "some whitespace", so `func\s+Foo`
// reads as the definition lookup it is.
var whitespaceClassRe = regexp.MustCompile(`(?:\\s|\[\[:space:\]\]| )[+*]?`)

// normalizeAlternative strips the anchors and word boundaries that narrow a match without
// changing which name it looks for, and a trailing call paren.
func normalizeAlternative(alt string) string {
	alt = strings.TrimSpace(whitespaceClassRe.ReplaceAllString(alt, " "))
	for {
		before := alt
		for _, prefix := range []string{"^", `\b`, `\<`} {
			alt = strings.TrimPrefix(alt, prefix)
		}
		for _, suffix := range []string{"$", `\b`, `\>`, `\(`, "(", " "} {
			alt = strings.TrimSuffix(alt, suffix)
		}
		if alt == before {
			return strings.TrimSpace(alt)
		}
	}
}

// altKind is what one alternative of a search pattern looks for.
type altKind int

const (
	altText       altKind = iota
	altName               // a name: a call, a member, a word search, or an identifier's shape
	altDefinition         // a declaration lookup: func X, type X, class X, def X
	altDiagnostic
)

// searchAlt is one alternative of a search's patterns, classified by its syntax alone. Whether
// a name is a symbol is the index's call, made in judgeSearch.
type searchAlt struct {
	raw   string
	ident string
	kind  altKind
	// strong reports a name whose own shape says identifier (CamelCase, snake_case, or a
	// declaration keyword before it), which a stale index may vouch for before it has
	// indexed it. A plain word is as likely prose, so only a current index may call it one.
	strong bool
	// explicit reports syntax that can only be naming a symbol, a declaration keyword or a
	// call of a CamelCase name: the one shape a stale index may route before indexing it,
	// since it is the name a branch is adding. A bare CamelCase word is as often a string
	// constant's value or a hook event name.
	explicit bool
	how      string // the syntax that decided it, said in the deny
}

// declLookupRe is a declaration lookup in the languages a symbol index covers: the keyword
// is what separates a lookup from prose.
// Buzz's `fun`, `object` and `protocol` are absent: no symbol index reads Buzz, so refs
// would answer with a Go or TypeScript symbol that merely shares the name.
var declLookupRe = regexp.MustCompile(`^(?:export |pub |async )?(?:func|type|class|def|function|fn|interface|struct|enum|trait|const|var|let) (?:\\?\([^()]*\\?\) ?)?([A-Za-z_][A-Za-z0-9_]*)(?: (?:struct|interface)\b.*)?$`)

// fileExts are the trailing segments that make `a.b` a file name rather than a member.
var fileExts = map[string]bool{
	"go": true, "buzz": true, "md": true, "yaml": true, "yml": true, "json": true, "jsonl": true, "ts": true,
	"tsx": true, "js": true, "toml": true, "txt": true, "sh": true, "py": true, "css": true, "html": true,
	"lock": true, "mod": true, "sum": true, "proto": true, "log": true, "png": true, "svg": true,
}

// searchAlts splits c's patterns into alternatives and classifies each. A pattern whose
// alternation sits inside a group is one regular expression, so it stays whole: splitting
// it would read `kg\.\(AddNode` as a name.
func searchAlts(c hint.Invocation) []searchAlt {
	mode := searchMode(c)
	word := hasFlag(c.Args, 'w', "word-regexp")
	var out []searchAlt
	for _, p := range hint.Patterns(c) {
		parts := splitAlternation(p, mode)
		if len(parts) > 1 && slices.ContainsFunc(parts, func(a string) bool { return !groupsBalanced(a, mode) }) {
			parts = []string{p}
		}
		for _, a := range parts {
			out = append(out, classifyAlternative(a, mode, word))
		}
	}
	return out
}

// groupsBalanced reports whether alt opens as many regex groups as it closes, ignoring a
// trailing call paren, which is a literal the name is followed by.
func groupsBalanced(alt string, mode regexMode) bool {
	open, closeTok := "(", ")"
	if mode == regexBasic {
		open, closeTok = `\(`, `\)`
	}
	if mode == regexFixed {
		return true
	}
	trimmed := strings.TrimSuffix(strings.TrimSuffix(alt, `\(`), "(")
	depth := 0
	for i := 0; i < len(trimmed); i++ {
		switch {
		case mode != regexBasic && trimmed[i] == '\\':
			i++
		case strings.HasPrefix(trimmed[i:], open):
			depth++
			i += len(open) - 1
		case strings.HasPrefix(trimmed[i:], closeTok):
			depth--
			i += len(closeTok) - 1
		}
	}
	return depth == 0
}

// lowerWordRe is a single lowercase word, the declaration names refs cannot pin to one.
var lowerWordRe = regexp.MustCompile(`^[a-z]+$`)

// camelIdentRe is a CamelCase identifier: a lowercase letter or digit followed by a capital.
var camelIdentRe = regexp.MustCompile(`^[A-Za-z0-9]*[a-z0-9][A-Z][A-Za-z0-9]*$`)

// stdlibQualifiers are the standard-library packages and globals a member access through
// them selects: the name belongs to the language, and refs would resolve a workspace symbol
// that merely shares it.
var stdlibQualifiers = map[string]bool{
	"os": true, "io": true, "fmt": true, "strings": true, "strconv": true, "bytes": true, "filepath": true,
	"path": true, "time": true, "json": true, "http": true, "url": true, "context": true, "errors": true,
	"sort": true, "sync": true, "atomic": true, "regexp": true, "exec": true, "slices": true, "maps": true,
	"bufio": true, "log": true, "slog": true, "math": true, "rand": true, "reflect": true, "runtime": true,
	"signal": true, "syscall": true, "testing": true, "unicode": true, "utf8": true, "hex": true,
	"base64": true, "net": true, "ioutil": true, "fs": true, "template": true, "flag": true,
	"ast": true, "token": true, "parser": true, "printer": true, "scanner": true, "cmp": true, "iter": true,
	"binary": true, "big": true, "bits": true, "sha256": true, "crypto": true, "tls": true, "gzip": true,
	"zip": true, "tar": true, "csv": true, "xml": true, "html": true, "mime": true, "embed": true,
	"unsafe": true, "httptest": true, "fstest": true,
	"Object": true, "Math": true, "JSON": true, "console": true, "process": true, "Promise": true,
}

// memberName splits a qualified name, `opts.Jobs`, `\.Member` or `s.lim.Acquire`, into the
// segment before the member and the member, reporting false for anything else, a file name
// such as `queue.yaml` among them.
func memberName(core string) (qualifier, member string, ok bool) {
	s := strings.ReplaceAll(core, `\.`, ".")
	if !strings.Contains(s, ".") {
		return "", "", false
	}
	segs := strings.Split(strings.TrimPrefix(s, "."), ".")
	last := segs[len(segs)-1]
	if fileExts[strings.ToLower(last)] {
		return "", "", false
	}
	for _, seg := range segs {
		if seg == "" || !segmentRe.MatchString(seg) {
			return "", "", false
		}
	}
	if len(segs) > 1 {
		qualifier = segs[len(segs)-2]
	}
	return qualifier, last, true
}

var segmentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
