package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	validate "buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/egladman/magus/internal/docs"
)

// The fixture below stands in for the real descriptor set. It is hand-built
// rather than compiled from .proto text because this generator's whole job is
// reading a descriptor set, and the shapes worth pinning - a synthetic oneof
// behind proto3 `optional`, a map entry, a reserved range, a validate rule, a
// cross-package reference, a package with no service - are all descriptor-level
// facts that a fixture in .proto form would still have to be compiled into.
const (
	queryPath  = "magus/query/v1/query.proto"
	tokenPath  = "magus/token/v1/token.proto"
	legacyPath = "magus/legacy/v1/legacy.proto"
)

func fld(name string, num int32, t descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(num),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   t.Enum(),
	}
}

func ref(name string, num int32, t descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
	f := fld(name, num, t)
	f.TypeName = proto.String(typeName)
	return f
}

func repeated(f *descriptorpb.FieldDescriptorProto) *descriptorpb.FieldDescriptorProto {
	f.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
	return f
}

func enumVal(name string, num int32) *descriptorpb.EnumValueDescriptorProto {
	return &descriptorpb.EnumValueDescriptorProto{Name: proto.String(name), Number: proto.Int32(num)}
}

// loc places a comment at a line. The span is what protodesc turns into the line
// number the page's source links point at; the columns are never read.
func loc(path []int32, line int32, leading, trailing string) *descriptorpb.SourceCodeInfo_Location {
	return &descriptorpb.SourceCodeInfo_Location{
		Path:             path,
		Span:             []int32{line, 0, 40},
		LeadingComments:  proto.String(leading),
		TrailingComments: proto.String(trailing),
	}
}

// mapEntry builds the synthetic entry message behind a map<string, V> field.
func mapEntry(name string, value *descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
	value.Name = proto.String("value")
	value.Number = proto.Int32(2)
	return &descriptorpb.DescriptorProto{
		Name:    proto.String(name),
		Field:   []*descriptorpb.FieldDescriptorProto{fld("key", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING), value},
		Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
	}
}

func filterOptions() *descriptorpb.FieldOptions {
	opts := &descriptorpb.FieldOptions{}
	proto.SetExtension(opts, validate.E_Field, &validate.FieldRules{
		Required: proto.Bool(true),
		Type: &validate.FieldRules_String_{String_: &validate.StringRules{
			MinLen:  proto.Uint64(2),
			Pattern: proto.String(`^(shared|private)/.+$`),
		}},
	})
	return opts
}

func selectorOptions() *descriptorpb.OneofOptions {
	opts := &descriptorpb.OneofOptions{}
	proto.SetExtension(opts, validate.E_Oneof, &validate.OneofRules{Required: proto.Bool(true)})
	return opts
}

