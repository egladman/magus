// Command magus-protodocs generates the daemon API reference from the .proto contract, so
// a third party can build a client without reading the schema out of the repository. Run it
// manually to refresh the committed pages:
//
//	go run ./cmd/magus-protodocs -descriptor ../proto/gen/descriptor.binpb -out ./reference/api
//
// It reads a descriptor set rather than importing the generated Go, because protoc-gen-go
// clears SourceCodeInfo from the rawDesc it embeds: protoregistry therefore exposes no
// comments at all. (The .pb.go and .connect.go files DO carry the prose, as Go doc comments
// - that is a rendering of the schema, not the schema, and recovering proto field names,
// numbers, and labels back out of it would be reconstruction, not reading.)
//
// The descriptor is produced by the `proto` project's `descriptor` target and consumed here
// as a plain file, so magus sees the whole chain - .proto -> descriptor -> these pages - and
// buf's version enters the proto project's cache key. Invoking buf from inside this binary
// would hide all of that behind an exec call.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/egladman/magus/internal/docs"
)

// Field numbers from descriptor.proto. A comment is addressed by the path to the element it
// documents, so rendering prose means reproducing these exactly: a service is [6,i], its
// method [6,i,2,j], a top-level message [4,i], that message's field [4,i,2,j], a message
// nested inside it [4,i,3,k], an enum nested inside it [4,i,4,k].
const (
	fileMessage = 4
	fileEnum    = 5
	fileService = 6

	msgField      = 2
	msgNestedType = 3
	msgEnumType   = 4

	enumValue     = 2
	serviceMethod = 2
)

func main() {
	descPath := flag.String("descriptor", "../proto/gen/descriptor.binpb", "descriptor set produced by `magus run descriptor proto`")
	out := flag.String("out", "reference/api", "output directory for the API reference")
	flag.Parse()

	raw, err := os.ReadFile(*descPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus-protodocs: read descriptor %s: %v\n(run `magus run descriptor proto` to produce it)\n", *descPath, err)
		os.Exit(1)
	}
	// buf build emits a buf Image, a FileDescriptorSet plus a buf_extension field. Decoding
	// into FileDescriptorSet drops that extension as an unknown field, which is exactly what
	// we want - the extension carries buf bookkeeping, not schema.
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(raw, set); err != nil {
		fmt.Fprintln(os.Stderr, "magus-protodocs: decode descriptor set:", err)
		os.Exit(1)
	}

	n, err := writeAll(newAPI(set), *out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "magus-protodocs:", err)
		os.Exit(1)
	}
	fmt.Printf("magus-protodocs: wrote %d services to %s\n", n, *out)
}

type api struct {
	services []service
	messages map[string]message
	enums    map[string]enumType
}

type service struct {
	Name, Package, File, Doc string
	Methods                  []method
}

type method struct {
	Name, Doc, Input, Output string
	ClientStreaming          bool
	ServerStreaming          bool
}

// kind labels a method by streaming shape, which is the first thing a client author needs:
// it decides whether Connect's unary or streaming protocol applies.
func (m method) kind() string {
	switch {
	case m.ClientStreaming && m.ServerStreaming:
		return "bidirectional streaming"
	case m.ServerStreaming:
		return "server streaming"
	case m.ClientStreaming:
		return "client streaming"
	default:
		return "unary"
	}
}

type message struct {
	Name, Doc string
	Fields    []field
	// refs are the magus-owned messages and enums this message's fields point at. The page
	// walks them transitively: ListEventsResponse holds `repeated Event`, and Event is never
	// itself a method input or output, so a one-hop walk would omit the service's central type.
	refs []string
}

type field struct {
	Name     string
	Number   int32
	Type     string // display form: "string", "repeated Event", "map<string, string>"
	Ref      string // fully qualified magus type this points at, "" for scalars
	Doc      string
	OneOf    string // name of the oneof this field belongs to, "" if none
	Optional bool   // proto3 explicit presence
}

type enumType struct {
	Name, Doc string
	Values    []enumValueDef
}

type enumValueDef struct {
	Name   string
	Number int32
	Doc    string
}

