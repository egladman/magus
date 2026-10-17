package knowledge

import "strings"

// SignatureMatches compares declared, a declaration written by hand or copied from an index,
// with the signature the index holds for symbol node id, and returns that indexed signature.
// ok is false when the node is not in the graph or carries no signature, so nothing was
// compared.
//
// Go and Buzz declarations compare by shape: kind, name, receiver, parameter types and
// results, so a renamed parameter or a receiver written `(g Goal)` against the index's
// `(Goal)` still matches. A pointer receiver compares as its base type, since the index's
// rendering of one is not pinned. Every other language compares the text with whitespace
// collapsed.
func (g *Graph) SignatureMatches(id, declared string) (indexed string, match, ok bool) {
	n, found := g.node(id)
	if !found {
		return "", false, false
	}
	indexed = n.Attrs[AttrSignature]
	if indexed == "" {
		return "", false, false
	}
	return indexed, sameSignature(n.Attrs[attrLanguage], indexed, declared), true
}

func sameSignature(language, a, b string) bool {
	a, b = strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " ")
	if a == b {
		return true
	}
	var shape func(string) (string, bool)
	switch language {
	case "go":
		shape = goSignatureShape
	case "buzz":
		shape = buzzSignatureShape
	default:
		return false
	}
	sa, okA := shape(a)
	sb, okB := shape(b)
	return okA && okB && sa == sb
}

// goSignatureShape renders a func or method declaration as the parts a caller depends on,
// or false for anything else: a type, value or field declaration has no parameter names to
// forgive, so its text is its shape.
func goSignatureShape(sig string) (string, bool) {
	rest, ok := strings.CutPrefix(sig, "func ")
	if !ok {
		return "", false
	}
	receiver := ""
	if strings.HasPrefix(rest, "(") {
		end := matchingClose(rest, 0)
		if end < 0 {
			return "", false
		}
		fields := strings.Fields(rest[1:end])
		if len(fields) == 0 {
			return "", false
		}
		receiver = strings.TrimPrefix(fields[len(fields)-1], "*")
		rest = strings.TrimLeft(rest[end+1:], ". ")
	}
	name := rest
	if i := strings.IndexAny(rest, "(["); i >= 0 {
		name = rest[:i]
	}
	params, results, ok := goFuncParts("func " + rest)
	if !ok {
		return "", false
	}
	types := make([]string, len(params))
	for i, p := range params {
		types[i] = p.Type
	}
	return strings.Join([]string{"func", receiver, strings.TrimSpace(name), strings.Join(types, ", "), goResultTypes(results)}, "|"), true
}

// goResultTypes is a result list with its names dropped, so `(n int, err error)` and
// `(int, error)` are one shape.
func goResultTypes(results string) string {
	if strings.HasPrefix(results, "(") && matchingClose(results, 0) == len(results)-1 {
		results = results[1 : len(results)-1]
	}
	params := goParams(results)
	out := make([]string, len(params))
	for i, p := range params {
		out[i] = p.Type
	}
	return strings.Join(out, ", ")
}

// buzzSignatureShape renders a Buzz function declaration as its name, parameter types and
// everything after its parameter list, or false for any other declaration.
func buzzSignatureShape(sig string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(sig, "extern "), "fun ")
	if !ok {
		return "", false
	}
	open := strings.IndexByte(rest, '(')
	if open < 0 {
		return "", false
	}
	end := matchingClose(rest, open)
	if end < 0 {
		return "", false
	}
	var types []string
	for list := rest[open+1 : end]; list != ""; {
		part, more, _ := cutTopLevel(list, ',')
		list = more
		_, typ, _ := strings.Cut(part, ":")
		typ, _, _ = strings.Cut(typ, "=")
		types = append(types, strings.TrimSpace(typ))
	}
	return strings.Join([]string{"fun", strings.TrimSpace(rest[:open]), strings.Join(types, ", "), strings.TrimSpace(rest[end+1:])}, "|"), true
}