func fixtureSet() *descriptorpb.FileDescriptorSet {
	query := &descriptorpb.FileDescriptorProto{
		Name:    proto.String(queryPath),
		Package: proto.String("magus.query.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("TimeRange"),
			Field: []*descriptorpb.FieldDescriptorProto{
				fld("start", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
				fld("end", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			},
		}},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name:          proto.String("Order"),
			Value:         []*descriptorpb.EnumValueDescriptorProto{enumVal("ORDER_UNSPECIFIED", 0), enumVal("ORDER_ASC", 1)},
			ReservedRange: []*descriptorpb.EnumDescriptorProto_EnumReservedRange{{Start: proto.Int32(7), End: proto.Int32(9)}},
			ReservedName:  []string{"ORDER_LEGACY"},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{
			loc([]int32{2}, 1, "Shared query types. Nothing here declares a service.\n\nRanges are half-open and read start|end.\n", ""),
			loc([]int32{4, 0}, 5, "A window of time.\n", ""),
			loc([]int32{5, 0}, 11, "Sort order.\n", ""),
			loc([]int32{5, 0, 2, 1}, 13, "", "Oldest first."),
		}},
	}

	token := &descriptorpb.FileDescriptorProto{
		Name:       proto.String(tokenPath),
		Package:    proto.String("magus.token.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{queryPath},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("ListTokensRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					func() *descriptorpb.FieldDescriptorProto {
						f := fld("filter", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING)
						f.Options = filterOptions()
						return f
					}(),
					fld("page_size", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
					ref("scope", 3, descriptorpb.FieldDescriptorProto_TYPE_ENUM, ".magus.token.v1.TokenScope"),
					fld("since", 4, descriptorpb.FieldDescriptorProto_TYPE_INT64),
					fld("include_expired", 5, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					ref("window", 6, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".magus.query.v1.TimeRange"),
					repeated(fld("tags", 7, descriptorpb.FieldDescriptorProto_TYPE_STRING)),
					func() *descriptorpb.FieldDescriptorProto {
						f := fld("cursor", 8, descriptorpb.FieldDescriptorProto_TYPE_STRING)
						f.OneofIndex = proto.Int32(0)
						f.Proto3Optional = proto.Bool(true)
						return f
					}(),
				},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_cursor")}},
			},
			{
				Name: proto.String("ListTokensResponse"),
				Field: []*descriptorpb.FieldDescriptorProto{
					repeated(ref("tokens", 1, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".magus.token.v1.Token")),
					repeated(ref("labels", 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".magus.token.v1.ListTokensResponse.LabelsEntry")),
					repeated(ref("by_id", 3, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".magus.token.v1.ListTokensResponse.ByIdEntry")),
				},
				NestedType: []*descriptorpb.DescriptorProto{
					mapEntry("LabelsEntry", fld("value", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING)),
					mapEntry("ByIdEntry", ref("value", 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".magus.token.v1.Token")),
				},
			},
			{
				Name: proto.String("Token"),
				Field: []*descriptorpb.FieldDescriptorProto{
					fld("id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
					ref("scope", 2, descriptorpb.FieldDescriptorProto_TYPE_ENUM, ".magus.token.v1.TokenScope"),
					ref("expires_at", 3, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".magus.query.v1.TimeRange"),
					func() *descriptorpb.FieldDescriptorProto {
						f := fld("legacy", 7, descriptorpb.FieldDescriptorProto_TYPE_STRING)
						f.Options = &descriptorpb.FieldOptions{Deprecated: proto.Bool(true)}
						return f
					}(),
					ref("mode", 8, descriptorpb.FieldDescriptorProto_TYPE_ENUM, ".magus.token.v1.Mode"),
				},
				ReservedRange: []*descriptorpb.DescriptorProto_ReservedRange{
					{Start: proto.Int32(4), End: proto.Int32(6)},
					{Start: proto.Int32(9), End: proto.Int32(10)},
				},
				ReservedName: []string{"legacy_id"},
			},
			{
				Name: proto.String("WatchRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					func() *descriptorpb.FieldDescriptorProto {
						f := fld("by_id", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING)
						f.OneofIndex = proto.Int32(0)
						return f
					}(),
					func() *descriptorpb.FieldDescriptorProto {
						f := fld("by_name", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING)
						f.OneofIndex = proto.Int32(0)
						return f
					}(),
					func() *descriptorpb.FieldDescriptorProto {
						f := fld("only_active", 3, descriptorpb.FieldDescriptorProto_TYPE_BOOL)
						f.OneofIndex = proto.Int32(1)
						return f
					}(),
				},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{
					{Name: proto.String("selector"), Options: selectorOptions()},
					{Name: proto.String("extra")},
				},
				Options: &descriptorpb.MessageOptions{Deprecated: proto.Bool(true)},
			},
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{
			{
				Name: proto.String("TokenScope"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					enumVal("TOKEN_SCOPE_UNSPECIFIED", 0),
					func() *descriptorpb.EnumValueDescriptorProto {
						v := enumVal("TOKEN_SCOPE_CONNECTOR", 1)
						v.Options = &descriptorpb.EnumValueOptions{Deprecated: proto.Bool(true)}
						return v
					}(),
				},
			},
			{
				Name:    proto.String("Mode"),
				Value:   []*descriptorpb.EnumValueDescriptorProto{enumVal("MODE_UNSPECIFIED", 0)},
				Options: &descriptorpb.EnumOptions{Deprecated: proto.Bool(true)},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: proto.String("TokenService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{
					Name:       proto.String("ListTokens"),
					InputType:  proto.String(".magus.token.v1.ListTokensRequest"),
					OutputType: proto.String(".magus.token.v1.ListTokensResponse"),
				},
				{
					Name:            proto.String("WatchTokens"),
					InputType:       proto.String(".magus.token.v1.WatchRequest"),
					OutputType:      proto.String(".magus.token.v1.Token"),
					ServerStreaming: proto.Bool(true),
				},
				{
					Name:       proto.String("RevokeToken"),
					InputType:  proto.String(".magus.token.v1.WatchRequest"),
					OutputType: proto.String(".magus.token.v1.Token"),
					Options:    &descriptorpb.MethodOptions{Deprecated: proto.Bool(true)},
				},
			},
		}},
		SourceCodeInfo: &descriptorpb.SourceCodeInfo{Location: []*descriptorpb.SourceCodeInfo_Location{
			loc([]int32{2}, 2, "The token contract.\n", ""),
			loc([]int32{6, 0}, 11, "Issues and lists tokens. Everything the daemon accepts as auth comes from here.\n", ""),
			loc([]int32{6, 0, 2, 0}, 13, "Lists tokens, newest first.\n", ""),
			loc([]int32{6, 0, 2, 1}, 15, "Streams tokens as they are issued.\n", ""),
			loc([]int32{4, 0}, 20, "The filter and paging for ListTokens.\n", ""),
			loc([]int32{4, 0, 2, 0}, 22, "", "The prefix to match: shared|private. See the STATE_ note."),
			loc([]int32{4, 2}, 40, "One issued token.\n\nSee https://example.com/tokens for the STATE_ prefix.\n", ""),
			loc([]int32{5, 0}, 60, "What a token may reach.\n", ""),
			loc([]int32{5, 0, 2, 1}, 63, "", "A connector token."),
		}},
	}

	legacy := &descriptorpb.FileDescriptorProto{
		Name:    proto.String(legacyPath),
		Package: proto.String("magus.legacy.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("PingRequest")},
			{Name: proto.String("PingResponse"), Field: []*descriptorpb.FieldDescriptorProto{
				fld("pong", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING),
			}},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:    proto.String("LegacyService"),
			Options: &descriptorpb.ServiceOptions{Deprecated: proto.Bool(true)},
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       proto.String("Ping"),
				InputType:  proto.String(".magus.legacy.v1.PingRequest"),
				OutputType: proto.String(".magus.legacy.v1.PingResponse"),
			}},
		}},
	}

	// A magus package that declares nothing has nowhere to be documented, and a
	// package outside the contract is not magus's to document at all.
	empty := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("magus/empty/v1/empty.proto"),
		Package: proto.String("magus.empty.v1"),
		Syntax:  proto.String("proto3"),
	}
	foreign := &descriptorpb.FileDescriptorProto{
		Name:        proto.String("third/party/other.proto"),
		Package:     proto.String("other.v1"),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Foreign")}},
	}

	return &descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{query, token, legacy, empty, foreign}}
}

