package emit

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Render writes a template as the file at path. The path's extension picks the
// language through index (spells.EncodingIndex). A Problem's Line counts the
// template file's header as line 1; its Column counts static text only. The text
// is returned only when there are no problems.
func Render(t types.Template, path string, index map[string]spells.LanguageEncodings) (string, Problems) {
	r := &renderer{path: path, index: index, enc: newEncoder()}
	out := r.render(t)
	if len(r.probs) > 0 {
		return "", r.probs
	}
	return out, nil
}

type renderer struct {
	path  string
	index map[string]spells.LanguageEncodings
	enc   *encoder
	probs Problems
}

type place struct{ line, column int }

// places starts at line 2 because output follows the header line of the file.
func places(t types.Template) []place {
	out := make([]place, 0, len(t.Interpolations))
	line, col := 2, 1
	for i := range t.Interpolations {
		for j := 0; j < len(t.Strings[i]); j++ {
			if t.Strings[i][j] == '\n' {
				line++
				col = 1
			} else {
				col++
			}
		}
		out = append(out, place{line, col})
	}
	return out
}

type refuse func(types.DiagnosticCode, string)

func (r *renderer) render(t types.Template) string {
	if len(t.Strings) != len(t.Interpolations)+1 {
		r.probs = append(r.probs, Problem{types.EmitBadShape, 0, 0, fmt.Sprintf(
			"a template with %d holes holds %d strings; this one holds %d",
			len(t.Interpolations), len(t.Interpolations)+1, len(t.Strings))})
		return ""
	}
	at := places(t)
	var out []byte
	skipNL := false
	for i, hole := range t.Interpolations {
		s := t.Strings[i]
		if skipNL {
			s = strings.TrimPrefix(s, "\n")
			skipNL = false
		}
		out = append(out, s...)

		lineStart := bytes.LastIndexByte(out, '\n') + 1
		prefix := string(out[lineStart:])
		alone := isBlank(prefix) && endsLine(t, i)
		fail := func(code types.DiagnosticCode, msg string) {
			r.probs = append(r.probs, Problem{code, at[i].line, at[i].column, msg})
		}

		text, drop, ok := r.hole(hole, prefix, alone, at[i], fail)
		switch {
		case !ok:
		case drop:
			out = out[:lineStart]
			skipNL = true
		default:
			out = append(out, text...)
		}
	}
	last := t.Strings[len(t.Strings)-1]
	if skipNL {
		last = strings.TrimPrefix(last, "\n")
	}
	return string(append(out, last...))
}

// endsLine reports whether nothing but the line feed follows hole i on its line.
func endsLine(t types.Template, i int) bool {
	next := t.Strings[i+1]
	if next == "" {
		return i+1 == len(t.Strings)-1
	}
	return next[0] == '\n'
}

func isBlank(s string) bool { return strings.Trim(s, " \t") == "" }

// hole renders one interpolation. drop asks the caller to remove the hole's line.
func (r *renderer) hole(h types.Interpolation, prefix string, alone bool, at place, fail refuse) (text string, drop, ok bool) {
	if h.Expression == "" {
		h.Expression = "an emit\\hole value with no expression"
	}
	switch h.Conversion {
	case "":
	case "s":
		s, isScalar := stringForm(h.Value)
		if !isScalar {
			fail(types.EmitWrongKind, fmt.Sprintf("%s is %s, which !s cannot write as text: pass a str, an int, a double or a bool", h.Expression, kindName(h.Value)))
			return "", false, false
		}
		h.Value = s
	default:
		fail(types.EmitConversion, fmt.Sprintf("!%s is not supported: Buzz has no repr or ascii; write !s or drop the conversion", h.Conversion))
		return "", false, false
	}
	switch h.FormatSpec {
	case "":
		fail(types.EmitNoEncoding, fmt.Sprintf("%s names no encoding; write %s:ident, :string, :block or :raw", h.Expression, h.Expression))
		return "", false, false
	case specBlock:
		return r.block(h, prefix, alone, at, fail)
	case specRaw:
		s, ok := asString(h, fail)
		if !ok {
			return "", false, false
		}
		switch {
		case s == "":
			fail(types.EmitRefusedValue, fmt.Sprintf("%s is empty and :raw refuses that; pass emit\\text(\"\") to a :block hole for nothing", h.Expression))
			return "", false, false
		case strings.Contains(s, "\n"):
			fail(types.EmitRefusedValue, fmt.Sprintf("%s holds a line feed and :raw refuses that; pass a template to a :block hole", h.Expression))
			return "", false, false
		}
		return s, false, true
	}
	enc, found := r.lookup(h, fail)
	if !found {
		return "", false, false
	}
	s, ok := asString(h, fail)
	if !ok {
		return "", false, false
	}
	out, code, msg := r.enc.encode(enc, s)
	if msg != "" {
		fail(code, fmt.Sprintf("%s: %s", h.Expression, msg))
		return "", false, false
	}
	return out, false, true
}