// newAPI flattens the descriptor set. Only magus's own contract is kept: the module pulls in
// protovalidate and the well-known types, and documenting those would bury the API in
// vendored schema.
func newAPI(set *descriptorpb.FileDescriptorSet) api {
	a := api{messages: map[string]message{}, enums: map[string]enumType{}}
	mapEntries := map[string]bool{}

	for _, f := range set.GetFile() {
		pkg := f.GetPackage()
		if !strings.HasPrefix(pkg, "magus.") {
			continue
		}
		c := commentIndex(f)

		for i, m := range f.GetMessageType() {
			a.collectMessage(m, pkg, []int32{fileMessage, int32(i)}, c, mapEntries)
		}
		for i, e := range f.GetEnumType() {
			a.collectEnum(e, pkg, []int32{fileEnum, int32(i)}, c)
		}
		for i, s := range f.GetService() {
			svc := service{
				Name: s.GetName(), Package: pkg, File: f.GetName(),
				Doc: c.at(fileService, int32(i)),
			}
			for j, m := range s.GetMethod() {
				svc.Methods = append(svc.Methods, method{
					Name:            m.GetName(),
					Doc:             c.at(fileService, int32(i), serviceMethod, int32(j)),
					Input:           strings.TrimPrefix(m.GetInputType(), "."),
					Output:          strings.TrimPrefix(m.GetOutputType(), "."),
					ClientStreaming: m.GetClientStreaming(),
					ServerStreaming: m.GetServerStreaming(),
				})
			}
			a.services = append(a.services, svc)
		}
	}

	// Resolve map fields only now: a map's synthetic entry type may be declared after the
	// field that uses it, so the entry set has to be complete before rewriting display types.
	for name, m := range a.messages {
		for i, fl := range m.Fields {
			if fl.Ref != "" && mapEntries[fl.Ref] {
				m.Fields[i].Type = mapDisplay(a.messages[fl.Ref])
				m.Fields[i].Ref = ""
			}
		}
		a.messages[name] = m
	}
	// A synthetic map-entry type is an implementation detail of the map syntax, never a type
	// the schema author wrote or a client sees.
	for name := range mapEntries {
		delete(a.messages, name)
	}

	slices.SortFunc(a.services, func(x, y service) int {
		if c := strings.Compare(x.Package, y.Package); c != 0 {
			return c
		}
		return strings.Compare(x.Name, y.Name)
	})
	return a
}

func (a *api) collectMessage(m *descriptorpb.DescriptorProto, pkg string, path []int32, c comments, mapEntries map[string]bool) {
	full := pkg + "." + m.GetName()
	if m.GetOptions().GetMapEntry() {
		mapEntries[full] = true
	}
	msg := message{Name: full, Doc: c.at(path...)}

	oneofs := m.GetOneofDecl()
	for j, fd := range m.GetField() {
		fl := field{
			Name:     fd.GetName(),
			Number:   fd.GetNumber(),
			Doc:      c.at(append(slices.Clone(path), msgField, int32(j))...),
			Optional: fd.GetProto3Optional(),
		}
		fl.Type, fl.Ref = displayType(fd)
		// A proto3 `optional` is implemented as a synthetic single-member oneof; reporting it
		// as a oneof would tell a client two fields are mutually exclusive when there is only
		// one of them.
		if fd.OneofIndex != nil && !fd.GetProto3Optional() {
			if idx := int(fd.GetOneofIndex()); idx < len(oneofs) {
				fl.OneOf = oneofs[idx].GetName()
			}
		}
		if fl.Ref != "" {
			msg.refs = append(msg.refs, fl.Ref)
		}
		msg.Fields = append(msg.Fields, fl)
	}
	a.messages[full] = msg

	// Nested types carry their parent's name as a prefix, which is how a client refers to them
	// and how a method's input_type names them.
	nestedPkg := full
	for k, n := range m.GetNestedType() {
		a.collectMessage(n, nestedPkg, append(slices.Clone(path), msgNestedType, int32(k)), c, mapEntries)
	}
	for k, e := range m.GetEnumType() {
		a.collectEnum(e, nestedPkg, append(slices.Clone(path), msgEnumType, int32(k)), c)
	}
}