func fixtureAPI(t *testing.T) api {
	t.Helper()
	a, err := newAPI(fixtureSet())
	require.NoError(t, err)
	return a
}

func fieldNamed(t *testing.T, m message, name string) field {
	t.Helper()
	for _, f := range m.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s has no field %q", m.Name, name)
	return field{}
}

// TestNewAPIKeepsOnlyMagusContract pins the filter. The module pulls in
// protovalidate and the well-known types, and documenting those would bury the
// API in vendored schema - so a package outside magus.* is skipped, and a magus
// package that declares nothing gets no page of its own either.
func TestNewAPIKeepsOnlyMagusContract(t *testing.T) {
	a := fixtureAPI(t)

	require.Len(t, a.services, 2)
	assert.Equal(t, "LegacyService", a.services[0].Name, "services sort by package then name")
	assert.Equal(t, "TokenService", a.services[1].Name)

	assert.Equal(t, map[string]string{
		"magus.legacy.v1": "legacy/v1/legacy",
		"magus.token.v1":  "token/v1/token",
	}, a.svcOfPkg)

	require.Len(t, a.packages, 1, "only a magus package with types and no service gets a standalone page")
	assert.Contains(t, a.packages, "magus.query.v1")

	assert.Contains(t, a.messages, "magus.token.v1.Token")
	assert.NotContains(t, a.messages, "other.v1.Foreign", "a non-magus package is not ours to document")
	assert.NotContains(t, a.messages, "magus.token.v1.ListTokensResponse.LabelsEntry",
		"a map entry is an implementation detail of the map syntax, never a type a client sees")
}

func TestNewAPIRejectsAnUnresolvableSet(t *testing.T) {
	_, err := newAPI(&descriptorpb.FileDescriptorSet{File: []*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("magus/broken/v1/broken.proto"),
		Package: proto.String("magus.broken.v1"),
		Syntax:  proto.String("proto9"),
	}}})
	assert.ErrorContains(t, err, "resolve descriptor set")
}

// TestFieldShapesAreReadOffTheDescriptor asserts the whole row for each field a
// client author reads: the display type, the fully qualified target a link is
// built from, and the presence flags. Every one of these is a decision the
// generator makes rather than a value it copies.
func TestFieldShapesAreReadOffTheDescriptor(t *testing.T) {
	a := fixtureAPI(t)
	req := a.messages["magus.token.v1.ListTokensRequest"]

	assert.Equal(t, field{
		Name: "window", JSONName: "window", Number: 6,
		Kind: protoreflect.MessageKind,
		Type: "TimeRange", Ref: "magus.query.v1.TimeRange",
	}, fieldNamed(t, req, "window"))

	assert.Equal(t, field{
		Name: "tags", JSONName: "tags", Number: 7,
		Kind: protoreflect.StringKind, Repeated: true, Type: "repeated string",
	}, fieldNamed(t, req, "tags"))

	assert.Equal(t, field{
		Name: "scope", JSONName: "scope", Number: 3,
		Kind: protoreflect.EnumKind, RefIsEnum: true, EnumFallback: "TOKEN_SCOPE_CONNECTOR",
		Type: "TokenScope", Ref: "magus.token.v1.TokenScope",
	}, fieldNamed(t, req, "scope"))

	assert.Equal(t, field{
		Name: "page_size", JSONName: "pageSize", Number: 2,
		Kind: protoreflect.Int32Kind, Type: "int32",
	}, fieldNamed(t, req, "page_size"))
}

// TestProto3OptionalIsNotReportedAsAOneof guards a real misstatement: proto3
// `optional` is implemented as a single-member synthetic oneof, and reporting it
// as one would tell a client two fields are mutually exclusive when there is only
// one of them.
func TestProto3OptionalIsNotReportedAsAOneof(t *testing.T) {
	a := fixtureAPI(t)
	cursor := fieldNamed(t, a.messages["magus.token.v1.ListTokensRequest"], "cursor")

	assert.Equal(t, field{
		Name: "cursor", JSONName: "cursor", Number: 8,
		Kind: protoreflect.StringKind, Type: "string", Optional: true,
	}, cursor)
}

// TestRealOneofsCarryTheirRequiredRule pins the note rendered above a oneof's
// fields: (buf.validate.oneof).required means exactly one must be set, and a
// oneof without it means at most one.
func TestRealOneofsCarryTheirRequiredRule(t *testing.T) {
	a := fixtureAPI(t)
	watch := a.messages["magus.token.v1.WatchRequest"]

	byID := fieldNamed(t, watch, "by_id")
	assert.Equal(t, "selector", byID.OneOf)
	assert.True(t, byID.OneOfRequired)
	assert.Equal(t, "selector", fieldNamed(t, watch, "by_name").OneOf)

	onlyActive := fieldNamed(t, watch, "only_active")
	assert.Equal(t, "extra", onlyActive.OneOf)
	assert.False(t, onlyActive.OneOfRequired)
}

