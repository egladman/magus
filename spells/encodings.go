package spells

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Encoding is one way a language writes a value into its own source, named by
// the word after ":" in an emit hole. One engine applies every encoding alike,
// so a spell adds one by writing data. For every value an encoding accepts,
// the text written must be one token of the kind its name says, meaning the
// same text.
type Encoding struct {
	// Name matches [a-z][a-z0-9-]*, is unique in the language, and is never
	// block or raw: those belong to the engine.
	Name string `json:"name,omitempty"`
	// Pattern is an RE2 expression the whole value must match. Empty accepts
	// any text.
	Pattern string `json:"pattern,omitempty"`
	// Reserved values are refused even when they match Pattern: keywords.
	Reserved []string `json:"reserved,omitempty"`
	// Open and Close are written around the encoded value.
	Open  string `json:"open,omitempty"`
	Close string `json:"close,omitempty"`
	// Escapes are tried in order at each position; the first match is
	// written. An identity escape (text "\n" to "\n") passes a control
	// through.
	Escapes []Escape `json:"escapes,omitempty"`
	// Control writes any C0 control or DEL no escape matched. It holds one
	// placeholder, {hex2}, {hex4} or {oct3}, filled with the byte's lowercase
	// digits. The rest is copied, so Rust writes `\u{{hex2}}`. Empty refuses
	// such a value.
	Control string `json:"control,omitempty"`
}

// Escape writes To wherever Text matches the value being encoded.
type Escape struct {
	// Text, not from: from is a Buzz keyword, written @"from" in a spell.
	Text string `json:"text,omitempty"`
	To   string `json:"to,omitempty"`
}

// LanguageEncodings is what an output path's extension routes to: the spell
// whose language claims the extension, and the encodings it declares, which
// may be none.
type LanguageEncodings struct {
	Spell     string     `json:"spell,omitempty"`
	Language  string     `json:"language,omitempty"`
	Encodings []Encoding `json:"encodings,omitempty"`
}

// EncodingIndex routes a file extension (lowercase, with dot) to the language
// that claims it. A language with no encodings still claims its extensions,
// so a refusal can name the spell to add one to. The first claim wins, as in
// CommentSyntaxIndex.
func EncodingIndex(list []*Spell) map[string]LanguageEncodings {
	out := map[string]LanguageEncodings{}
	for _, s := range list {
		var encs []Encoding
		if s.Syntax() != nil {
			encs = s.Syntax().Encodings
		}
		for _, ext := range s.LanguageExtensions() {
			if _, taken := out[ext]; !taken {
				out[ext] = LanguageEncodings{Spell: s.Name(), Language: s.Language(), Encodings: encs}
			}
		}
	}
	return out
}

var encodingName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// engineEncodings are the emit engine's own; a spell declaring one would
// shadow structure with language.
var engineEncodings = []string{"block", "raw"}

var controlPlaceholders = []string{"{hex2}", "{hex4}", "{oct3}"}

// ValidateEncodings checks the encodings a language declares, at load. It
// returns the first problem, naming the encoding by index and name.
func ValidateEncodings(l Language) error {
	if l.Syntax == nil || len(l.Syntax.Encodings) == 0 {
		return nil
	}
	if len(l.Extensions) == 0 {
		return fmt.Errorf("encodings without extensions route no output path, declare the extensions that are this language")
	}
	seen := map[string]bool{}
	for i, e := range l.Syntax.Encodings {
		at := fmt.Sprintf("encodings[%d] (%s)", i, e.Name)
		if err := checkEncodingName(e.Name, seen); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
		if err := checkEncodingBody(e); err != nil {
			return fmt.Errorf("%s: %w", at, err)
		}
	}
	return nil
}

func checkEncodingName(name string, seen map[string]bool) error {
	switch {
	case !encodingName.MatchString(name):
		return fmt.Errorf("name %q must match [a-z][a-z0-9-]*", name)
	case slices.Contains(engineEncodings, name):
		return fmt.Errorf("name %q is the engine's own encoding, pick another name", name)
	case seen[name]:
		return fmt.Errorf("name %q is declared twice", name)
	}
	seen[name] = true
	return nil
}

func checkEncodingBody(e Encoding) error {
	if _, err := regexp.Compile(e.Pattern); err != nil {
		return fmt.Errorf("pattern does not compile as RE2: %w", err)
	}
	for j, esc := range e.Escapes {
		if esc.Text == "" || !utf8.ValidString(esc.Text) {
			return fmt.Errorf("escapes[%d].text must be non-empty UTF-8", j)
		}
		for k, prior := range e.Escapes[:j] {
			if strings.HasPrefix(esc.Text, prior.Text) {
				return fmt.Errorf("escapes[%d].text %q is never reached: escapes[%d].text %q matches first", j, esc.Text, k, prior.Text)
			}
		}
	}
	if e.Control != "" {
		n := 0
		for _, p := range controlPlaceholders {
			n += strings.Count(e.Control, p)
		}
		if n != 1 {
			return fmt.Errorf("control %q must hold exactly one of %s", e.Control, strings.Join(controlPlaceholders, ", "))
		}
	}
	return nil
}
