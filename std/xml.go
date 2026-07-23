package std

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

//go:generate go run ../cmd/magus-utils bindings -module xml -lang buzz -out ../host/gen/xml.go

func init() { Register(XML) }

// XML is the "xml" host module: build and serialize an XML/SVG tree, and parse one
// back. It is the markup counterpart to the json module - render is to stringify what
// parse is to json.parse.
//
// A NODE is either a string (character data) or an ELEMENT: a map with "tag" (the
// element name), "attrs" (a FLAT list of alternating name, value strings), and
// "children" (a list of nodes). Attributes are a list, not a map, on purpose: a map
// crossing the VM boundary loses order, and attribute order is significant when the
// output must be byte-for-byte stable (e.g. a committed badge SVG). An element with no
// children self-closes (<rect .../>); otherwise it wraps its children (<g ...>...</g>).
// render emits no whitespace between tags, so the caller controls the exact bytes.
var XML = Module{
	Name: "xml",
	Doc:  "Build, serialize, and parse XML/SVG.",
	Methods: []Method{
		{
			Name:    "render",
			Doc:     "Serialize an XML node to a string. A node is a string (text) or an element map {\"tag\": name, \"attrs\": [name, value, ...], \"children\": [node, ...]}. Empty-children elements self-close; no whitespace is emitted between tags.",
			Args:    []Arg{{Name: "node", Type: TypeAny}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    XMLRender,
		},
		{
			Name: "element",
			Doc:  "Build an element node from a tag, a flat [name, value, ...] attribute list, and a list of child nodes (elements or strings). Sugar for the {\"tag\", \"attrs\", \"children\"} map that render consumes.",
			Args: []Arg{
				{Name: "tag", Type: TypeString},
				{Name: "attrs", Type: TypeStringSlice},
				{Name: "children", Type: TypeAny},
			},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    XMLElement,
		},
		{
			Name:    "parse",
			Doc:     "Parse an XML string into a node tree: each element becomes {\"tag\": name, \"attrs\": [name, value, ...], \"children\": [node, ...]}, character data becomes a string. The inverse shape of render.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    XMLParse,
		},
	},
}

// XMLElement builds an element node. attrs is a flat [name, value, ...] list; children
// is a list of nodes (elements or strings). It is a constructor for the map shape render
// serializes, so an author writes xml.element(...) instead of a bare map literal.
func XMLElement(_ context.Context, tag string, attrs []string, children any) (any, error) {
	a := make([]any, len(attrs))
	for i, s := range attrs {
		a[i] = s
	}
	kids, err := asList(children)
	if err != nil {
		return nil, fmt.Errorf("xml.element %q: children: %w", tag, err)
	}
	return map[string]any{"tag": tag, "attrs": a, "children": kids}, nil
}

// XMLRender serializes an XML node tree to a string (see the module doc for the shape).
func XMLRender(_ context.Context, node any) (string, error) {
	var b strings.Builder
	if err := renderNode(&b, node); err != nil {
		return "", fmt.Errorf("xml.render: %w", err)
	}
	return b.String(), nil
}

func renderNode(b *strings.Builder, node any) error {
	switch n := node.(type) {
	case string:
		b.WriteString(escapeText(n))
		return nil
	case map[string]any:
		tag, ok := n["tag"].(string)
		if !ok || tag == "" {
			return fmt.Errorf("element is missing a string \"tag\"")
		}
		b.WriteByte('<')
		b.WriteString(tag)
		attrs, err := asList(n["attrs"])
		if err != nil {
			return fmt.Errorf("element %q attrs: %w", tag, err)
		}
		if len(attrs)%2 != 0 {
			return fmt.Errorf("element %q: attrs must be an even-length [name, value, ...] list, got %d", tag, len(attrs))
		}
		for i := 0; i < len(attrs); i += 2 {
			b.WriteByte(' ')
			b.WriteString(asString(attrs[i]))
			b.WriteString(`="`)
			b.WriteString(escapeAttr(asString(attrs[i+1])))
			b.WriteByte('"')
		}
		children, err := asList(n["children"])
		if err != nil {
			return fmt.Errorf("element %q children: %w", tag, err)
		}
		if len(children) == 0 {
			b.WriteString("/>")
			return nil
		}
		b.WriteByte('>')
		for _, c := range children {
			if err := renderNode(b, c); err != nil {
				return err
			}
		}
		b.WriteString("</")
		b.WriteString(tag)
		b.WriteByte('>')
		return nil
	default:
		return fmt.Errorf("node must be a string or an element map, got %T", node)
	}
}

// XMLParse parses an XML string into the same node shape render consumes.
func XMLParse(_ context.Context, s string) (any, error) {
	dec := xml.NewDecoder(strings.NewReader(s))
	var stack []map[string]any
	var root any
	push := func(node any) {
		if len(stack) == 0 {
			root = node
			return
		}
		parent := stack[len(stack)-1]
		children, _ := parent["children"].([]any)
		parent["children"] = append(children, node)
	}
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml.parse: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			attrs := make([]any, 0, len(t.Attr)*2)
			for _, a := range t.Attr {
				attrs = append(attrs, a.Name.Local, a.Value)
			}
			el := map[string]any{"tag": t.Name.Local, "attrs": attrs, "children": []any{}}
			push(el)
			stack = append(stack, el)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if text := string(t); strings.TrimSpace(text) != "" {
				push(text)
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("xml.parse: no element found")
	}
	return root, nil
}

// asList coerces a node field to a list; a missing (nil) field is an empty list.
func asList(v any) ([]any, error) {
	switch l := v.(type) {
	case nil:
		return nil, nil
	case []any:
		return l, nil
	case []string:
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("want a list, got %T", v)
	}
}

// asString renders an attribute name/value; strings pass through, other scalars are
// formatted so a caller may pass a number without pre-converting it.
func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// escapeText escapes character data (&, <, >). escapeAttr also escapes the quote that
// delimits an attribute value. Both leave everything else byte-identical.
func escapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;").Replace(s)
}
