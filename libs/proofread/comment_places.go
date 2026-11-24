package proofread

import (
	"path"
	"regexp"
	"strings"
	"unicode"
)

// SourceLanguage names a language whose comments [KindCodeComment] reads.
type SourceLanguage string

const (
	// LanguageGo reads a .go file: its doc comments by Go's exported names.
	LanguageGo SourceLanguage = "go"
	// LanguageTypeScript is TypeScript, .tsx included.
	LanguageTypeScript SourceLanguage = "ts"
	// LanguageBuzz reads a .buzz file, where export marks the public API.
	LanguageBuzz SourceLanguage = "buzz"
)

// PathLanguage returns the language p's extension names, and false for a
// path in none of them.
func PathLanguage(p string) (SourceLanguage, bool) {
	switch path.Ext(p) {
	case ".go":
		return LanguageGo, true
	case ".ts", ".tsx", ".mts", ".cts":
		return LanguageTypeScript, true
	case ".buzz":
		return LanguageBuzz, true
	}

	return "", false
}

// ParseLanguage returns the language name names, and false for any other.
func ParseLanguage(name string) (SourceLanguage, bool) {
	switch l := SourceLanguage(name); l {
	case LanguageGo, LanguageTypeScript, LanguageBuzz:
		return l, true
	}

	return "", false
}

// codePlace says where a comment block sits, which decides its budget.
type codePlace string

const (
	// codeTrailing follows code on its line.
	codeTrailing codePlace = "trailing"
	// codeInline stands on its own lines inside a body, or floats between
	// declarations with no declaration under it.
	codeInline codePlace = "inline"
	// codeDoc sits directly above an unexported declaration or member.
	codeDoc codePlace = "doc"
	// codeExportedDoc sits directly above an exported declaration or member.
	codeExportedDoc codePlace = "exported-doc"
	// codeHeader heads the file: above the package clause, or above the first
	// code with a blank line between.
	codeHeader codePlace = "header"
)

// codeComment is one comment block of a source file.
type codeComment struct {
	place codePlace
	// start is the 1-based line the block opens on.
	start int
	// text holds one entry per source line, delimiters gone. A directive, a
	// banner and a marked paragraph are blanked: no rule reads them.
	text string
	// banners are the 0-based lines of text that were banners.
	banners []int
	// next is the code on the line under an inline block.
	next string
	// idents are the identifiers the file's code spells, shared by every block.
	idents map[string]bool
}

// codeFile is what [readCode] finds in a source file.
type codeFile struct {
	comments  []codeComment
	generated bool
}

// codeLine is one source line split into its code and its comment.
type codeLine struct {
	indent string
	code   string
	// comment is the text after a line comment's `//`, set when hasLine is.
	comment string
	hasLine bool
	// block is set on a line that only holds part of a block comment, and
	// body is that part with the delimiters gone.
	block bool
	body  string
}