// TestMapValueTypesStayReachable is the reason refs records a map's value type:
// a map cell is always plain text, so a magus type reachable only through one
// would never be rendered on any page without this.
func TestMapValueTypesStayReachable(t *testing.T) {
	a := fixtureAPI(t)
	resp := a.messages["magus.token.v1.ListTokensResponse"]

	labels := fieldNamed(t, resp, "labels")
	assert.Equal(t, "map<string, string>", labels.Type)
	assert.Equal(t, "", labels.Ref, "a map cell carries no inline link")

	byID := fieldNamed(t, resp, "by_id")
	assert.Equal(t, "map<string, Token>", byID.Type)
	assert.Equal(t, "", byID.Ref)

	assert.Contains(t, resp.refs, "magus.token.v1.Token")
}

// TestReservedNotesExplainTheGaps: a reader meeting a jump in the field-number
// column should see it explained rather than read as an omission.
func TestReservedNotesExplainTheGaps(t *testing.T) {
	a := fixtureAPI(t)

	assert.Equal(t, "Reserved: 4 to 5, 9; `legacy_id`.", a.messages["magus.token.v1.Token"].Reserved,
		"a message's reserved range is half-open, so 4 to 6 covers 4 and 5")
	assert.Equal(t, "Reserved: 7 to 9; `ORDER_LEGACY`.", a.enums["magus.query.v1.Order"].Reserved,
		"an enum's reserved range is inclusive at both ends")
	assert.Equal(t, "", a.enums["magus.token.v1.TokenScope"].Reserved)
}

func TestDeprecationIsCarriedFromEveryOptionsMessage(t *testing.T) {
	a := fixtureAPI(t)

	assert.True(t, a.services[0].Deprecated, "LegacyService is deprecated")
	assert.False(t, a.services[1].Deprecated)
	assert.True(t, a.services[1].Methods[2].Deprecated, "RevokeToken is deprecated")
	assert.False(t, a.services[1].Methods[0].Deprecated)
	assert.True(t, a.messages["magus.token.v1.WatchRequest"].Deprecated)
	assert.True(t, a.enums["magus.token.v1.Mode"].Deprecated)
	assert.True(t, fieldNamed(t, a.messages["magus.token.v1.Token"], "legacy").Deprecated)
	assert.True(t, a.enums["magus.token.v1.TokenScope"].Values[1].Deprecated)
}

func TestIsDeprecatedIgnoresWhatCannotCarryIt(t *testing.T) {
	assert.False(t, isDeprecated(nil))
	assert.False(t, isDeprecated(&descriptorpb.FileDescriptorProto{}), "a message with no Deprecated field is not deprecated")
}

// TestEnumFallbackPrefersARealValue: a request field left at its zero value
// would round-trip but says nothing about what a client would actually send, so
// the JSON example names a value other than the conventional _UNSPECIFIED = 0.
func TestEnumFallbackPrefersARealValue(t *testing.T) {
	a := fixtureAPI(t)

	scope := fieldNamed(t, a.messages["magus.token.v1.ListTokensRequest"], "scope")
	assert.Equal(t, "TOKEN_SCOPE_CONNECTOR", scope.EnumFallback)

	mode := fieldNamed(t, a.messages["magus.token.v1.Token"], "mode")
	assert.Equal(t, "MODE_UNSPECIFIED", mode.EnumFallback, "an enum with only a zero value falls back to it")
}

func TestMethodKind(t *testing.T) {
	for _, tc := range []struct {
		client, server bool
		want           string
	}{
		{false, false, "unary"},
		{false, true, "server streaming"},
		{true, false, "client streaming"},
		{true, true, "bidirectional streaming"},
	} {
		m := method{ClientStreaming: tc.client, ServerStreaming: tc.server}
		assert.Equal(t, tc.want, m.kind())
	}
}

// TestReachableFromWalksTransitivelyWithinOnePackage is the fix for a one-hop
// walk documenting only inputs and outputs: the types those carry are the ones a
// client most needs, and Mode - two hops down, through Token - is exactly what a
// one-hop walk would leave undocumented while linking to it.
func TestReachableFromWalksTransitivelyWithinOnePackage(t *testing.T) {
	a := fixtureAPI(t)
	msgs, enums := a.reachableFrom("magus.token.v1", seedsOf(a.services[1]))

	assert.Contains(t, msgs, "magus.token.v1.Token")
	assert.Contains(t, msgs, "magus.token.v1.ListTokensRequest")
	assert.NotContains(t, msgs, "magus.query.v1.TimeRange", "a foreign type is linked where it is used, not rendered again")
	assert.Contains(t, enums, "magus.token.v1.TokenScope")
	assert.Contains(t, enums, "magus.token.v1.Mode")
}

// TestComputeUsedByReachesThroughNesting: a message nested inside a response can
// still say which RPCs return it, and each RPC is listed once however many paths
// reach the type.
func TestComputeUsedByReachesThroughNesting(t *testing.T) {
	a := fixtureAPI(t)
	usedBy := a.computeUsedBy()

	var labels []string
	for _, u := range usedBy["magus.query.v1.TimeRange"] {
		labels = append(labels, u.label)
	}
	assert.Contains(t, labels, "ListTokens (request)")
	assert.Contains(t, labels, "ListTokens (response)", "TimeRange hangs off Token, which hangs off the response")

	seen := map[string]bool{}
	for _, l := range labels {
		assert.False(t, seen[l], "duplicate usage entry %q", l)
		seen[l] = true
	}
}

