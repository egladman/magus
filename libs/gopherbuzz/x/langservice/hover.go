package langservice

import (
	"fmt"
	"slices"
	"strings"
)

// Hover is what shows when the pointer rests on a symbol: a signature or
// qualified name, and its doc.
type Hover struct {
	Title string `json:"title"`
	Doc   string `json:"doc,omitempty"`
}

// Signature is call-signature help: the callee's signature and its doc.
type Signature struct {
	Label string `json:"label"`
	Doc   string `json:"doc,omitempty"`
}

// Hover describes the symbol at byte offset in src. It knows module members
// such as `fs\glob`, module names, the file's top-level declarations and
// builtins; ok is false for anything else. It reads the raw text, so it answers
// on source the parser would reject. An offset outside src is clamped.
func (s *Service) Hover(src string, offset int) (Hover, bool) {
	start, end := identSpan(src, clampOffset(src, offset))
	word := src[start:end]
	if word == "" {
		return Hover{}, false
	}

	if start > 0 && src[start-1] == '\\' {
		bs := start - 1
		for bs > 0 && isIdentByte(src[bs-1]) {
			bs--
		}
		base := src[bs : start-1]
		if base == "" {
			return Hover{}, false
		}
		name, ok := s.resolveModule(base, src)
		if !ok {
			return Hover{}, false
		}
		m, ok := s.member(name, word)
		if !ok {
			return Hover{}, false
		}
		return Hover{Title: qualified(name, m), Doc: m.Doc}, true
	}
	if start > 0 && src[start-1] == '.' {
		return Hover{}, false // a member of a value: no host docs describe it
	}

	if mod, ok := s.module(word); ok {
		return Hover{Title: "module " + word, Doc: mod.Doc}, true
	}
	for _, sym := range scanSymbols(src) {
		if sym.Name != word {
			continue
		}
		if sym.Sig != "" {
			return Hover{Title: sym.Sig}, true
		}
		return Hover{Title: fmt.Sprintf("%s %s", string(sym.Kind), sym.Name)}, true
	}
	if slices.Contains(builtins, word) {
		return Hover{Title: word + "(...)", Doc: "Buzz builtin"}, true
	}
	return Hover{}, false
}

// Signature gives help for the call whose argument list encloses byte offset in
// src. ok is false outside a call, or when the callee is not a module function,
// an in-file function or a builtin. It balances parens back to the innermost
// open one, so it answers on half-typed source. An offset outside src is
// clamped.
func (s *Service) Signature(src string, offset int) (Signature, bool) {
	offset = clampOffset(src, offset)

	// A statement boundary before an open paren means the cursor is in no call.
	open := -1
	depth := 0
scan:
	for i := offset - 1; i >= 0; i-- {
		switch src[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				open = i
				break scan
			}
			depth--
		case ';', '{', '}':
			break scan
		}
	}
	if open < 0 {
		return Signature{}, false
	}

	// The walk spans dots too, so a method on a value (`xs.len(`) is not read as
	// the builtin it shares a name with.
	j := open
	for j > 0 && isSpace(src[j-1]) {
		j--
	}
	end := j
	for j > 0 && (isIdentByte(src[j-1]) || src[j-1] == '.' || src[j-1] == '\\') {
		j--
	}
	callee := src[j:end]
	if callee == "" || strings.LastIndexByte(callee, '.') > strings.LastIndexByte(callee, '\\') {
		return Signature{}, false
	}

	if sep := strings.LastIndexByte(callee, '\\'); sep >= 0 {
		name, ok := s.resolveModule(callee[:sep], src)
		if !ok {
			return Signature{}, false
		}
		m, ok := s.member(name, callee[sep+1:])
		if !ok || m.Kind != MemberFunction {
			return Signature{}, false
		}
		return Signature{Label: qualified(name, m), Doc: m.Doc}, true
	}

	for _, sym := range scanSymbols(src) {
		if sym.Name == callee && sym.Kind == symFunction {
			return Signature{Label: sym.Sig}, true
		}
	}
	if slices.Contains(builtins, callee) {
		return Signature{Label: callee + "(...)"}, true
	}
	return Signature{}, false
}

// member finds one member of a table module; a module that does not parse has
// none.
func (s *Service) member(module, name string) (Member, bool) {
	members, err := s.members(module)
	if err != nil {
		return Member{}, false
	}
	for _, m := range members {
		if m.Name == name {
			return m, true
		}
	}
	return Member{}, false
}
