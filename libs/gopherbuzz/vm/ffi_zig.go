package vm

// Zig-declaration dialect for zdef(), for upstream-Buzz source parity.
//
// Upstream Buzz's zdef takes Zig declarations (`fn add(a: c_int, b: c_int)
// c_int;`); gopherbuzz is C-ABI native and historically took C prototypes.
// Both dialects describe the same call boundary, so they compile to the same
// CType model and bind through the same provider. builtinZdef sniffs the
// dialect per call (looksLikeZigDecls), keeping every existing C-decl script
// working while letting upstream-style declarations run unchanged.
//
// The subset matches what the C dialect models: scalar/pointer/string
// parameters and returns, the magic CGPoint/CGRect two- and four-double
// struct returns, and — as the Zig spelling of our `extern` extension —
// `var name: T;` data-symbol declarations (`anyopaque` without a pointer
// yields the symbol's address, the &-style form).

import (
	"fmt"
	"strings"
)

// looksLikeZigDecls reports whether src reads as Zig declarations: any
// declaration starting with `fn `, or a var/const declaration whose type
// follows a colon (C externs put the type before the name).
func looksLikeZigDecls(src string) bool {
	for _, decl := range strings.Split(src, ";") {
		decl = strings.TrimSpace(decl)
		if strings.HasPrefix(decl, "fn ") {
			return true
		}
		if (strings.HasPrefix(decl, "var ") || strings.HasPrefix(decl, "const ")) &&
			strings.Contains(decl, ":") {
			return true
		}
	}
	return false
}

// zigTypeToCType maps a Zig type token to the FFI type model.
func zigTypeToCType(tok string) CType {
	tok = strings.TrimSpace(tok)
	tok = strings.TrimPrefix(tok, "?") // optional pointer: same ABI slot
	switch tok {
	case "void":
		return CVoid
	case "bool":
		return CBool
	case "i8", "i16", "i32", "i64", "isize",
		"c_short", "c_int", "c_long", "c_longlong", "c_char":
		return CInt
	case "u8", "u16", "u32", "u64", "usize",
		"c_ushort", "c_uint", "c_ulong", "c_ulonglong":
		return CUint
	case "f32":
		return CFloat
	case "f64", "c_longdouble":
		return CDouble
	case "CGPoint", "NSPoint", "CGSize", "NSSize":
		return CPoint2D
	case "CGRect", "NSRect":
		return CRect4D
	}
	// Null-terminated u8 pointers are strings; every other pointer shape
	// ([*]T, *T, **T, [*c]T) is an opaque address.
	if strings.HasPrefix(tok, "[*:0]") {
		return CCharPtr
	}
	if strings.HasPrefix(tok, "*") || strings.HasPrefix(tok, "[*") {
		return CVoidPtr
	}
	return CUnsupported
}

// ParseZigDecls parses Zig-style declarations into the same signatures the
// C-decl parser produces. Exported for the same embedder reasons as
// ParseCDecls.
func ParseZigDecls(src string) ([]CFuncSig, error) {
	sigs := make([]CFuncSig, 0, strings.Count(src, ";")+1)
	structs := map[string][]string{}
	for _, part := range strings.Split(src, ";") {
		part = strings.TrimSpace(part)
		part = stripZigComments(part)
		if part == "" {
			continue
		}
		sig, err := parseSingleZigDecl(part, structs)
		if err != nil {
			return nil, err
		}
		if sig.IsStruct {
			structs[sig.Name] = sig.FieldTypeNames
		}
		sigs = append(sigs, sig)
	}
	return sigs, nil
}

// stripZigComments drops `// …` line comments (multiline backtick decl blocks
// carry them, upstream-style).
func stripZigComments(src string) string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		if strings.TrimSpace(line) != "" {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return strings.Join(out, " ")
}

func parseSingleZigDecl(src string, structs map[string][]string) (CFuncSig, error) {
	if rest, ok := strings.CutPrefix(src, "fn "); ok {
		return parseZigFn(rest, structs)
	}
	if rest, ok := strings.CutPrefix(src, "const "); ok {
		if strings.Contains(rest, "extern struct") {
			return parseZigStruct(rest)
		}
		return parseZigVar(rest)
	}
	if rest, ok := strings.CutPrefix(src, "var "); ok {
		return parseZigVar(rest)
	}
	return CFuncSig{}, fmt.Errorf("buzz: ffi: not a Zig declaration: %q (expected fn/var/const)", src)
}