func (a *api) collectEnum(e *descriptorpb.EnumDescriptorProto, pkg string, path []int32, c comments) {
	et := enumType{Name: pkg + "." + e.GetName(), Doc: c.at(path...)}
	for j, v := range e.GetValue() {
		et.Values = append(et.Values, enumValueDef{
			Name:   v.GetName(),
			Number: v.GetNumber(),
			Doc:    c.at(append(slices.Clone(path), enumValue, int32(j))...),
		})
	}
	a.enums[et.Name] = et
}

// displayType returns the table cell for a field and, when the field points at a magus type,
// the fully qualified name so the page can document it too.
func displayType(fd *descriptorpb.FieldDescriptorProto) (string, string) {
	switch fd.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, descriptorpb.FieldDescriptorProto_TYPE_ENUM:
		full := strings.TrimPrefix(fd.GetTypeName(), ".")
		disp := leaf(full)
		if fd.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
			disp = "repeated " + disp
		}
		if !strings.HasPrefix(full, "magus.") {
			return disp, ""
		}
		return disp, full
	default:
		t := strings.ToLower(strings.TrimPrefix(fd.GetType().String(), "TYPE_"))
		if fd.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
			return "repeated " + t, ""
		}
		return t, ""
	}
}

// mapDisplay renders a synthetic map-entry message back as the `map<k, v>` the author wrote.
// Without this a map field reads as `repeated FooEntry`, naming a type that appears nowhere
// in the schema and cannot be looked up.
func mapDisplay(entry message) string {
	var k, v string
	for _, f := range entry.Fields {
		switch f.Name {
		case "key":
			k = f.Type
		case "value":
			v = f.Type
		}
	}
	if k == "" || v == "" {
		return "map"
	}
	return fmt.Sprintf("map<%s, %s>", k, v)
}

// comments maps a source path ("6.0.2.1") to that element's prose. Both leading and trailing
// comments are read: this contract documents 161 fields with a TRAILING comment and 5 with a
// leading one, so reading only leading comments would drop almost all of it.
type comments map[string]string

func (c comments) at(path ...int32) string { return c[pathKey(path...)] }

func commentIndex(f *descriptorpb.FileDescriptorProto) comments {
	idx := comments{}
	for _, loc := range f.GetSourceCodeInfo().GetLocation() {
		parts := make([]string, 0, len(loc.GetPath()))
		for _, p := range loc.GetPath() {
			parts = append(parts, fmt.Sprint(p))
		}
		var joined []string
		if s := clean(loc.GetLeadingComments()); s != "" {
			joined = append(joined, s)
		}
		if s := clean(loc.GetTrailingComments()); s != "" {
			joined = append(joined, s)
		}
		if len(joined) > 0 {
			idx[strings.Join(parts, ".")] = strings.Join(joined, " ")
		}
	}
	return idx
}

func pathKey(path ...int32) string {
	parts := make([]string, 0, len(path))
	for _, p := range path {
		parts = append(parts, fmt.Sprint(p))
	}
	return strings.Join(parts, ".")
}

// clean folds a comment block into one paragraph and escapes what would otherwise break the
// Markdown table it lands in. Proto comments wrap at the author's column, and this schema
// already contains pipes in prose ("RUNNING -> PASSED|FAILED|CACHED"), which would split a
// table row into extra columns.
func clean(c string) string {
	lines := strings.Split(strings.TrimSpace(c), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimSpace(l))
	}
	return strings.ReplaceAll(strings.TrimSpace(strings.Join(out, " ")), "|", `\|`)
}