// asString is the kind check of every encoding but block.
func asString(h types.Interpolation, fail refuse) (string, bool) {
	if s, isStr := h.Value.(string); isStr {
		return s, true
	}
	hint := "pass a str"
	if _, scalar := stringForm(h.Value); scalar {
		hint = fmt.Sprintf("write {{%s!s:%s}}", h.Expression, h.FormatSpec)
	}
	if _, isTemplate := asTemplate(h.Value); isTemplate {
		hint = "write the hole as :block"
	}
	fail(types.EmitWrongKind, fmt.Sprintf("%s is %s and :%s takes a str; %s", h.Expression, kindName(h.Value), h.FormatSpec, hint))
	return "", false
}

func asTemplate(v any) (types.Template, bool) {
	switch t := v.(type) {
	case types.Template:
		return t, true
	case *types.Template:
		if t != nil {
			return *t, true
		}
	}
	return types.Template{}, false
}

func (r *renderer) lookup(h types.Interpolation, fail refuse) (spells.Encoding, bool) {
	ext := strings.ToLower(filepath.Ext(r.path))
	lang, claimed := r.index[ext]
	if !claimed {
		fail(types.EmitUnknownEncoding, fmt.Sprintf("no spell declares a language for %q (%s), so :%s has no meaning; block and raw exist. add the extension to a spell's mgs_getLanguage, or encode in Buzz and name raw", ext, r.path, h.FormatSpec))
		return spells.Encoding{}, false
	}
	names := []string{specBlock, specRaw}
	for _, e := range lang.Encodings {
		if e.Name == h.FormatSpec {
			return e, true
		}
		names = append(names, e.Name)
	}
	sort.Strings(names)
	owner := "spell " + lang.Spell
	if lang.Spell == "" {
		owner = "the spell declaring " + lang.Language
	}
	fail(types.EmitUnknownEncoding, fmt.Sprintf("%s declares no %s encoding for %s (%s); %s exist. add it to the spell's mgs_getLanguage, or encode in Buzz and name raw",
		owner, h.FormatSpec, ext, r.path, strings.Join(names, ", ")))
	return spells.Encoding{}, false
}

// block renders a nested template with this output's encodings and carries the
// hole line's indentation onto every line after the first.
func (r *renderer) block(h types.Interpolation, prefix string, alone bool, at place, fail refuse) (string, bool, bool) {
	nested, isTemplate := asTemplate(h.Value)
	if !isTemplate {
		fail(types.EmitWrongKind, fmt.Sprintf("%s is %s and :block takes a template; wrap it with emit\\text", h.Expression, kindName(h.Value)))
		return "", false, false
	}
	mark := len(r.probs)
	body := r.render(nested)
	if len(r.probs) > mark {
		r.wrapNested(mark, nested, h, at)
		return "", false, false
	}
	body = strings.TrimSuffix(body, "\n")
	lines := strings.Split(body, "\n")
	switch {
	case body == "":
		return "", alone, true
	case len(lines) == 1:
		return body, false, true
	case !alone:
		fail(types.EmitBlockShape, fmt.Sprintf("%s holds %d lines and shares line %d; put the hole on its own line", h.Expression, len(lines), at.line))
		return "", false, false
	}
	var b strings.Builder
	b.WriteString(lines[0])
	for _, l := range lines[1:] {
		b.WriteByte('\n')
		if l != "" {
			b.WriteString(prefix)
			b.WriteString(l)
		}
	}
	return b.String(), false, true
}

// Join writes parts one after another with sep between them. sep is static text,
// and an empty sep is template concatenation.
func Join(parts []types.Template, sep string) types.Template {
	out := types.Template{Strings: []string{""}}
	for i, p := range parts {
		if i > 0 {
			out.Strings[len(out.Strings)-1] += sep
		}
		if len(p.Strings) == 0 {
			continue
		}
		out.Strings[len(out.Strings)-1] += p.Strings[0]
		out.Strings = append(out.Strings, p.Strings[1:]...)
		out.Interpolations = append(out.Interpolations, p.Interpolations...)
	}
	return out
}

// wrapNested restates a nested template's problems at the hole that holds it.
func (r *renderer) wrapNested(from int, nested types.Template, h types.Interpolation, at place) {
	name := nested.Source
	if name == "" {
		name = "a template built in Buzz"
	}
	for i := from; i < len(r.probs); i++ {
		p := r.probs[i]
		where := name
		if p.Line > 0 {
			where = fmt.Sprintf("%s:%d", name, p.Line)
		}
		r.probs[i] = Problem{p.Code, at.line, at.column,
			fmt.Sprintf("in %s, held by %s: %s", where, h.Expression, p.Message)}
	}
}
