package transform

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestRunReturnsJSON(t *testing.T) {
	got, err := Run(context.Background(), Request{
		Script: `fun transform(input: any, args: [str]) > any { return input; }`,
		Input:  json.RawMessage(`{"name":"magus","count":2}`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"name":"magus","count":2}`, string(got.JSON))
}

// Input numbers decode the way serialize\jsonDecode decodes them, so an
// integral count arrives as an int a script can index or compare with.
func TestRunDecodesIntegralInputAsInt(t *testing.T) {
	got, err := Run(context.Background(), Request{
		Script: `fun transform(input: any, args: [str]) > any { return input is int; }`,
		Input:  json.RawMessage(`2`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `true`, string(got.JSON))
}

func TestRunDoesNotImportHostModulesOrFiles(t *testing.T) {
	for _, tc := range []struct {
		name       string
		importPath string
		want       string
	}{
		{"magus", "magus", "use the client tool"},
		{"prefixed magus", "buzz:magus", "use the client tool"},
		{"filesystem module", "fs", "outside this tool; it provides only std, math, crypto, serialize, buffer"},
		{"debug module", "debug", "outside this tool"},
		{"file", "./helper.buzz", "file imports are unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Run(context.Background(), Request{
				Script: `import "` + tc.importPath + `"; fun transform(input: any, args: [str]) > any { return input; }`,
			})
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestRunLimitsInputBeforeExecuting(t *testing.T) {
	_, err := Run(context.Background(), Request{
		Script: strings.Repeat("x", MaxSourceBytes+1),
	})
	assert.ErrorContains(t, err, "buzz: script must be")
	_, err = Run(context.Background(), Request{
		Script: `fun transform(input: any, args: [str]) > any { return input; }`,
		Input:  json.RawMessage(strings.Repeat("x", MaxInputBytes+1)),
	})
	assert.ErrorContains(t, err, "buzz: input exceeds")
	_, err = Run(context.Background(), Request{
		Script: `fun transform(input: any, args: [str]) > any { return input; }`,
		Args:   make([]string, MaxArgs+1),
	})
	assert.ErrorContains(t, err, "buzz: more than 64 arguments")
}

func TestMagusImportHasDiagnostic(t *testing.T) {
	_, err := Run(context.Background(), Request{
		Script: `import "magus"; fun transform(input: any, args: [str]) > any { return input; }`,
	})
	require.ErrorIs(t, err, types.MCPBuzzFailed)
}

func TestServe(t *testing.T) {
	t.Setenv(WorkerEnv, "1")
	wire, err := json.Marshal(Request{
		Script: `fun transform(input: any, args: [str]) > any { return input; }`,
		Input:  json.RawMessage(`{"ok":true}`),
	})
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	require.Zero(t, Serve(context.Background(), bytes.NewReader(wire), &stdout, &stderr), stderr.String())
	_, set := os.LookupEnv(WorkerEnv)
	assert.False(t, set, "a nested magus would start as another worker")
	var result Result
	require.NoError(t, json.UnmarshalStrict(stdout.Bytes(), &result))
	assert.JSONEq(t, `{"ok":true}`, string(result.JSON))
}

func TestServeRejectsOversizedRequest(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := strings.NewReader(strings.Repeat("x", MaxRequestBytes+1))
	assert.Equal(t, 1, Serve(context.Background(), input, &stdout, &stderr))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "buzz: request is too large")
}