var (
	generatedHeader = regexp.MustCompile(`^ ?Code generated .* DO NOT EDIT\.$`)

	goDirective   = regexp.MustCompile(`^(?:go:|line |export |extern |nolint| \+build )`)
	tsDirective   = regexp.MustCompile(`^(?:/ ?<|\s*(?:eslint-|@ts-|prettier-ignore|biome-ignore))`)
	buzzDirective = regexp.MustCompile(`^ magus-guard-(?:template|coverage): `)

	goName        = regexp.MustCompile(`^(?:func\s+(?:\([^)]*\)\s*)?|(?:type|var|const)\s+)?([A-Za-z_]\w*)`)
	goContainer   = regexp.MustCompile(`^(?:(?:var|const|type|import)\s*\($|type\s+\w+.*\b(?:struct|interface)\s*\{$|(?:\w+\s+)?(?:struct|interface)\s*\{$)`)
	tsContainer   = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:abstract\s+)?(?:class|interface|enum|namespace|module)\b[^=]*\{$|^(?:export\s+)?type\s+\w+.*=\s*\{$`)
	buzzContainer = regexp.MustCompile(`^(?:export\s+)?(?:object|enum|protocol)\b`)
	tsPrivate     = regexp.MustCompile(`^(?:private|protected|#)`)
	bannerLine    = regexp.MustCompile(`^[-=*_~#/+]{3,}$|^[-=*_~#/+]{3,}\s.*\s[-=*_~#/+]{3,}$|^[-=*_~#/+]{3,}\s+\S`)
	identPattern  = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// readCode splits src into lines and finds every comment block in it, placed.
// A generated file holds none: its generator owns the words.
func readCode(src string, lang SourceLanguage) codeFile {
	lines := splitCode(src)
	idents := map[string]bool{}

	for _, ln := range lines {
		for _, id := range identPattern.FindAllString(stripStrings(ln.code), -1) {
			idents[id] = true
		}
	}

	var (
		f       codeFile
		frames  []frame
		seen    bool
		pending []int
	)

	flush := func(next int) {
		if len(pending) == 0 {
			return
		}

		c := buildBlock(lines, pending, lang)
		if c.generated {
			f.generated = true
		}

		c.comment.idents = idents
		c.comment.place, c.comment.next = placeBlock(lines, next, frames, seen, lang)

		if strings.TrimSpace(c.comment.text) != "" || len(c.comment.banners) > 0 {
			f.comments = append(f.comments, c.comment)
		}

		pending = nil
	}

	for i, ln := range lines {
		standalone := strings.TrimSpace(ln.code) == "" && (ln.hasLine || ln.block)

		if standalone {
			if len(pending) > 0 && !sameBlock(lines[pending[len(pending)-1]], ln) {
				flush(-1)
			}

			pending = append(pending, i)

			continue
		}

		if strings.TrimSpace(ln.code) == "" {
			flush(-1)

			continue
		}

		flush(i)

		if ln.hasLine {
			c := codeComment{place: codeTrailing, start: i + 1, text: ln.comment, idents: idents}
			if !codeDirective(lang, ln.comment) && strings.TrimSpace(c.text) != "" {
				f.comments = append(f.comments, c)
			}
		}

		frames = advanceFrames(frames, ln, lang)
		seen = true
	}

	flush(-1)

	if f.generated {
		return codeFile{generated: true}
	}

	return f
}

// sameBlock reports whether cur continues the block prev is in: a block
// comment's lines always do, and line comments do at the same indent.
func sameBlock(prev, cur codeLine) bool {
	if prev.block || cur.block {
		return prev.block && cur.block
	}

	return prev.indent == cur.indent
}

type builtBlock struct {
	comment   codeComment
	generated bool
}

// buildBlock joins the comment lines at idx into one block. A directive line,
// a banner and a paragraph a marker opens are blanked.
func buildBlock(lines []codeLine, idx []int, lang SourceLanguage) builtBlock {
	var (
		out      builtBlock
		text     = make([]string, len(idx))
		isBlock  = lines[idx[0]].block
		rawBlock []string
	)

	out.comment.start = idx[0] + 1

	for j, i := range idx {
		if isBlock {
			rawBlock = append(rawBlock, lines[i].body)

			continue
		}

		raw := lines[i].comment
		if generatedHeader.MatchString(raw) {
			out.generated = true
		}

		if codeDirective(lang, raw) {
			continue
		}

		text[j] = strings.TrimPrefix(raw, " ")
	}

	if isBlock {
		text = strings.Split(normalizeComment(strings.Join(rawBlock, "\n")), "\n")
	}

	for j, t := range text {
		if bannerLine.MatchString(strings.TrimSpace(t)) {
			out.comment.banners = append(out.comment.banners, j)
			text[j] = ""
		}
	}

	blankMarked(text)
	out.comment.text = strings.Join(text, "\n")

	return out
}

// blankMarked blanks each paragraph a marker such as TODO opens. A marker's
// note is never a finding, and only its own paragraph is exempt.
func blankMarked(text []string) {
	inMarked, opens := false, true

	for j, t := range text {
		if strings.TrimSpace(t) == "" {
			inMarked, opens = false, true

			continue
		}

		if opens {
			inMarked = marked(t)
		}

		opens = false

		if inMarked {
			text[j] = ""
		}
	}
}

// codeDirective reports whether the text after a `//` is one a tool reads.
func codeDirective(lang SourceLanguage, comment string) bool {
	switch lang {
	case LanguageGo:
		return goDirective.MatchString(comment)
	case LanguageTypeScript:
		return tsDirective.MatchString(comment)
	case LanguageBuzz:
		return buzzDirective.MatchString(comment)
	}

	return false
}

// frame is one open bracket. A container holds declarations, as a struct, a
// class or a grouped const does; any other frame is a body.
type frame struct {
	container, exported bool
}

// advanceFrames pushes each opener and pops each closer on ln's code. Every
// opener on a line takes the line's own kind.
func advanceFrames(frames []frame, ln codeLine, lang SourceLanguage) []frame {
	code := strings.TrimSpace(stripStrings(ln.code))
	fr := frame{container: isContainer(lang, code), exported: strings.HasPrefix(code, "export ") || exportedGo(lang, code)}

	for _, r := range code {
		switch r {
		case '{', '(', '[':
			frames = append(frames, fr)
		case '}', ')', ']':
			if len(frames) > 0 {
				frames = frames[:len(frames)-1]
			}
		}
	}

	return frames
}

func isContainer(lang SourceLanguage, code string) bool {
	switch lang {
	case LanguageGo:
		return goContainer.MatchString(code)
	case LanguageTypeScript:
		return tsContainer.MatchString(code)
	case LanguageBuzz:
		return buzzContainer.MatchString(code)
	}

	return false
}

func exportedGo(lang SourceLanguage, code string) bool {
	if lang != LanguageGo {
		return false
	}

	m := goName.FindStringSubmatch(code)

	return m != nil && unicode.IsUpper(rune(m[1][0]))
}

// placeBlock places the block ending above line next, -1 for a blank line.
// It also returns the code an inline block sits on.
func placeBlock(lines []codeLine, next int, frames []frame, seen bool, lang SourceLanguage) (codePlace, string) {
	code := ""
	if next >= 0 {
		code = strings.TrimSpace(lines[next].code)
	}

	switch {
	case lang == LanguageGo && strings.HasPrefix(code, "package "):
		return codeHeader, ""
	case !seen && (next < 0 || strings.HasPrefix(code, "namespace ") || strings.HasPrefix(code, "import ")):
		return codeHeader, ""
	case next < 0:
		return codeInline, ""
	}

	inBody := len(frames) > 0 && !frames[len(frames)-1].container
	if inBody {
		return codeInline, code
	}

	if exportedDecl(lang, code, frames) {
		return codeExportedDoc, ""
	}

	return codeDoc, ""
}

// exportedDecl reports whether the declaration or member code opens is part
// of its package's or module's API.
func exportedDecl(lang SourceLanguage, code string, frames []frame) bool {
	inContainer := len(frames) > 0

	switch lang {
	case LanguageGo:
		m := goName.FindStringSubmatch(strings.TrimPrefix(code, "*"))

		return m != nil && unicode.IsUpper(rune(m[1][0]))
	case LanguageTypeScript:
		if inContainer {
			return frames[len(frames)-1].exported && !tsPrivate.MatchString(code)
		}

		return strings.HasPrefix(code, "export ")
	case LanguageBuzz:
		if inContainer {
			return frames[len(frames)-1].exported
		}

		return strings.HasPrefix(code, "export ")
	}

	return false
}

// splitCode splits src into lines, each cut into its code and its comment.
// Strings, Go raw strings and template literals are code, whatever they hold.
func splitCode(src string) []codeLine {
	raw := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	out := make([]codeLine, len(raw))

	inBlock, inRaw := false, false

	for n, line := range raw {
		var code, body strings.Builder

		ln := codeLine{indent: line[:len(line)-len(strings.TrimLeft(line, " \t"))]}
		blockOnly := inBlock

		for i := 0; i < len(line); i++ {
			c := line[i]

			switch {
			case inBlock:
				if strings.HasPrefix(line[i:], "*/") {
					inBlock = false
					i++

					continue
				}

				body.WriteByte(c)
			case inRaw:
				code.WriteByte(c)

				if c == '\\' && i+1 < len(line) {
					code.WriteByte(line[i+1])
					i++
				} else if c == '`' {
					inRaw = false
				}
			case strings.HasPrefix(line[i:], "//"):
				ln.comment, ln.hasLine = line[i+2:], true
				i = len(line)
			case strings.HasPrefix(line[i:], "/*"):
				if strings.TrimSpace(code.String()) == "" {
					blockOnly = true
				}

				inBlock = true
				i++
			case c == '`':
				inRaw = true

				code.WriteByte(c)
			case c == '"' || c == '\'':
				end := closeQuote(line, i)
				code.WriteString(line[i:end])
				i = end - 1
			default:
				code.WriteByte(c)
			}
		}

		ln.code = code.String()
		if blockOnly && strings.TrimSpace(ln.code) == "" && !ln.hasLine {
			ln.block, ln.body = true, body.String()
		}

		out[n] = ln
	}

	return out
}

// closeQuote returns the index just past the string that opens at line[at],
// or the line's end for one left open.
func closeQuote(line string, at int) int {
	q := line[at]

	for i := at + 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case q:
			return i + 1
		}
	}

	return len(line)
}

// stripStrings blanks the strings of code so their text reads as no
// identifier and no bracket.
func stripStrings(code string) string {
	var b strings.Builder

	for i := 0; i < len(code); i++ {
		c := code[i]
		if c != '"' && c != '\'' && c != '`' {
			b.WriteByte(c)

			continue
		}

		end := closeQuote(code, i)
		b.WriteString(`""`)
		i = end - 1
	}

	return b.String()
}