func leaf(qualified string) string {
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// slug is the service's page basename. The package is folded in only on collision, so the
// common case stays readable (/reference/api/viewer/) and a future same-named service in
// another package cannot silently overwrite another's page.
func (a api) slug(s service) string {
	base := strings.TrimSuffix(strings.ToLower(s.Name), "service")
	for _, other := range a.services {
		if other.Name == s.Name && other.Package != s.Package {
			return strings.ReplaceAll(strings.TrimPrefix(s.Package, "magus."), ".", "-") + "-" + base
		}
	}
	return base
}

func writeAll(a api, outDir string) (int, error) {
	if len(a.services) == 0 {
		return 0, fmt.Errorf("no magus services found in descriptor set")
	}
	// Render everything before writing anything: a failure partway through a direct-write loop
	// would leave the committed tree half old and half new, with nothing to say which is which.
	pages := map[string]string{"index.md": renderIndex(a)}
	for _, s := range a.services {
		pages[a.slug(s)+".md"] = renderService(a, s)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return 0, err
	}
	for name, body := range pages {
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(body), 0o644); err != nil {
			return 0, err
		}
	}
	// Prune pages for services that no longer exist. The drift gate only compares files the
	// generator writes, so one it stops writing is invisible to it: a deleted RPC's page would
	// otherwise sit in the committed tree indefinitely, describing something the daemon no
	// longer serves.
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		if _, keep := pages[e.Name()]; !keep {
			if err := os.Remove(filepath.Join(outDir, e.Name())); err != nil {
				return 0, err
			}
		}
	}
	return len(a.services), nil
}

func renderIndex(a api) string {
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:       "Daemon API",
		Description: "The magus daemon's Connect, gRPC, and gRPC-Web API: every service, method, message, and enum, generated from the .proto contract.",
		Tags:        []string{"api", "proto", "protobuf", "connect", "grpc", "daemon", "reference"},
	})

	b.WriteString("# Daemon API\n\n")
	b.WriteString("The daemon serves its API over [Connect](https://connectrpc.com), which speaks three protocols on one endpoint: Connect's own browser-native HTTP, gRPC, and gRPC-Web. Anything that can send an HTTP request can call it, so a generated client is optional.\n\n")
	b.WriteString("This reference is generated from the `.proto` contract, so it cannot drift from what the daemon serves. The console is one consumer of this API and has no privileged access to it - a front end you write yourself can do everything the console does.\n\n")

	b.WriteString("## Before you call anything\n\n")
	b.WriteString("Start the daemon with `magus server start`. See [the console reference](../console.md) for the endpoint and port, and [the auth diagnostics](../codes/auth/) for what a rejected token means. Requests carry a hashed, expiring `mgs_` bearer token.\n\n")

	b.WriteString("## Services\n\n")
	b.WriteString("| Service | Methods | Package |\n")
	b.WriteString("|---------|---------|--------|\n")
	for _, s := range a.services {
		fmt.Fprintf(&b, "| [%s](%s.md) | %d | `%s` |\n", s.Name, a.slug(s), len(s.Methods), s.Package)
	}
	b.WriteString("\n")

	b.WriteString("## Calling a method without a generated client\n\n")
	b.WriteString("A unary Connect method is a plain POST with a JSON body, so `curl` is a complete client:\n\n")
	b.WriteString("```sh\n")
	if s, m, ok := firstUnary(a); ok {
		fmt.Fprintf(&b, "curl -X POST \\\n  -H \"Content-Type: application/json\" \\\n  -H \"Authorization: Bearer $MAGUS_TOKEN\" \\\n  -d '{}' \\\n  http://127.0.0.1:7391/%s.%s/%s\n", s.Package, s.Name, m.Name)
	}
	b.WriteString("```\n\n")
	b.WriteString("The path is always `/<package>.<Service>/<Method>`, which every page below states per method.\n\n")
	return b.String()
}

// firstUnary picks a real method for the example, so the snippet cannot name an endpoint that
// was renamed or removed from the contract.
func firstUnary(a api) (service, method, bool) {
	for _, s := range a.services {
		for _, m := range s.Methods {
			if !m.ClientStreaming && !m.ServerStreaming {
				return s, m, true
			}
		}
	}
	return service{}, method{}, false
}

