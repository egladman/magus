package langservice

// Hover is the information shown when the pointer rests on a symbol: a Title (a
// signature or qualified name) and a longer Doc. A nil *Hover means nothing to show.
type Hover struct {
	Title string `json:"title"`
	Doc   string `json:"doc,omitempty"`
}

// HoverAt returns hover information for the symbol at offset in src, or nil when the
// cursor is not on a recognized symbol. It resolves module member accesses
// (`fs.glob` -> the method's signature and doc), bare module names, the file's own
// top-level declarations, and builtins. Like completion it reads the raw text, so
// it works on source the parser would reject.
func HoverAt(src string, offset int) *Hover {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}

	start, end := identSpan(src, offset)
	word := src[start:end]
	if word == "" {
		return nil
	}

	// Member access: `<base>.<word>`. Resolve base to a module and describe word.
	if start > 0 && src[start-1] == '.' {
		b := start - 1
		bs := b
		for bs > 0 && isIdentByte(src[bs-1]) {
			bs--
		}
		base := src[bs:b]
		if base != "" {
			if mod, ok := resolveModule(base, src); ok {
				return memberHover(mod, word)
			}
		}
		return nil
	}

	// A bare module name.
	if mod, ok := LookupModule(word); ok {
		return &Hover{Title: "module " + mod.Name, Doc: mod.Doc}
	}

	// A top-level declaration in this file.
	for _, s := range scanSymbols(src) {
		if s.Name != word {
			continue
		}
		if s.Sig != "" {
			return &Hover{Title: s.Sig}
		}
		return &Hover{Title: string(s.Kind) + " " + s.Name}
	}

	// A builtin global.
	for _, bfn := range builtins {
		if bfn == word {
			return &Hover{Title: word + "(...)", Doc: "Buzz builtin"}
		}
	}
	return nil
}

func memberHover(mod Module, name string) *Hover {
	for _, m := range mod.Methods {
		if m.Name == name {
			return &Hover{Title: m.Sig, Doc: m.Doc}
		}
	}
	for _, f := range mod.Fields {
		if f.Name == name {
			title := mod.Name + "." + f.Name
			if f.Type != "" {
				title += ": " + f.Type
			}
			return &Hover{Title: title, Doc: f.Doc}
		}
	}
	return nil
}

// identSpan returns the byte range of the identifier straddling or ending at
// offset, expanding in both directions over identifier bytes. An empty range
// (start == end) means the cursor is not on an identifier.
func identSpan(src string, offset int) (start, end int) {
	start, end = offset, offset
	for start > 0 && isIdentByte(src[start-1]) {
		start--
	}
	for end < len(src) && isIdentByte(src[end]) {
		end++
	}
	return start, end
}