// TestExampleJSONIsAShallowSkeleton pins every placeholder rule at once,
// including the one a client author gets wrong: the 64-bit family serializes as
// a JSON string because large values overflow a JSON number's safe range.
func TestExampleJSONIsAShallowSkeleton(t *testing.T) {
	a := fixtureAPI(t)

	assert.Equal(t,
		`{"filter":"","pageSize":0,"scope":"TOKEN_SCOPE_CONNECTOR","since":"0","includeExpired":false,"cursor":""}`,
		a.exampleJSON("magus.token.v1.ListTokensRequest"))
	assert.Equal(t, "{}", a.exampleJSON("magus.legacy.v1.PingRequest"), "a message with no fields takes none")
	assert.Equal(t, "{}", a.exampleJSON("magus.nowhere.v1.Unknown"))
}

func TestPlaceholderFor(t *testing.T) {
	for _, tc := range []struct {
		what string
		f    field
		want string
	}{
		{"string", field{Kind: protoreflect.StringKind}, `""`},
		{"bytes is base64, so a string", field{Kind: protoreflect.BytesKind}, `""`},
		{"bool", field{Kind: protoreflect.BoolKind}, "false"},
		{"int64 as a string", field{Kind: protoreflect.Int64Kind}, `"0"`},
		{"uint64 as a string", field{Kind: protoreflect.Uint64Kind}, `"0"`},
		{"fixed64 as a string", field{Kind: protoreflect.Fixed64Kind}, `"0"`},
		{"int32 as a number", field{Kind: protoreflect.Int32Kind}, "0"},
		{"an enum names a value", field{RefIsEnum: true, EnumFallback: "X"}, `"X"`},
		{"an enum with nothing to name", field{RefIsEnum: true}, `""`},
	} {
		assert.Equal(t, tc.want, placeholderFor(tc.f), tc.what)
	}
}

func TestFirstUnaryFindsNothingInAnEmptyAPI(t *testing.T) {
	_, _, ok := firstUnary(api{})
	assert.False(t, ok)

	a := fixtureAPI(t)
	s, m, ok := firstUnary(a)
	require.True(t, ok)
	assert.Equal(t, "LegacyService", s.Name)
	assert.Equal(t, "Ping", m.Name)
}

func TestPagePathMirrorsTheProtoPath(t *testing.T) {
	assert.Equal(t, "token/v1/token", pagePath(tokenPath))
	assert.Equal(t, "other/thing", pagePath("other/thing.proto"), "only the magus/ prefix is dropped")
}

// TestRelLink resolves from the page's DIRECTORY, because a relative href is
// read from the page's location: a cross-package reference cannot assume its
// target is a sibling in the same directory.
func TestRelLink(t *testing.T) {
	assert.Equal(t, "../../query/v1/query", relLink("token/v1/token", "query/v1/query"))
	assert.Equal(t, "token", relLink("token/v1/token", "token/v1/token"))
	assert.Equal(t, "../../index", relLink("token/v1/token", "index"))
}

func TestAnchorReproducesTheAutoHeadingID(t *testing.T) {
	for in, want := range map[string]string{
		"magus.token.v1.ListTokensRequest": "listtokensrequest",
		"ListTokens":                       "listtokens",
		"Some_Name-Here":                   "some-name-here",
		"a b":                              "a-b",
		"???":                              "heading",
		"":                                 "heading",
	} {
		assert.Equal(t, want, anchor(in), "anchor(%q)", in)
	}
}

func TestLeaf(t *testing.T) {
	assert.Equal(t, "Token", leaf("magus.token.v1.Token"))
	assert.Equal(t, "Token", leaf("Token"))
}

// TestTypeLinkPicksTheRightTarget covers the three answers: a same-package type
// is a local anchor, a foreign one is that type's canonical page, and anything
// this run never saw renders as plain code rather than a broken link.
func TestTypeLinkPicksTheRightTarget(t *testing.T) {
	a := fixtureAPI(t)
	const from = "token/v1/token"

	assert.Equal(t, "string", a.typeLink("string", "", "magus.token.v1", from), "a scalar is not a link")
	assert.Equal(t, "[Token](#token)", a.typeLink("Token", "magus.token.v1.Token", "magus.token.v1", from))
	assert.Equal(t, "[TimeRange](../../query/v1/query.md#timerange)",
		a.typeLink("TimeRange", "magus.query.v1.TimeRange", "magus.token.v1", from))
	assert.Equal(t, "`Ghost`", a.typeLink("Ghost", "magus.ghost.v1.Ghost", "magus.token.v1", from),
		"a type this run never saw has no page to link to")
}

