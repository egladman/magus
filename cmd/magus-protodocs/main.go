// Command magus-protodocs generates the daemon API reference from the .proto contract, so
// a third party can build a client without reading the schema out of the repository. It is
// wired into docs/magusfile.buzz's content_generate target and drift-gated there; run it
// directly only to iterate on the generator itself:
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
//
// The descriptor set is resolved into real protoreflect descriptors via protodesc rather than
// walked as raw descriptorpb.FileDescriptorProto: that gives exact source line numbers
// (FileDescriptor.SourceLocations), map/oneof/optional resolution, and extension access
// (proto.GetExtension against a field's Options) for free, instead of hand-rolled
// SourceCodeInfo path arithmetic.
package main

import (
	"cmp"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	validate "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"

	"github.com/egladman/magus/internal/docs"
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

	a, err := newAPI(set)
	if err != nil {
		fmt.Fprintln(os.Stderr, "magus-protodocs:", err)
		os.Exit(1)
	}
	n, err := writeAll(a, *out)
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
	// packages holds a standalone page for a magus package that declares messages or enums
	// but no service (magus.query.v1, magus.graph.v1) - without one, a type declared there
	// has nowhere to live and a message that imports it (ActivityService's TimeRange) would
	// have to duplicate it in full on every page that references it.
	packages map[string]pkgPage
	// svcOfPkg maps a package to the slug of the one service page that documents it. A
	// package in this schema declares at most one service, so a type's declaring package
	// resolves to exactly one canonical page: this map if it has a service, packages
	// otherwise.
	svcOfPkg map[string]string
}

type service struct {
	Name, Package, File, Doc string
	Line                     int32
	Deprecated               bool
	Methods                  []method
}

type method struct {
	Name, Doc, Input, Output string
	Line                     int32
	ClientStreaming          bool
	ServerStreaming          bool
	Deprecated               bool
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
	Name, Package, File, Doc string
	Line                     int32
	Deprecated               bool
	Fields                   []field
	Reserved                 string
	// refs are the magus-owned messages and enums this message's fields point at (including
	// a map field's value type, which never appears in a Ref cell of its own but must still
	// be reachable). The page walks them transitively within one package: ListEventsResponse
	// holds `repeated Event`, and Event is never itself a method input or output, so a
	// one-hop walk would omit the service's central type.
	refs []string
}

type field struct {
	Name          string
	JSONName      string
	Number        int32
	Kind          protoreflect.Kind
	Repeated      bool
	RefIsEnum     bool
	EnumFallback  string // a non-zero value name, for a JSON example; "" if the enum has none
	Type          string // display form: "string", "repeated Event", "map<string, string>"
	Ref           string // fully qualified magus type this points at, "" for scalars and maps
	Doc           string
	OneOf         string
	OneOfRequired bool
	Optional      bool // proto3 explicit presence
	Deprecated    bool
	Constraints   string // rendered buf.validate constraints, "" if none
}

type enumType struct {
	Name, Package, File, Doc string
	Line                     int32
	Deprecated               bool
	Values                   []enumValueDef
	Reserved                 string
}

type enumValueDef struct {
	Name       string
	Number     int32
	Doc        string
	Deprecated bool
}

type pkgPage struct {
	Package, File, Doc string
	Line               int32
}

// newAPI flattens the descriptor set. Only magus's own contract is kept: the module pulls in
// protovalidate and the well-known types, and documenting those would bury the API in
// vendored schema.
func newAPI(set *descriptorpb.FileDescriptorSet) (api, error) {
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return api{}, fmt.Errorf("resolve descriptor set: %w", err)
	}

	a := api{
		messages: map[string]message{},
		enums:    map[string]enumType{},
		packages: map[string]pkgPage{},
		svcOfPkg: map[string]string{},
	}
	pkgMeta := map[string]pkgPage{}

	for _, fp := range set.GetFile() {
		pkg := fp.GetPackage()
		if !strings.HasPrefix(pkg, "magus.") {
			continue
		}
		fd, err := files.FindFileByPath(fp.GetName())
		if err != nil {
			return api{}, fmt.Errorf("resolve file %s: %w", fp.GetName(), err)
		}
		doc, line := fileDoc(fd)
		pkgMeta[pkg] = pkgPage{Package: pkg, File: fd.Path(), Doc: doc, Line: line}

		for i := 0; i < fd.Messages().Len(); i++ {
			a.collectMessage(fd, fd.Messages().Get(i))
		}
		for i := 0; i < fd.Enums().Len(); i++ {
			a.collectEnum(fd, fd.Enums().Get(i))
		}
		for i := 0; i < fd.Services().Len(); i++ {
			sd := fd.Services().Get(i)
			svcDoc, svcLine := bodyDoc(fd, sd)
			svc := service{
				Name: string(sd.Name()), Package: pkg, File: fd.Path(),
				Doc: svcDoc, Line: svcLine,
				Deprecated: isDeprecated(sd.Options()),
			}
			for j := 0; j < sd.Methods().Len(); j++ {
				md := sd.Methods().Get(j)
				mDoc, mLine := bodyDoc(fd, md)
				svc.Methods = append(svc.Methods, method{
					Name: string(md.Name()), Doc: mDoc, Line: mLine,
					Input:           string(md.Input().FullName()),
					Output:          string(md.Output().FullName()),
					ClientStreaming: md.IsStreamingClient(),
					ServerStreaming: md.IsStreamingServer(),
					Deprecated:      isDeprecated(md.Options()),
				})
			}
			a.services = append(a.services, svc)
			a.svcOfPkg[pkg] = a.pagePathOf(svc)
		}
	}

	// A package with no service (magus.query.v1, magus.graph.v1) still needs somewhere to
	// live if it declares anything: a standalone page. One with a service was already
	// pointed at that service's page above.
	for pkg, meta := range pkgMeta {
		if _, hasSvc := a.svcOfPkg[pkg]; hasSvc {
			continue
		}
		if len(a.messagesInPackage(pkg)) == 0 && len(a.enumsInPackage(pkg)) == 0 {
			continue
		}
		a.packages[pkg] = meta
	}

	slices.SortFunc(a.services, func(x, y service) int {
		if c := strings.Compare(x.Package, y.Package); c != 0 {
			return c
		}
		return strings.Compare(x.Name, y.Name)
	})
	return a, nil
}

