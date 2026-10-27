package token

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// secretWords are the credential words of the secretfields linter, snake-cased: a proto
// field whose name ends in one carries a credential.
var secretWords = [][]string{
	{"password"}, {"passwd"}, {"secret"}, {"token"}, {"tokens"}, {"credential"},
	{"api", "key"}, {"bearer"}, {"signing", "key"}, {"private", "key"}, {"hmac"},
}

// TestSecretProtoFieldsAreDebugRedacted reads every magus proto in the committed descriptor
// set and fails a string or bytes field named for a credential that lacks debug_redact,
// which is what keeps the generated String() and prototext from printing it.
func TestSecretProtoFieldsAreDebugRedacted(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "proto", "gen", "descriptor.binpb"))
	require.NoError(t, err)
	var set descriptorpb.FileDescriptorSet
	require.NoError(t, proto.Unmarshal(raw, &set))

	var checked int
	var missing []string
	var walk func(prefix string, msgs []*descriptorpb.DescriptorProto)
	walk = func(prefix string, msgs []*descriptorpb.DescriptorProto) {
		for _, m := range msgs {
			name := fmt.Sprintf("%s.%s", prefix, m.GetName())
			for _, f := range m.GetField() {
				kind := f.GetType()
				if kind != descriptorpb.FieldDescriptorProto_TYPE_STRING && kind != descriptorpb.FieldDescriptorProto_TYPE_BYTES {
					continue
				}
				if !namesSecret(f.GetName()) || paginationCursor(m, f) {
					continue
				}
				checked++
				if !f.GetOptions().GetDebugRedact() {
					missing = append(missing, fmt.Sprintf("%s.%s", name, f.GetName()))
				}
			}
			walk(name, m.GetNestedType())
		}
	}
	for _, file := range set.GetFile() {
		if strings.HasPrefix(file.GetPackage(), "magus.") {
			walk(file.GetPackage(), file.GetMessageType())
		}
	}
	require.NotZero(t, checked, "no credential field found, so the descriptor set or the word list is wrong")
	require.Empty(t, missing, "mark each [debug_redact = true]")
}

// paginationCursor reports whether f is AIP-158's opaque list cursor, which the AIP names
// page_token: a request's page_token beside a page_size, or a response's next_page_token
// beside a repeated field. It is a position in a list, not a credential.
func paginationCursor(m *descriptorpb.DescriptorProto, f *descriptorpb.FieldDescriptorProto) bool {
	switch f.GetName() {
	case "page_token":
		return slices.ContainsFunc(m.GetField(), func(o *descriptorpb.FieldDescriptorProto) bool { return o.GetName() == "page_size" })
	case "next_page_token":
		return slices.ContainsFunc(m.GetField(), func(o *descriptorpb.FieldDescriptorProto) bool {
			return o.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED
		})
	}
	return false
}

func namesSecret(field string) bool {
	parts := strings.Split(field, "_")
	for _, w := range secretWords {
		if len(w) <= len(parts) && slices.Equal(parts[len(parts)-len(w):], w) {
			return true
		}
	}
	return false
}