func TestPageForAndPackageOf(t *testing.T) {
	a := fixtureAPI(t)

	path, ok := a.pageFor("magus.token.v1")
	assert.True(t, ok)
	assert.Equal(t, "token/v1/token", path, "a package with a service lives on that service's page")

	path, ok = a.pageFor("magus.query.v1")
	assert.True(t, ok)
	assert.Equal(t, "query/v1/query", path, "a package with no service gets a standalone page")

	_, ok = a.pageFor("google.protobuf")
	assert.False(t, ok)

	assert.Equal(t, "magus.token.v1", a.packageOf("magus.token.v1.Token"))
	assert.Equal(t, "magus.token.v1", a.packageOf("magus.token.v1.TokenScope"))
	assert.Equal(t, "", a.packageOf("google.protobuf.Timestamp"))
}

func TestSourceLink(t *testing.T) {
	assert.Equal(t,
		"[token.proto:12]("+docs.RepoBlob+"/proto/"+tokenPath+"#L12)",
		sourceLink(tokenPath, 12))
}

// TestWriteAllRendersEveryPageAndPrunesTheRest: the drift gate only compares
// files the generator writes, so a page it stops writing is invisible to it - a
// deleted RPC's page would sit in the committed tree describing something the
// daemon no longer serves.
func TestWriteAllRendersEveryPageAndPrunesTheRest(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "token", "v1", "removed.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(stale), 0o755))
	require.NoError(t, os.WriteFile(stale, []byte(generatedPage("Removed")), 0o644))

	n, err := writeAll(fixtureAPI(t), dir)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	for _, name := range []string{"index.md", "token/v1/token.md", "legacy/v1/legacy.md", "query/v1/query.md"} {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name)))
		assert.NoError(t, err, "%s was not written", name)
	}
	_, err = os.Stat(stale)
	assert.True(t, os.IsNotExist(err), "the page for a removed service survived")
}

// generatedPage is a page carrying this generator's ownership marker, which is
// what makes it eligible for pruning.
func generatedPage(title string) string {
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{Title: title, GeneratedFrom: pageGeneratedFrom})
	return b.String()
}

// TestWriteAllPrunesOnlyItsOwnPages: the sweep used to remove every .md under -out
// that the run did not write, so a hand-written page added to the API section - or
// anything else pointed at by a mistaken -out - was deleted without a trace.
func TestWriteAllPrunesOnlyItsOwnPages(t *testing.T) {
	dir := t.TempDir()
	handwritten := filepath.Join(dir, "guide.md")
	require.NoError(t, os.WriteFile(handwritten, []byte("# Calling the API by hand\n"), 0o644))
	foreign := filepath.Join(dir, "other.md")
	var b strings.Builder
	docs.WriteFrontmatter(&b, docs.Frontmatter{Title: "Other", GeneratedFrom: "internal/other/other.go"})
	require.NoError(t, os.WriteFile(foreign, []byte(b.String()), 0o644))

	_, err := writeAll(fixtureAPI(t), dir)
	require.NoError(t, err)

	for _, path := range []string{handwritten, foreign} {
		_, err := os.Stat(path)
		assert.NoError(t, err, "%s carries no marker of this generator and must survive", path)
	}
}

func TestWriteAllRefusesADescriptorSetWithNoServices(t *testing.T) {
	_, err := writeAll(api{}, t.TempDir())
	assert.ErrorContains(t, err, "no magus services found")
}

// TestWriteAllRefusesAnEmptyOutDir: an empty -out makes every page path relative,
// pointing the prune walk at the working directory.
func TestWriteAllRefusesAnEmptyOutDir(t *testing.T) {
	_, err := writeAll(fixtureAPI(t), "")
	assert.ErrorContains(t, err, "-out")
}

func renderedPage(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	_, err := writeAll(fixtureAPI(t), dir)
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	require.NoError(t, err)
	return string(b)
}

// TestServicePageStatesTheCallContract is what a third party builds a client
// from without reading the schema: the HTTP path, the streaming shape, the
// request and response types, and a link into the .proto line that defines each.
func TestServicePageStatesTheCallContract(t *testing.T) {
	body := renderedPage(t, "token/v1/token.md")

	fm, ok := docs.ParseFrontmatter(body)
	require.True(t, ok)
	assert.Equal(t, "TokenService", fm.Title)
	assert.Equal(t, "reference/api/", fm.GeneratedFrom)
	assert.Equal(t, "Issues and lists tokens.", fm.Description)
	assert.Contains(t, fm.Tags, "tokenservice")

	for _, want := range []string{
		"`POST /magus.token.v1.TokenService/ListTokens`: unary. Source: [token.proto:14]",
		"`POST /magus.token.v1.TokenService/WatchTokens`: server streaming.",
		"Takes [ListTokensRequest](#listtokensrequest), returns [ListTokensResponse](#listtokensresponse).",
		"Package `magus.token.v1`, defined in `proto/magus/token/v1/token.proto`.",
		"Part of the [daemon API](../../index.md).",
		"### RevokeToken\n\n**Deprecated.**",
	} {
		assert.Contains(t, body, want)
	}
}

// TestServicePageDocumentsTheTypesItReaches, including the cross-package link
// and the "Used by" backreference a shared type needs to be findable at all.
func TestServicePageDocumentsTheTypesItReaches(t *testing.T) {
	body := renderedPage(t, "token/v1/token.md")

	for _, want := range []string{
		"### Token\n",
		"| `expires_at` | [TimeRange](../../query/v1/query.md#timerange) | 3 |",
		"| `labels` | map<string, string> | 2 |",
		"| `legacy` | string | 7 | **Deprecated.** |",
		"| `by_id` | string | 1 | _one of `selector`, required_ |",
		"| `only_active` | bool | 3 | _one of `extra`_ |",
		"| `cursor` | string | 8 | _optional_ |",
		"_Reserved: 4 to 5, 9; `legacy_id`._",
		"Used by: [ListTokens (request)](token.md#listtokens)",
		"### WatchRequest\n\n**Deprecated.**",
		"### Mode\n\n**Deprecated.**",
		"| `TOKEN_SCOPE_CONNECTOR` | 1 | **Deprecated.** A connector token. |",
	} {
		assert.Contains(t, body, want)
	}
}