func (a *api) messagesInPackage(pkg string) []string {
	var out []string
	for full, m := range a.messages {
		if m.Package == pkg {
			out = append(out, full)
		}
	}
	slices.Sort(out)
	return out
}

func (a *api) enumsInPackage(pkg string) []string {
	var out []string
	for full, e := range a.enums {
		if e.Package == pkg {
			out = append(out, full)
		}
	}
	slices.Sort(out)
	return out
}

func (a *api) collectMessage(fd protoreflect.FileDescriptor, md protoreflect.MessageDescriptor) {
	full := string(md.FullName())
	doc, line := bodyDoc(fd, md)
	msg := message{
		Name: full, Package: string(md.ParentFile().Package()), File: fd.Path(),
		Doc: doc, Line: line,
		Deprecated: isDeprecated(md.Options()),
		Reserved:   reservedFieldNote(md),
	}

	oneofReq := map[string]bool{}
	for i := 0; i < md.Oneofs().Len(); i++ {
		od := md.Oneofs().Get(i)
		if !od.IsSynthetic() {
			oneofReq[string(od.Name())] = oneofRequired(od)
		}
	}

	for i := 0; i < md.Fields().Len(); i++ {
		fdesc := md.Fields().Get(i)
		fDoc := cellDoc(fd, fdesc)
		fl := field{
			Name:        string(fdesc.Name()),
			JSONName:    fdesc.JSONName(),
			Number:      int32(fdesc.Number()),
			Kind:        fdesc.Kind(),
			Repeated:    fdesc.IsList(),
			Doc:         fDoc,
			Optional:    fdesc.HasOptionalKeyword(),
			Deprecated:  isDeprecated(fdesc.Options()),
			Constraints: fieldConstraints(fdesc),
		}
		fl.Type, fl.Ref = displayType(fdesc)
		if fdesc.Kind() == protoreflect.EnumKind {
			fl.RefIsEnum = true
			fl.EnumFallback = firstNonZeroEnumValue(fdesc.Enum())
		}
		// A proto3 `optional` is implemented as a synthetic single-member oneof; reporting it
		// as a oneof would tell a client two fields are mutually exclusive when there is only
		// one of them.
		if od := fdesc.ContainingOneof(); od != nil && !od.IsSynthetic() {
			fl.OneOf = string(od.Name())
			fl.OneOfRequired = oneofReq[fl.OneOf]
		}
		if fl.Ref != "" {
			msg.refs = append(msg.refs, fl.Ref)
		} else if fdesc.IsMap() {
			if _, vref := displayType(fdesc.MapValue()); vref != "" {
				msg.refs = append(msg.refs, vref)
			}
		}
		msg.Fields = append(msg.Fields, fl)
	}
	a.messages[full] = msg

	for i := 0; i < md.Messages().Len(); i++ {
		nested := md.Messages().Get(i)
		// The synthetic entry type behind a map<k, v> field is an implementation detail of
		// the map syntax, never a type the schema author wrote or a client sees.
		if nested.IsMapEntry() {
			continue
		}
		a.collectMessage(fd, nested)
	}
	for i := 0; i < md.Enums().Len(); i++ {
		a.collectEnum(fd, md.Enums().Get(i))
	}
}

func (a *api) collectEnum(fd protoreflect.FileDescriptor, ed protoreflect.EnumDescriptor) {
	full := string(ed.FullName())
	doc, line := bodyDoc(fd, ed)
	et := enumType{
		Name: full, Package: string(ed.ParentFile().Package()), File: fd.Path(),
		Doc: doc, Line: line,
		Deprecated: isDeprecated(ed.Options()),
		Reserved:   reservedEnumNote(ed),
	}
	for i := 0; i < ed.Values().Len(); i++ {
		vd := ed.Values().Get(i)
		vDoc := cellDoc(fd, vd)
		et.Values = append(et.Values, enumValueDef{
			Name: string(vd.Name()), Number: int32(vd.Number()), Doc: vDoc,
			Deprecated: isDeprecated(vd.Options()),
		})
	}
	a.enums[full] = et
}

// firstNonZeroEnumValue names a value other than the conventional _UNSPECIFIED = 0 one, for a
// JSON example: a request field left at its zero value would round-trip but says nothing
// about what a client would actually send.
func firstNonZeroEnumValue(ed protoreflect.EnumDescriptor) string {
	for i := 0; i < ed.Values().Len(); i++ {
		if v := ed.Values().Get(i); v.Number() != 0 {
			return string(v.Name())
		}
	}
	if ed.Values().Len() > 0 {
		return string(ed.Values().Get(0).Name())
	}
	return ""
}

// displayType returns the table cell for a field and, when the field points at a magus type,
// the fully qualified name so the page can document (and link to) it too. A map field's cell
// is always plain text - "map<string, Foo>" - even when Foo is a magus type: the caller
// still needs Foo reachable (see collectMessage), just not rendered as a link inline in the
// cell.
func displayType(fd protoreflect.FieldDescriptor) (disp, ref string) {
	if fd.IsMap() {
		k, _ := displayType(fd.MapKey())
		v, _ := displayType(fd.MapValue())
		return fmt.Sprintf("map<%s, %s>", k, v), ""
	}
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind, protoreflect.EnumKind:
		full := string(fieldTypeFullName(fd))
		disp := leaf(full)
		if fd.IsList() {
			disp = "repeated " + disp
		}
		if !strings.HasPrefix(full, "magus.") {
			return disp, ""
		}
		return disp, full
	default:
		t := fd.Kind().String()
		if fd.IsList() {
			return "repeated " + t, ""
		}
		return t, ""
	}
}