func renderService(a api, s service) string {
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:       s.Name,
		Description: firstSentence(s.Doc, fmt.Sprintf("The %s service in the magus daemon API: every method, request, response, and enum.", s.Name)),
		Tags:        []string{"api", "proto", "connect", "grpc", strings.ToLower(s.Name)},
	})

	fmt.Fprintf(&b, "# %s\n\n", s.Name)
	if s.Doc != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Doc)
	}
	fmt.Fprintf(&b, "Package `%s`, defined in `proto/%s`. Part of the [daemon API](index.md).\n\n", s.Package, s.File)

	b.WriteString("## Methods\n\n")
	for _, m := range s.Methods {
		fmt.Fprintf(&b, "### %s\n\n", m.Name)
		if m.Doc != "" {
			fmt.Fprintf(&b, "%s\n\n", m.Doc)
		}
		fmt.Fprintf(&b, "`POST /%s.%s/%s` - %s.\n\n", s.Package, s.Name, m.Name, m.kind())
		fmt.Fprintf(&b, "Takes `%s`, returns `%s`.\n\n", leaf(m.Input), leaf(m.Output))
	}

	msgNames, enumNames := a.reachableFrom(s)

	if len(msgNames) > 0 {
		b.WriteString("## Messages\n\n")
		for _, n := range msgNames {
			writeMessage(&b, a.messages[n])
		}
	}
	if len(enumNames) > 0 {
		b.WriteString("## Enums\n\n")
		for _, n := range enumNames {
			writeEnum(&b, a.enums[n])
		}
	}
	return b.String()
}

// reachableFrom walks the type graph transitively from a service's method signatures. A
// one-hop walk documents only inputs and outputs, which omits the types they carry -
// ListEventsResponse holds `repeated Event`, and Event is the message a client most needs.
func (a api) reachableFrom(s service) (msgs []string, enums []string) {
	seen := map[string]bool{}
	var queue []string
	for _, m := range s.Methods {
		queue = append(queue, m.Input, m.Output)
	}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		if msg, ok := a.messages[n]; ok {
			msgs = append(msgs, n)
			queue = append(queue, msg.refs...)
			continue
		}
		if _, ok := a.enums[n]; ok {
			enums = append(enums, n)
		}
		// Anything else is a well-known type or another package's: named at the call site,
		// not ours to document.
	}
	slices.Sort(msgs)
	slices.Sort(enums)
	return msgs, enums
}

func writeMessage(b *strings.Builder, m message) {
	fmt.Fprintf(b, "### %s\n\n", leaf(m.Name))
	if m.Doc != "" {
		fmt.Fprintf(b, "%s\n\n", m.Doc)
	}
	if len(m.Fields) == 0 {
		b.WriteString("No fields.\n\n")
		return
	}
	b.WriteString("| Field | Type | # | Description |\n")
	b.WriteString("|-------|------|---|-------------|\n")
	for _, f := range m.Fields {
		var notes []string
		if f.OneOf != "" {
			notes = append(notes, fmt.Sprintf("_one of `%s`_", f.OneOf))
		}
		if f.Optional {
			notes = append(notes, "_optional_")
		}
		desc := strings.TrimSpace(strings.Join(append(notes, f.Doc), " "))
		fmt.Fprintf(b, "| `%s` | %s | %d | %s |\n", f.Name, f.Type, f.Number, desc)
	}
	b.WriteString("\n")
}

func writeEnum(b *strings.Builder, e enumType) {
	fmt.Fprintf(b, "### %s\n\n", leaf(e.Name))
	if e.Doc != "" {
		fmt.Fprintf(b, "%s\n\n", e.Doc)
	}
	b.WriteString("| Value | # | Description |\n")
	b.WriteString("|-------|---|-------------|\n")
	for _, v := range e.Values {
		fmt.Fprintf(b, "| `%s` | %d | %s |\n", v.Name, v.Number, v.Doc)
	}
	b.WriteString("\n")
}

// firstSentence trims a doc comment to something that fits a frontmatter description.
func firstSentence(doc, fallback string) string {
	if doc == "" {
		return fallback
	}
	if i := strings.Index(doc, ". "); i >= 0 {
		return doc[:i+1]
	}
	return doc
}