// TestProseIsEscapedForTheMarkdownItLandsIn guards three characters a .proto
// author writes without thinking about Markdown: a pipe splits a table row, a
// bare identifier fragment opens an emphasis span that italicizes the rest of the
// page, and a bare URL trips the site's link lint.
func TestProseIsEscapedForTheMarkdownItLandsIn(t *testing.T) {
	body := renderedPage(t, "token/v1/token.md")

	assert.Contains(t, body, `The prefix to match: shared\|private. See the STATE\_ note.`)
	assert.Contains(t, body, "See <https://example.com/tokens> for the STATE\\_ prefix.")
	assert.Contains(t, body, "One issued token.\n\nSee <https://example.com/tokens>",
		"a body comment keeps the author's paragraph break, so the summary is not buried")
	assert.Contains(t, body, "| `filter` | string | 1 | _required; ",
		"a field's constraints are rendered into its row, leading with required")
	assert.Contains(t, body, "string.pattern: `^(shared\\|private)/.+$`",
		"a pipe inside a code span still ends the table cell, so it is escaped there too")
}

// TestConstraintsStateWhatTheDaemonEnforces, so a client learns the rule from
// the reference instead of from a rejected request.
func TestConstraintsStateWhatTheDaemonEnforces(t *testing.T) {
	a := fixtureAPI(t)
	filter := fieldNamed(t, a.messages["magus.token.v1.ListTokensRequest"], "filter")

	assert.True(t, strings.HasPrefix(filter.Constraints, "required;"), "required leads: %q", filter.Constraints)
	assert.Contains(t, filter.Constraints, "string.min_len: 2")
	assert.Contains(t, filter.Constraints, "string.pattern: `^(shared\\|private)/.+$`")

	assert.Equal(t, "", fieldNamed(t, a.messages["magus.token.v1.ListTokensRequest"], "page_size").Constraints,
		"a field with no rules carries no constraint note")
}

// TestDescribeRulesWalksProtovalidateGenerically is why there is no case per
// constraint: a rule this schema starts using tomorrow shows up without a code
// change here, and only free-form CEL - which has no fixed shape - gets a
// fallback.
func TestDescribeRulesWalksProtovalidateGenerically(t *testing.T) {
	rules := &validate.FieldRules{
		Required:      proto.Bool(true),
		CelExpression: []string{"this > 42"},
		Cel:           []*validate.Rule{{Expression: proto.String("this != ''")}},
		Type: &validate.FieldRules_String_{String_: &validate.StringRules{
			In:  []string{"a", "b"},
			Len: proto.Uint64(4),
		}},
	}
	got := describeRules(rules.ProtoReflect())

	assert.NotContains(t, got, "required", "required is the caller's to render, ahead of everything else")
	assert.Contains(t, got, "custom rule: `this > 42`")
	assert.Contains(t, got, "1 custom CEL rule(s)")
	assert.Contains(t, got, "string.in: `a`, `b`")
	assert.Contains(t, got, "string.len: 4")
}

// TestConstraintLeavesRenderInFieldNumberOrder asserts the whole slice rather than
// membership. Every other assertion here is a Contains, so when protoreflect's
// undefined-order Range put two rules of one block in either order, this suite stayed
// green while the generator wrote different bytes each run - only the docs drift gate
// caught it, and a drift gate cannot say which run was right.
func TestConstraintLeavesRenderInFieldNumberOrder(t *testing.T) {
	rules := &validate.FieldRules{Type: &validate.FieldRules_Int32{Int32: &validate.Int32Rules{
		GreaterThan: &validate.Int32Rules_Gte{Gte: 0},
		LessThan:    &validate.Int32Rules_Lte{Lte: 1000},
	}}}

	assert.Equal(t, []string{"int32.lte: 1000", "int32.gte: 0"}, describeRules(rules.ProtoReflect()),
		"lte is field 3 and gte is field 5, so lte leads regardless of the order they were set in")
}

// TestDescribeSubRulesNotesFurtherNesting rather than recursing without bound:
// repeated.items is itself a FieldRules, and nothing in this schema goes deeper.
func TestDescribeSubRulesNotesFurtherNesting(t *testing.T) {
	rules := &validate.FieldRules{Type: &validate.FieldRules_Repeated{Repeated: &validate.RepeatedRules{
		MinItems: proto.Uint64(1),
		Unique:   proto.Bool(true),
		Items:    &validate.FieldRules{},
	}}}
	got := describeRules(rules.ProtoReflect())

	assert.Contains(t, got, "repeated.min_items: 1")
	assert.Contains(t, got, "repeated.unique", "a bool rule renders as its bare name")
	assert.Contains(t, got, "repeated.items constrained")
}