func fieldTypeFullName(fd protoreflect.FieldDescriptor) protoreflect.FullName {
	if fd.Kind() == protoreflect.EnumKind {
		return fd.Enum().FullName()
	}
	return fd.Message().FullName()
}

// deprecatable is satisfied by every *Options message descriptorpb defines
// (MessageOptions, FieldOptions, EnumOptions, EnumValueOptions, ServiceOptions,
// MethodOptions): each carries its own `optional bool deprecated`.
type deprecatable interface {
	GetDeprecated() bool
}

func isDeprecated(opts protoreflect.ProtoMessage) bool {
	if opts == nil {
		return false
	}
	d, ok := opts.(deprecatable)
	return ok && d.GetDeprecated()
}

// fieldConstraints renders a field's buf.validate rules as short phrases -
// "0 to 1000", "matches `^[a-z]{2,8}[0-9a-f]+$`", "required" - so the reference states what
// the daemon actually enforces instead of leaving a client to discover it from a rejected
// request.
func fieldConstraints(fd protoreflect.FieldDescriptor) string {
	opts, ok := fd.Options().(*descriptorpb.FieldOptions)
	if !ok || opts == nil {
		return ""
	}
	rules, ok := proto.GetExtension(opts, validate.E_Field).(*validate.FieldRules)
	if !ok || rules == nil {
		return ""
	}
	var parts []string
	if rules.GetRequired() {
		parts = append(parts, "required")
	}
	parts = append(parts, describeRules(rules.ProtoReflect())...)
	return strings.Join(parts, "; ")
}

// oneofRequired reports (buf.validate.oneof).required: exactly one field of the oneof must
// be set. Rendered on the oneof note rather than repeated per field.
func oneofRequired(od protoreflect.OneofDescriptor) bool {
	opts, ok := od.Options().(*descriptorpb.OneofOptions)
	if !ok || opts == nil {
		return false
	}
	rules, ok := proto.GetExtension(opts, validate.E_Oneof).(*validate.OneofRules)
	return ok && rules.GetRequired()
}

// describeRules generically walks a protovalidate FieldRules message (or one of its
// per-type sub-messages, e.g. StringRules) and renders every populated leaf as a short
// "name: value" phrase. Doing this by reflection rather than a hand-written case per
// constraint means a protovalidate rule this schema starts using tomorrow shows up without a
// code change here; only the free-form CEL rules, which have no fixed shape, get a fallback.
func describeRules(m protoreflect.Message) []string {
	var out []string
	rangeInFieldOrder(m, func(fd protoreflect.FieldDescriptor, v protoreflect.Value) {
		name := string(fd.Name())
		// required/ignore are handled by the caller (required) or are an internal
		// evaluation hint a client has no use for (ignore).
		if name == "required" || name == "ignore" {
			return
		}
		switch {
		case fd.IsList():
			out = append(out, describeListRule(name, fd, v)...)
		case fd.Kind() == protoreflect.MessageKind && !fd.IsMap():
			out = append(out, describeSubRules(name, v.Message())...)
		case fd.Kind() == protoreflect.BoolKind:
			if v.Bool() {
				out = append(out, name)
			}
		default:
			out = append(out, name+": "+formatScalar(fd, v))
		}
	})
	return out
}

// rangeInFieldOrder visits m's populated fields in field-number order. protoreflect's Range
// is documented as visiting in an undefined order, so rendering straight from it made these
// pages differ between runs for any field carrying two rules in one block ("int32.gte: 0;
// int32.lte: 1000" one run, the reverse the next) and the docs drift gate caught the flip.
// Field number is the .proto's own ordering, so this also renders rules in the order the
// option declares them.
func rangeInFieldOrder(m protoreflect.Message, fn func(protoreflect.FieldDescriptor, protoreflect.Value)) {
	type field struct {
		fd protoreflect.FieldDescriptor
		v  protoreflect.Value
	}
	var fields []field
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		fields = append(fields, field{fd, v})
		return true
	})
	slices.SortFunc(fields, func(x, y field) int { return cmp.Compare(x.fd.Number(), y.fd.Number()) })
	for _, f := range fields {
		fn(f.fd, f.v)
	}
}

// describeSubRules renders a type-specific rule block (StringRules, Int32Rules, ...),
// prefixing each leaf with the block's own field name ("string.pattern", "int32.gte") so it
// reads the same way the .proto option does.
func describeSubRules(prefix string, m protoreflect.Message) []string {
	var out []string
	rangeInFieldOrder(m, func(fd protoreflect.FieldDescriptor, v protoreflect.Value) {
		name := prefix + "." + string(fd.Name())
		switch {
		case fd.IsList():
			out = append(out, describeListRule(name, fd, v)...)
		case fd.Kind() == protoreflect.MessageKind && !fd.IsMap():
			// A further-nested rule (e.g. repeated.items is itself a FieldRules). Noted
			// rather than recursed without bound - nothing in this schema goes this deep.
			out = append(out, name+" constrained")
		case fd.Kind() == protoreflect.BoolKind:
			if v.Bool() {
				out = append(out, name)
			}
		default:
			out = append(out, name+": "+formatScalar(fd, v))
		}
	})
	return out
}

