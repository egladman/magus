package cache

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func TestOutputImportNamesOnlyAShapedFailedNeedRef(t *testing.T) {
	key := strings.Repeat("ab", 32)
	rec := func(ref string) []byte {
		b, err := jsonMarshalLine(types.StoredOutput{
			Schema: types.Schema{Version: types.StoredOutputAPIVersion},
			OutputDescriptor: types.OutputDescriptor{
				Ref: PortableRef(key), Key: key, Project: "libs/x", Target: "lint", Failed: true,
				FailedNeedTarget: "lint-go\x1b[2J", FailedNeedRef: ref,
			},
		})
		require.NoError(t, err)
		return b
	}

	var stdout, stderr bytes.Buffer
	require.NoError(t, PrintOutputRecords(bytes.NewReader(rec("out0123456789ab")), &stdout, &stderr))
	assert.Contains(t, stderr.String(), "magus query output out0123456789ab")
	assert.NotContains(t, stderr.String(), "\x1b", "the member's target is neutralized like the error")

	stderr.Reset()
	require.NoError(t, PrintOutputRecords(bytes.NewReader(rec("x; rm -rf ~")), &stdout, &stderr))
	assert.NotContains(t, stderr.String(), "rm -rf", "a ref no magus mints is never echoed as a command")
	assert.NotContains(t, stderr.String(), "failed:")
}

func TestOutputImportNeutralizesWhatItPrints(t *testing.T) {
	key := strings.Repeat("ab", 32)
	rec := types.StoredOutput{
		Schema: types.Schema{Version: types.StoredOutputAPIVersion},
		OutputDescriptor: types.OutputDescriptor{
			Ref: PortableRef(key), Key: key, Project: "libs/x", Target: "lint", Failed: true,
			ErrMsg:   "exit 1\x1b]52;c;cm0gLXJmIH4=\x07\nrun: magus x out000000000000",
			Platform: "linux/amd64", Revision: strings.Repeat("c", 40), MagusVersion: "v9.9.9",
			Spell: "go::go-build", ExtraArgs: []string{"-run", "X"},
		},
		ClassDigests: []types.ClassDigest{{Class: "src", Digest: "0123456789ab", Count: 2}},
		Output:       "line\x1b]0;owned\x07\x1b[2J\x1b[31mred\x1b[0m\n",
	}
	b, err := jsonMarshalLine(rec)
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	require.NoError(t, PrintOutputRecords(bytes.NewReader(b), &stdout, &stderr))
	assert.Equal(t, "line<U+001B>]0;owned<U+0007><U+001B>[2J\x1b[31mred\x1b[0m\n", stdout.String())
	assert.NotContains(t, stderr.String(), "\x1b", "no escape reaches stderr")
	assert.Contains(t, stderr.String(), "<U+001B>]52;c;")
	assert.Contains(t, stderr.String(), "3 terminal control(s)")
	assert.Contains(t, stderr.String(), "platform: linux/amd64 (this machine: ")
	assert.Contains(t, stderr.String(), "magus:    v9.9.9")
	assert.Contains(t, stderr.String(), "src 0123456789ab")
	assert.NotContains(t, stderr.String(), "magus x "+rec.Ref, "no reproduce command for a record from elsewhere")
	assert.NotContains(t, stderr.String(), "go::go-build", "spell and args are never echoed as a command")
}

func jsonMarshalLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	return append(b, '\n'), err
}