// parseZigStruct parses `Name = extern struct { f1: T1, f2: T2 }`.
func parseZigStruct(src string) (CFuncSig, error) {
	name, rest, ok := strings.Cut(src, "=")
	if !ok {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: malformed struct declaration: %q", src)
	}
	name = strings.TrimSpace(name)
	lb := strings.Index(rest, "{")
	rb := strings.LastIndex(rest, "}")
	if name == "" || lb < 0 || rb < lb {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: malformed struct declaration: %q", src)
	}
	var fields []string
	for _, f := range splitZigParams(rest[lb+1 : rb]) {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		_, ftype, ok := strings.Cut(f, ":")
		if !ok {
			return CFuncSig{}, fmt.Errorf("buzz: ffi: struct field needs `name: type` in %s: %q", name, f)
		}
		fields = append(fields, strings.TrimSpace(ftype))
	}
	if len(fields) == 0 {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: struct %s has no fields", name)
	}
	return CFuncSig{Name: name, IsStruct: true, FieldTypeNames: fields}, nil
}

// structReturnKind classifies a by-value struct return: two doubles ride the
// CGPoint path, four the CGRect path; anything else must come back by
// reference (the same rule upstream applies to struct passing).
func structReturnKind(fields []string) CType {
	for _, f := range fields {
		if f != "f64" {
			return CUnsupported
		}
	}
	switch len(fields) {
	case 2:
		return CPoint2D
	case 4:
		return CRect4D
	}
	return CUnsupported
}

// parseZigVar parses `name: type` — the Zig spelling of an extern data
// symbol. Pointer types load the pointer ([*:0]const u8 follows it to a
// str), scalars load at their width, and a bare `anyopaque` binds the
// symbol's own address (what C's &name is).
func parseZigVar(src string) (CFuncSig, error) {
	name, typeTok, ok := strings.Cut(src, ":")
	if !ok {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: var declaration needs `name: type`: %q", src)
	}
	name = strings.TrimSpace(name)
	typeTok = strings.TrimSpace(typeTok)
	if name == "" || typeTok == "" {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: var declaration needs `name: type`: %q", src)
	}
	kind := zigTypeToCType(typeTok)
	if kind == CUnsupported || typeTok == "anyopaque" {
		kind = CAddr
	}
	if kind == CVoid || kind == CPoint2D || kind == CRect4D {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: unsupported var type %q in %q", typeTok, src)
	}
	return CFuncSig{Name: name, Ret: kind, IsVar: true, VarTypeName: typeTok}, nil
}

func parseZigFn(src string, structs map[string][]string) (CFuncSig, error) {
	lp := strings.Index(src, "(")
	rp := strings.LastIndex(src, ")")
	if lp < 0 || rp < lp {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: malformed fn declaration: %q", src)
	}
	name := strings.TrimSpace(src[:lp])
	retTok := strings.TrimSpace(src[rp+1:])
	if name == "" || retTok == "" {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: fn declaration needs a name and return type: %q", src)
	}
	ret := zigTypeToCType(retTok)
	if ret == CUnsupported {
		if fields, ok := structs[retTok]; ok {
			ret = structReturnKind(fields)
		}
	}
	if ret == CUnsupported {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: unsupported return type %q in fn %s (by-value struct returns are limited to two or four f64 fields; return a pointer otherwise)", retTok, name)
	}

	var params []CParam
	paramSrc := strings.TrimSpace(src[lp+1 : rp])
	if paramSrc != "" {
		for _, p := range splitZigParams(paramSrc) {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			pname, ptype, ok := strings.Cut(p, ":")
			if !ok {
				return CFuncSig{}, fmt.Errorf("buzz: ffi: parameter needs `name: type` in fn %s: %q", name, p)
			}
			kind := zigTypeToCType(strings.TrimSpace(ptype))
			if kind == CUnsupported {
				if _, ok := structs[strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ptype), "*"))]; ok && strings.HasPrefix(strings.TrimSpace(ptype), "*") {
					kind = CVoidPtr // declared struct, by reference
				} else if _, ok := structs[strings.TrimSpace(ptype)]; ok {
					return CFuncSig{}, fmt.Errorf("buzz: ffi: by-value struct parameter %q not supported in fn %s: pass *%s (by reference, as upstream does)", strings.TrimSpace(ptype), name, strings.TrimSpace(ptype))
				}
			}
			if kind == CUnsupported || kind == CVoid {
				return CFuncSig{}, fmt.Errorf("buzz: ffi: unsupported parameter type %q in fn %s", strings.TrimSpace(ptype), name)
			}
			if kind == CPoint2D || kind == CRect4D {
				return CFuncSig{}, fmt.Errorf("buzz: ffi: by-value struct parameter %q not supported in fn %s: declare separate f64 parameters or a pointer", strings.TrimSpace(ptype), name)
			}
			params = append(params, CParam{Name: strings.TrimSpace(pname), Type: kind})
		}
	}
	return CFuncSig{Name: name, Ret: ret, Params: params}, nil
}

// splitZigParams splits on commas outside brackets/parens (Zig pointer types
// like [*:0]const u8 contain no commas today, but stay safe about it).
func splitZigParams(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}