func describeListRule(name string, fd protoreflect.FieldDescriptor, v protoreflect.Value) []string {
	list := v.List()
	if list.Len() == 0 {
		return nil
	}
	switch name {
	case "cel":
		return []string{fmt.Sprintf("%d custom CEL rule(s)", list.Len())}
	case "cel_expression":
		var exprs []string
		for i := 0; i < list.Len(); i++ {
			exprs = append(exprs, "`"+list.Get(i).String()+"`")
		}
		return []string{"custom rule: " + strings.Join(exprs, ", ")}
	}
	var vals []string
	for i := 0; i < list.Len(); i++ {
		vals = append(vals, formatScalar(fd, list.Get(i)))
	}
	return []string{name + ": " + strings.Join(vals, ", ")}
}

func formatScalar(fd protoreflect.FieldDescriptor, v protoreflect.Value) string {
	switch fd.Kind() {
	case protoreflect.StringKind, protoreflect.BytesKind:
		// A table cell ends at the first unescaped pipe, and a code span does not protect
		// it, so an alternation pattern like `^(shared|private)/.+$` truncates the row.
		return "`" + strings.ReplaceAll(v.String(), "|", `\|`) + "`"
	case protoreflect.EnumKind:
		if ev := fd.Enum().Values().ByNumber(v.Enum()); ev != nil {
			return string(ev.Name())
		}
		return fmt.Sprint(v.Interface())
	default:
		return fmt.Sprint(v.Interface())
	}
}

// reservedFieldNote renders a message's `reserved` numbers and names, so the gaps a reader
// sees in the field-number column are explained rather than left looking like an omission.
func reservedFieldNote(md protoreflect.MessageDescriptor) string {
	var nums []string
	rr := md.ReservedRanges()
	for i := 0; i < rr.Len(); i++ {
		r := rr.Get(i) // start inclusive, end exclusive
		nums = append(nums, fieldRangeString(int32(r[0]), int32(r[1])-1))
	}
	return reservedNote(nums, md.ReservedNames())
}

func reservedEnumNote(ed protoreflect.EnumDescriptor) string {
	var nums []string
	rr := ed.ReservedRanges()
	for i := 0; i < rr.Len(); i++ {
		r := rr.Get(i) // start and end both inclusive for an enum
		nums = append(nums, fieldRangeString(int32(r[0]), int32(r[1])))
	}
	return reservedNote(nums, ed.ReservedNames())
}

func fieldRangeString(lo, hi int32) string {
	if lo == hi {
		return fmt.Sprint(lo)
	}
	return fmt.Sprintf("%d to %d", lo, hi)
}