// TestDescribeListRuleSkipsAnEmptyList keeps an unset repeated rule from
// rendering as an empty phrase.
func TestDescribeListRuleSkipsAnEmptyList(t *testing.T) {
	empty := (&validate.FieldRules{}).ProtoReflect()
	fd := empty.Descriptor().Fields().ByName("cel_expression")
	require.NotNil(t, fd)

	assert.Nil(t, describeListRule("cel_expression", fd, empty.Get(fd)))
}

// TestFormatScalarNamesAnEnumValue, falling back to the number when the schema
// carries one the enum does not declare.
func TestFormatScalarNamesAnEnumValue(t *testing.T) {
	fd := (&validate.FieldRules{}).ProtoReflect().Descriptor().Fields().ByName("ignore")
	require.NotNil(t, fd)

	assert.Equal(t, "IGNORE_ALWAYS", formatScalar(fd, protoreflect.ValueOfEnum(3)))
	assert.Equal(t, "9999", formatScalar(fd, protoreflect.ValueOfEnum(9999)))
}

// TestIndexOrientsAClientWithNoGeneratedCode: the index is the page someone
// lands on with nothing but curl, so it has to name a method that really exists
// and say which ones curl cannot demux.
func TestIndexOrientsAClientWithNoGeneratedCode(t *testing.T) {
	body := renderedPage(t, "index.md")

	fm, ok := docs.ParseFrontmatter(body)
	require.True(t, ok)
	assert.Equal(t, "Daemon API", fm.Title)
	assert.Equal(t, "proto/magus/**/*.proto", fm.GeneratedFrom)

	for _, want := range []string{
		"| [TokenService](token/v1/token.md) | 3 | `magus.token.v1` |",
		"| [LegacyService](legacy/v1/legacy.md) | 1 | `magus.legacy.v1` |",
		"## Shared types",
		"| [magus.query.v1](query/v1/query.md) | `proto/magus/query/v1/query.proto` |",
		"http://127.0.0.1:7391/magus.legacy.v1.LegacyService/Ping",
		"-d '{}'",
		"## Streaming methods",
		"- [TokenService.WatchTokens](token/v1/token.md#watchtokens) (server streaming)",
	} {
		assert.Contains(t, body, want)
	}
	assert.NotContains(t, body, "magus.empty.v1", "a package that declares nothing has no page to link to")
}

func TestStreamingMethodsListsNothingWhenEveryMethodIsUnary(t *testing.T) {
	assert.Empty(t, streamingMethods(api{services: []service{{
		Name: "S", Methods: []method{{Name: "M"}},
	}}}))
}

// TestPackagePageDocumentsTypesWithNoServiceOfTheirOwn - without it a type
// declared there has nowhere to live, and every page referencing it would have
// to duplicate it in full.
func TestPackagePageDocumentsTypesWithNoServiceOfTheirOwn(t *testing.T) {
	body := renderedPage(t, "query/v1/query.md")

	fm, ok := docs.ParseFrontmatter(body)
	require.True(t, ok)
	assert.Equal(t, "magus.query.v1", fm.Title)
	assert.Equal(t, "Shared query types.", fm.Description)
	assert.Contains(t, fm.Tags, "query-v1")

	for _, want := range []string{
		"Declares no service of its own; part of the [daemon API](../../index.md).",
		"Ranges are half-open and read start\\|end.",
		"### TimeRange\n",
		"| `start` | string | 1 |",
		"### Order\n",
		"| `ORDER_ASC` | 1 | Oldest first. |",
		"_Reserved: 7 to 9; `ORDER_LEGACY`._",
		"Used by: [ListTokens (request)](../../token/v1/token.md#listtokens)",
	} {
		assert.Contains(t, body, want)
	}
}

// TestAMessageWithNoFieldsSaysSo rather than emitting an empty table.
func TestAMessageWithNoFieldsSaysSo(t *testing.T) {
	body := renderedPage(t, "legacy/v1/legacy.md")

	assert.Contains(t, body, "# LegacyService\n\n**Deprecated.**")
	assert.Contains(t, body, "### PingRequest\n\nSource: [legacy.proto:1]")
	assert.Contains(t, body, "No fields.")
}

func TestFirstSentence(t *testing.T) {
	assert.Equal(t, "fallback", firstSentence("", "fallback"))
	assert.Equal(t, "One claim.", firstSentence("One claim. And the rest.", "fallback"))
	assert.Equal(t, "No break at all", firstSentence("No break at all", "fallback"))
}

func TestCleanFlattensAndEscapes(t *testing.T) {
	assert.Equal(t, `a\|b and STATE\_ prefix`, clean("  a|b and\n  STATE_ prefix  \n"))
	assert.Equal(t, "", clean("   \n  "))
}

// TestEscapeAndLinkifySkipsInsideAURL: Markdown does not interpret backslash
// escapes inside an autolink, so an escaped underscore there would corrupt the
// link rather than render it.
func TestEscapeAndLinkifySkipsInsideAURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"see https://x.dev/a_b now", "see <https://x.dev/a_b> now"},
		{"(http://x.dev/a_b) tail", `(<http://x.dev/a_b>) tail`},
		{"ends at the string https://x.dev/a_b", "ends at the string <https://x.dev/a_b>"},
		{"no url a_b|c", `no url a\_b\|c`},
	} {
		assert.Equal(t, tc.want, escapeAndLinkify(tc.in), "escapeAndLinkify(%q)", tc.in)
	}
}