func reservedNote(nums []string, names protoreflect.Names) string {
	var parts []string
	if len(nums) > 0 {
		parts = append(parts, strings.Join(nums, ", "))
	}
	if names.Len() > 0 {
		var ns []string
		for i := 0; i < names.Len(); i++ {
			ns = append(ns, "`"+string(names.Get(i))+"`")
		}
		parts = append(parts, strings.Join(ns, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Reserved: " + strings.Join(parts, "; ") + "."
}

// fileDoc reads the package-level comment. There is no protoreflect.Descriptor for a
// `package` declaration, so it is addressed by SourceCodeInfo path [2] (the `package` field
// of FileDescriptorProto) directly, via ByPath rather than ByDescriptor.
func fileDoc(fd protoreflect.FileDescriptor) (string, int32) {
	loc := fd.SourceLocations().ByPath(protoreflect.SourcePath{2})
	return bodyComments(loc), int32(loc.StartLine) + 1
}

// bodyDoc reads a comment that renders as page PROSE - a service, method, message, or enum
// doc. It keeps the blank-line paragraph breaks the author wrote, because those carry the
// summary-then-detail split this schema writes in: Staleness opens with one line saying
// what it is, then a paragraph on why it is not calendar age, and running them together
// buries the summary.
//
// Fields and enum values keep docAndLine instead. Their prose lands in a table cell, which
// cannot hold a line break at all, so flattening there is the only option rather than a
// choice.
func bodyDoc(fd protoreflect.FileDescriptor, desc protoreflect.Descriptor) (string, int32) {
	loc := fd.SourceLocations().ByDescriptor(desc)
	return bodyComments(loc), int32(loc.StartLine) + 1
}

// cellDoc reads a comment destined for a table cell - a field or an enum value - and
// flattens it to the single line a cell can hold. No line number: a row carries no source
// link of its own, only the message or enum heading above it does.
func cellDoc(fd protoreflect.FileDescriptor, desc protoreflect.Descriptor) string {
	return cleanComments(fd.SourceLocations().ByDescriptor(desc))
}

// bodyComments is cleanComments with the author's paragraph breaks kept. Each paragraph
// still goes through clean(), so the escaping and bare-URL handling are identical to a
// table cell's - only the breaks between paragraphs survive.
func bodyComments(loc protoreflect.SourceLocation) string {
	var paras []string
	for _, block := range []string{loc.LeadingComments, loc.TrailingComments} {
		for _, para := range strings.Split(block, "\n\n") {
			if s := clean(para); s != "" {
				paras = append(paras, s)
			}
		}
	}
	return strings.Join(paras, "\n\n")
}

// cleanComments joins a location's leading and trailing comments. Both are read: this
// contract documents far more fields with a TRAILING comment than a leading one, so reading
// only leading comments would drop almost all of it.
func cleanComments(loc protoreflect.SourceLocation) string {
	var joined []string
	if s := clean(loc.LeadingComments); s != "" {
		joined = append(joined, s)
	}
	if s := clean(loc.TrailingComments); s != "" {
		joined = append(joined, s)
	}
	return strings.Join(joined, " ")
}

// clean folds a comment block into one paragraph and escapes what would otherwise break the
// Markdown it lands in. Proto comments are prose written for a .proto file, not for
// Markdown, so three things have to be neutralized:
//
//	|  this schema already writes pipes in prose ("RUNNING -> PASSED|FAILED|CACHED"), which
//	   would split a table row into extra columns.
//	_  an identifier fragment like "the STATE_ prefix" opens an emphasis span that never
//	   closes, italicizing the rest of the page. Escaping every underscore also renders
//	   snake_case names correctly, so there is no case where leaving one bare is better.
//	a bare URL in prose (graph.proto's source_base comment names one as an example) trips
//	   the site's no-bare-urls lint rule; wrapped in <...> it renders as a real link instead.
func clean(c string) string {
	lines := strings.Split(strings.TrimSpace(c), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, strings.TrimSpace(l))
	}
	return escapeAndLinkify(strings.TrimSpace(strings.Join(out, " ")))
}

// escapeAndLinkify escapes Markdown-significant characters everywhere except inside a bare
// URL, which it wraps in <...> instead: an escaped underscore inside a URL would corrupt
// it (Markdown does not interpret backslash escapes inside an autolink), so the escaping
// has to skip exactly the substrings this wraps.
func escapeAndLinkify(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		start := nextURLStart(s)
		if start < 0 {
			b.WriteString(escapeMD(s))
			break
		}
		b.WriteString(escapeMD(s[:start]))
		end := start
		for end < len(s) && !isURLBoundary(s[end]) {
			end++
		}
		b.WriteString("<" + s[start:end] + ">")
		s = s[end:]
	}
	return b.String()
}

func nextURLStart(s string) int {
	start := -1
	for _, scheme := range []string{"http://", "https://"} {
		if i := strings.Index(s, scheme); i >= 0 && (start < 0 || i < start) {
			start = i
		}
	}
	return start
}

func isURLBoundary(b byte) bool {
	return b == ' ' || b == '"' || b == ')' || b == ']' || b == '>' || b == '\n' || b == '\t'
}

func escapeMD(s string) string {
	return strings.NewReplacer("|", `\|`, "_", `\_`).Replace(s)
}

func leaf(qualified string) string {
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// anchor is the heading id goldmark's auto-heading-id extension would assign to a `### name`
// heading (parser.WithAutoHeadingID: lowercase, alnum kept, space/hyphen/underscore folded
// to a single hyphen, everything else dropped). Reproduced here rather than depending on the
// rendered HTML so a cross-reference can be built before the site ever renders this page.
func anchor(name string) string {
	var b strings.Builder
	for _, r := range leaf(name) {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "heading"
	}
	return b.String()
}

// pagePath is a page's output path relative to outDir, with no extension: the same path the
// .proto it documents has, minus the "magus/" prefix every magus package shares (redundant
// under reference/api/) - "magus/graph/v1/graph.proto" becomes "graph/v1/graph". A generated
// page then sits at the same depth as its source, so knowing one locates the other, and two
// files can never collide on output path without already colliding on input path.
func pagePath(protoFile string) string {
	return strings.TrimPrefix(strings.TrimSuffix(protoFile, ".proto"), "magus/")
}

func (a api) pagePathOf(s service) string { return pagePath(s.File) }

// pageFor returns the output path of the page that canonically documents a magus package,
// and whether one exists (it does for every magus.* package that declares a service or a
// top-level type; it does not for an external or well-known package).
func (a api) pageFor(pkg string) (string, bool) {
	if path, ok := a.svcOfPkg[pkg]; ok {
		return path, true
	}
	if p, ok := a.packages[pkg]; ok {
		return pagePath(p.File), true
	}
	return "", false
}

// relLink computes the relative Markdown link (no extension or anchor) from the page at
// fromPath to the page at toPath, both output paths as pagePath returns them. Pages nest by
// .proto directory, so a cross-package reference cannot assume its target is a sibling in
// the same directory - "token/v1/token" linking to "query/v1/query" needs
// "../../query/v1/query", not "query/v1/query".
//
// The link is resolved from fromPath's DIRECTORY, since a relative href is read from the
// page's location, not the page itself. ToSlash because the result is a URL: filepath works
// in the platform separator, which on Windows would emit backslashes into Markdown.
func relLink(fromPath, toPath string) string {
	rel, err := filepath.Rel(filepath.Dir(filepath.FromSlash(fromPath)), filepath.FromSlash(toPath))
	if err != nil {
		// Only reachable if the two paths cannot be made relative (a different volume or
		// an absolute/relative mix). Both come from pagePath, which yields neither.
		return toPath
	}
	return filepath.ToSlash(rel)
}

// typeLink renders a reference to a magus message or enum from the page at fromPath
// (documenting fromPkg): a same-package reference links to the local anchor, a foreign one
// links to that type's canonical page, and anything else (a well-known type, or a magus type
// this run never saw) renders as plain code.
func (a api) typeLink(disp, full, fromPkg, fromPath string) string {
	if full == "" {
		return disp
	}
	pkg := a.packageOf(full)
	if pkg == fromPkg {
		return fmt.Sprintf("[%s](#%s)", disp, anchor(full))
	}
	if path, ok := a.pageFor(pkg); ok {
		return fmt.Sprintf("[%s](%s.md#%s)", disp, relLink(fromPath, path), anchor(full))
	}
	return "`" + disp + "`"
}

func (a api) packageOf(full string) string {
	if m, ok := a.messages[full]; ok {
		return m.Package
	}
	if e, ok := a.enums[full]; ok {
		return e.Package
	}
	return ""
}

func writeAll(a api, outDir string) (int, error) {
	if len(a.services) == 0 {
		return 0, fmt.Errorf("no magus services found in descriptor set")
	}
	usedBy := a.computeUsedBy()
	// Render everything before writing anything: a failure partway through a direct-write loop
	// would leave the committed tree half old and half new, with nothing to say which is which.
	pages := map[string]string{"index.md": renderIndex(a)}
	for _, s := range a.services {
		pages[a.pagePathOf(s)+".md"] = renderService(a, s, usedBy)
	}
	for _, p := range a.packages {
		pages[pagePath(p.File)+".md"] = renderPackage(a, p, usedBy)
	}
	for name, body := range pages {
		full := filepath.Join(outDir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return 0, err
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			return 0, err
		}
	}
	// Prune pages for services and packages that no longer exist, at any depth a page might
	// live now that pages nest by .proto path. The drift gate only compares files the
	// generator writes, so one it stops writing is invisible to it: a deleted RPC's page
	// would otherwise sit in the committed tree indefinitely, describing something the
	// daemon no longer serves.
	err := filepath.WalkDir(outDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		rel, err := filepath.Rel(outDir, path)
		if err != nil {
			return err
		}
		if _, keep := pages[filepath.ToSlash(rel)]; !keep {
			return os.Remove(path)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(a.services), nil
}

// usage is one "Used by" entry: a method that reaches a message or enum transitively through
// its request or response type. pagePath and anchor are resolved to a link relative to the
// page rendering the entry, not here, since a shared type's page is not known until then.
type usage struct {
	label    string // "ListTokens (request)"
	pagePath string // "token/v1/token"
	anchor   string // "listtokens"
}

// computeUsedBy walks every method's request and response transitively and records, per
// magus type, which methods reach it - so a message that is never itself a request or
// response, only nested inside one, can still say which RPCs return it.
func (a api) computeUsedBy() map[string][]usage {
	out := map[string][]usage{}
	for _, s := range a.services {
		path := a.pagePathOf(s)
		for _, m := range s.Methods {
			record := func(root, kind string) {
				u := usage{label: fmt.Sprintf("%s (%s)", m.Name, kind), pagePath: path, anchor: anchor(m.Name)}
				seen := map[string]bool{}
				queue := []string{root}
				for len(queue) > 0 {
					n := queue[0]
					queue = queue[1:]
					if seen[n] {
						continue
					}
					seen[n] = true
					if msg, ok := a.messages[n]; ok {
						out[n] = append(out[n], u)
						queue = append(queue, msg.refs...)
						continue
					}
					if _, ok := a.enums[n]; ok {
						out[n] = append(out[n], u)
					}
				}
			}
			record(m.Input, "request")
			record(m.Output, "response")
		}
	}
	for k, v := range out {
		slices.SortFunc(v, func(x, y usage) int { return strings.Compare(x.label, y.label) })
		out[k] = slices.CompactFunc(v, func(x, y usage) bool { return x == y })
	}
	return out
}

func renderIndex(a api) string {
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:         "Daemon API",
		Description:   "The magus daemon's Connect, gRPC, and gRPC-Web API: every service, method, message, and enum, generated from the .proto contract.",
		GeneratedFrom: "proto/magus/**/*.proto",
		Tags:          []string{"api", "proto", "protobuf", "connect", "grpc", "daemon", "reference"},
	})

	b.WriteString("# Daemon API\n\n")
	b.WriteString("The daemon serves its API over [Connect](https://connectrpc.com), which speaks three protocols on one endpoint: Connect's own browser-native HTTP, gRPC, and gRPC-Web. Anything that can send an HTTP request can call it, so a generated client is optional.\n\n")
	b.WriteString("This reference is generated from the `.proto` contract, so it cannot drift from what the daemon serves. The Console is a reference frontend and has no privileged access to the API. A frontend you build can use the same published contract. Every service, method, message, and enum heading below links to the exact line in the `.proto` source that defines it.\n\n")

	b.WriteString("## Before you call anything\n\n")
	b.WriteString("Start the daemon with `magus server start`. See [the console reference](../console.md) for the endpoint and port, and [the auth diagnostics](../codes/auth/) for what a rejected token means. Requests carry a hashed, expiring `mgs_` bearer token.\n\n")

	b.WriteString("## Services\n\n")
	b.WriteString("| Service | Methods | Package |\n")
	b.WriteString("|---------|---------|--------|\n")
	for _, s := range a.services {
		fmt.Fprintf(&b, "| [%s](%s.md) | %d | `%s` |\n", s.Name, a.pagePathOf(s), len(s.Methods), s.Package)
	}
	b.WriteString("\n")

	if len(a.packages) > 0 {
		b.WriteString("## Shared types\n\n")
		b.WriteString("A package with no service of its own: its types are documented here instead of on a service page, and a service that uses one links to it.\n\n")
		b.WriteString("| Package | File |\n")
		b.WriteString("|---------|------|\n")
		pkgs := make([]string, 0, len(a.packages))
		for pkg := range a.packages {
			pkgs = append(pkgs, pkg)
		}
		slices.Sort(pkgs)
		for _, pkg := range pkgs {
			p := a.packages[pkg]
			fmt.Fprintf(&b, "| [%s](%s.md) | `proto/%s` |\n", pkg, pagePath(p.File), p.File)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Calling a method without a generated client\n\n")
	b.WriteString("A unary Connect method is a plain POST with a JSON body, so `curl` is a complete client. protojson, not the `.proto` field name, decides the wire shape:\n\n")
	b.WriteString("- Field names are lowerCamelCase (`page_size` in the tables below is `pageSize` on the wire).\n")
	b.WriteString("- `int64`, `uint64`, `fixed64`, and `sfixed64` values are JSON strings, not numbers (large values overflow a JSON number's safe integer range).\n")
	b.WriteString("- `bytes` is base64. A `Timestamp` is an RFC 3339 string; a `Duration` is a string like `\"1.5s\"`.\n")
	b.WriteString("- An enum serializes as its value name (`\"TOKEN_SCOPE_CONNECTOR\"`), not its number.\n\n")
	b.WriteString("```sh\n")
	if s, m, ok := firstUnary(a); ok {
		fmt.Fprintf(&b, "curl -X POST \\\n  -H \"Content-Type: application/json\" \\\n  -H \"Authorization: Bearer $MAGUS_TOKEN\" \\\n  -d '%s' \\\n  http://127.0.0.1:7391/%s.%s/%s\n", a.exampleJSON(m.Input), s.Package, s.Name, m.Name)
	}
	b.WriteString("```\n\n")
	b.WriteString("The path is always `/<package>.<Service>/<Method>`, which every page below states per method. A request body shown as `{}` takes no fields; a longer one is a shallow skeleton (top-level scalar and enum fields only) and does not attempt to satisfy every field's constraints - a `string.pattern` rule still needs a value matching that pattern.\n\n")

	if names := streamingMethods(a); len(names) > 0 {
		b.WriteString("## Streaming methods\n\n")
		b.WriteString("A server-streaming method returns a sequence of messages over one HTTP response rather than one body, so a single `curl -d` request cannot cleanly demux it. Use a generated Connect client, or a tool built for streaming RPCs such as [grpcurl](https://github.com/fullstorydev/grpcurl) (`grpcurl -plaintext ... /magus.viewer.v1.ViewerService/StreamEvents`). Streaming methods:\n\n")
		for _, n := range names {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func streamingMethods(a api) []string {
	var out []string
	for _, s := range a.services {
		for _, m := range s.Methods {
			if m.ClientStreaming || m.ServerStreaming {
				out = append(out, fmt.Sprintf("[%s.%s](%s.md#%s) (%s)", s.Name, m.Name, a.pagePathOf(s), anchor(m.Name), m.kind()))
			}
		}
	}
	return out
}

// exampleJSON builds a shallow protojson skeleton for a request message: top-level scalar
// and enum fields only, each keyed by its wire (JSON) name. A oneof's fields are omitted
// entirely - they are mutually exclusive alternatives, and including all of them would
// misstate the contract - as are message and repeated fields, to keep the example short
// enough to read at a glance.
func (a api) exampleJSON(inputFull string) string {
	msg, ok := a.messages[inputFull]
	if !ok {
		return "{}"
	}
	var lines []string
	for _, f := range msg.Fields {
		if f.Repeated || f.OneOf != "" {
			continue
		}
		if f.Ref != "" && !f.RefIsEnum {
			continue
		}
		lines = append(lines, fmt.Sprintf("%q:%s", f.JSONName, placeholderFor(f)))
	}
	if len(lines) == 0 {
		return "{}"
	}
	return "{" + strings.Join(lines, ",") + "}"
}

func placeholderFor(f field) string {
	if f.RefIsEnum {
		if f.EnumFallback != "" {
			return `"` + f.EnumFallback + `"`
		}
		return `""`
	}
	switch f.Kind {
	case protoreflect.StringKind, protoreflect.BytesKind:
		return `""`
	case protoreflect.BoolKind:
		return "false"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return `"0"` // the 64-bit family serializes as a JSON string
	default:
		return "0"
	}
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

func sourceLink(file string, line int32) string {
	return fmt.Sprintf("[%s:%d](%s/proto/%s#L%d)", filepath.Base(file), line, docs.RepoBlob, file, line)
}

func renderService(a api, s service, usedBy map[string][]usage) string {
	path := a.pagePathOf(s)
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:       s.Name,
		Description: firstSentence(s.Doc, fmt.Sprintf("The %s service in the magus daemon API: every method, request, response, and enum.", s.Name)),
		// The heading-level source links already point at this page's own .proto file
		// and line; the section overview is where the whole schema's source is named.
		GeneratedFrom: "reference/api/",
		Tags:          []string{"api", "proto", "connect", "grpc", strings.ToLower(s.Name)},
	})

	fmt.Fprintf(&b, "# %s\n\n", s.Name)
	if s.Deprecated {
		b.WriteString("**Deprecated.**\n\n")
	}
	if s.Doc != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Doc)
	}
	fmt.Fprintf(&b, "Package `%s`, defined in `proto/%s`. Source: %s. Part of the [daemon API](%s.md).\n\n", s.Package, s.File, sourceLink(s.File, s.Line), relLink(path, "index"))

	b.WriteString("## Methods\n\n")
	for _, m := range s.Methods {
		fmt.Fprintf(&b, "### %s\n\n", m.Name)
		if m.Deprecated {
			b.WriteString("**Deprecated.**\n\n")
		}
		if m.Doc != "" {
			fmt.Fprintf(&b, "%s\n\n", m.Doc)
		}
		fmt.Fprintf(&b, "`POST /%s.%s/%s`: %s. Source: %s.\n\n", s.Package, s.Name, m.Name, m.kind(), sourceLink(s.File, m.Line))
		fmt.Fprintf(&b, "Takes %s, returns %s.\n\n",
			a.typeLink(leaf(m.Input), m.Input, s.Package, path),
			a.typeLink(leaf(m.Output), m.Output, s.Package, path))
	}

	msgNames, enumNames := a.reachableFrom(s.Package, seedsOf(s))

	if len(msgNames) > 0 {
		b.WriteString("## Messages\n\n")
		for _, n := range msgNames {
			writeMessage(&b, a, a.messages[n], usedBy[n], path)
		}
	}
	if len(enumNames) > 0 {
		b.WriteString("## Enums\n\n")
		for _, n := range enumNames {
			writeEnum(&b, a.enums[n], usedBy[n], path)
		}
	}
	return b.String()
}

func renderPackage(a api, p pkgPage, usedBy map[string][]usage) string {
	path := pagePath(p.File)
	title := p.Package
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{
		Title:         title,
		Description:   firstSentence(p.Doc, fmt.Sprintf("The %s package in the magus daemon API: shared types with no service of their own.", title)),
		GeneratedFrom: "reference/api/",
		Tags:          []string{"api", "proto", "connect", "grpc", strings.ReplaceAll(strings.TrimPrefix(p.Package, "magus."), ".", "-")},
	})

	fmt.Fprintf(&b, "# %s\n\n", title)
	if p.Doc != "" {
		fmt.Fprintf(&b, "%s\n\n", p.Doc)
	}
	fmt.Fprintf(&b, "Package `%s`, defined in `proto/%s`. Source: %s. Declares no service of its own; part of the [daemon API](%s.md).\n\n", p.Package, p.File, sourceLink(p.File, p.Line), relLink(path, "index"))

	msgNames := a.messagesInPackage(p.Package)
	enumNames := a.enumsInPackage(p.Package)
	if len(msgNames) > 0 {
		b.WriteString("## Messages\n\n")
		for _, n := range msgNames {
			writeMessage(&b, a, a.messages[n], usedBy[n], path)
		}
	}
	if len(enumNames) > 0 {
		b.WriteString("## Enums\n\n")
		for _, n := range enumNames {
			writeEnum(&b, a.enums[n], usedBy[n], path)
		}
	}
	return b.String()
}

func seedsOf(s service) []string {
	seeds := make([]string, 0, 2*len(s.Methods))
	for _, m := range s.Methods {
		seeds = append(seeds, m.Input, m.Output)
	}
	return seeds
}

// reachableFrom walks the type graph transitively from a set of seed type names, staying
// within pkg. A one-hop walk documents only inputs and outputs, which omits the types they
// carry - ListEventsResponse holds `repeated Event`, and Event is the message a client most
// needs. A type declared in a different package is a foreign reference: it is linked where
// it is used (typeLink), not walked further or rendered again here - it has its own
// canonical page.
func (a api) reachableFrom(pkg string, seeds []string) (msgs []string, enums []string) {
	seen := map[string]bool{}
	queue := slices.Clone(seeds)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if seen[n] {
			continue
		}
		seen[n] = true
		if msg, ok := a.messages[n]; ok {
			if msg.Package != pkg {
				continue
			}
			msgs = append(msgs, n)
			queue = append(queue, msg.refs...)
			continue
		}
		if en, ok := a.enums[n]; ok {
			if en.Package != pkg {
				continue
			}
			enums = append(enums, n)
		}
		// Anything else is a well-known type: named at the call site, not ours to document.
	}
	slices.Sort(msgs)
	slices.Sort(enums)
	return msgs, enums
}

func writeMessage(b *strings.Builder, a api, m message, used []usage, fromPath string) {
	fmt.Fprintf(b, "### %s\n\n", leaf(m.Name))
	if m.Deprecated {
		b.WriteString("**Deprecated.**\n\n")
	}
	if m.Doc != "" {
		fmt.Fprintf(b, "%s\n\n", m.Doc)
	}
	fmt.Fprintf(b, "Source: %s.\n\n", sourceLink(m.File, m.Line))
	if len(m.Fields) == 0 {
		b.WriteString("No fields.\n\n")
	} else {
		b.WriteString("| Field | Type | # | Description |\n")
		b.WriteString("|-------|------|---|-------------|\n")
		for _, f := range m.Fields {
			var notes []string
			if f.Deprecated {
				notes = append(notes, "**Deprecated.**")
			}
			if f.OneOf != "" {
				if f.OneOfRequired {
					notes = append(notes, fmt.Sprintf("_one of `%s`, required_", f.OneOf))
				} else {
					notes = append(notes, fmt.Sprintf("_one of `%s`_", f.OneOf))
				}
			}
			if f.Optional {
				notes = append(notes, "_optional_")
			}
			if f.Constraints != "" {
				notes = append(notes, "_"+f.Constraints+"_")
			}
			desc := strings.TrimSpace(strings.Join(append(notes, f.Doc), " "))
			typeCell := f.Type
			if f.Ref != "" {
				typeCell = a.typeLink(f.Type, f.Ref, m.Package, fromPath)
			}
			fmt.Fprintf(b, "| `%s` | %s | %d | %s |\n", f.Name, typeCell, f.Number, desc)
		}
		b.WriteString("\n")
	}
	if m.Reserved != "" {
		fmt.Fprintf(b, "_%s_\n\n", m.Reserved)
	}
	writeUsedBy(b, used, fromPath)
}

func writeEnum(b *strings.Builder, e enumType, used []usage, fromPath string) {
	fmt.Fprintf(b, "### %s\n\n", leaf(e.Name))
	if e.Deprecated {
		b.WriteString("**Deprecated.**\n\n")
	}
	if e.Doc != "" {
		fmt.Fprintf(b, "%s\n\n", e.Doc)
	}
	fmt.Fprintf(b, "Source: %s.\n\n", sourceLink(e.File, e.Line))
	b.WriteString("| Value | # | Description |\n")
	b.WriteString("|-------|---|-------------|\n")
	for _, v := range e.Values {
		doc := v.Doc
		if v.Deprecated {
			doc = strings.TrimSpace("**Deprecated.** " + doc)
		}
		fmt.Fprintf(b, "| `%s` | %d | %s |\n", v.Name, v.Number, doc)
	}
	b.WriteString("\n")
	if e.Reserved != "" {
		fmt.Fprintf(b, "_%s_\n\n", e.Reserved)
	}
	writeUsedBy(b, used, fromPath)
}

func writeUsedBy(b *strings.Builder, used []usage, fromPath string) {
	if len(used) == 0 {
		return
	}
	labels := make([]string, len(used))
	for i, u := range used {
		labels[i] = fmt.Sprintf("[%s](%s.md#%s)", u.label, relLink(fromPath, u.pagePath), u.anchor)
	}
	fmt.Fprintf(b, "Used by: %s.\n\n", strings.Join(labels, ", "))
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
