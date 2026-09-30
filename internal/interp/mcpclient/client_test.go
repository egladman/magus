package mcpclient

import (
	"bytes"
	"context"
	"errors"
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

func TestRunReturnsMain(t *testing.T) {
	got, err := Run(context.Background(), Request{
		Script: `fun main(args: [str]) > int { return args.len(); }`,
		Args:   []string{"a", "b"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `2`, string(got.JSON))
	assert.Empty(t, got.Stdout)
}

// Only the top-level declaration is the entry: the same word in a string, a
// comment, a method or a recursive call must reach the program as written.
func TestRunRenamesOnlyTheTopLevelMain(t *testing.T) {
	got, err := Run(context.Background(), Request{
		Script: `import "std";

// fun main is the entry.
object Runner {
  fun main() > str { return "method"; }
}

fun main(args: [str]) > any {
  std\print("fun main");
  if (args.len() > 0) { return main([]); }
  return [Runner{}.main(), "done"];
}
`,
		Args: []string{"again"},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `["method","done"]`, string(got.JSON))
	assert.Equal(t, "fun main\nfun main\n", got.Stdout)
}

func TestRunRejectsFilesystemImport(t *testing.T) {
	_, err := Run(context.Background(), Request{
		Script: `import "fs"; fun main(args: [str]) > int { return 1; }`,
	})
	require.ErrorIs(t, err, types.MCPClientFailed)
	assert.Equal(t, 1, strings.Count(err.Error(), "[MGS3034]"), "the diagnostic is reported once: %s", err)
	assert.Contains(t, err.Error(), "outside this tool; it runs magus\\ and pure modules only (std, math, crypto, serialize, buffer,")
}

// A stdlib module the client does not provide is refused with the reason, not
// reported as a missing file import.
func TestRunRejectsWithheldStdlibWithTheReason(t *testing.T) {
	for _, name := range []string{"env", "debug", "gc", "assert", "test", "ffi"} {
		_, err := Run(context.Background(), Request{
			Script: `import "` + name + `"; fun main(args: [str]) > int { return 1; }`,
		})
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "outside this tool", name)
	}
}

func TestRunRejectsCmdAndPry(t *testing.T) {
	for _, member := range []string{`magus\cmd("status", args: [])`, `magus\pry()`} {
		_, err := Run(context.Background(), Request{
			Script: `import "magus"; fun main(args: [str]) > any !> any { return ` + member + `; }`,
		})
		require.Error(t, err, member)
	}
}

func TestRunDescribeModuleOmitsWithheldMembers(t *testing.T) {
	got, err := Run(context.Background(), Request{
		Script: `import "magus";
fun main(args: [str]) > [str] !> str {
  final found: mut [str] = mut [];
  foreach (entry in magus\describeModule("magus")) {
    foreach (method in entry.methods) {
      if (method.name == "cmd" or method.name == "pry") { found.append(method.name); }
    }
  }
  return found;
}`,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `[]`, string(got.JSON))
}

func TestRunAllowsMagusAndJSON(t *testing.T) {
	got, err := Run(context.Background(), Request{
		Script: `import "encoding/json"; import "magus"; fun main(args: [str]) > str !> str { return json\stringify(args.len()); }`,
	})
	require.NoError(t, err)
	assert.JSONEq(t, `"0"`, string(got.JSON))
}

// A value serialize\jsonDecode returns is Boxed, and it must come back as its data.
func TestRunReturnsADecodedValue(t *testing.T) {
	got, err := Run(context.Background(), Request{
		Script: `import "serialize"; fun main(args: [str]) > any !> any { return serialize\jsonDecode(args[0]); }`,
		Args:   []string{`{"n":2}`},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"n":2}`, string(got.JSON))
}

func TestRunLimitsArgs(t *testing.T) {
	script := `fun main(args: [str]) > int { return 0; }`
	_, err := Run(context.Background(), Request{Script: script, Args: make([]string, MaxArgs+1)})
	assert.ErrorContains(t, err, "more than 64 arguments")
	_, err = Run(context.Background(), Request{Script: script, Args: []string{strings.Repeat("x", MaxArgBytes+1)}})
	assert.ErrorContains(t, err, "args[0] exceeds")
}

// The largest request Run accepts must fit the wire bound the worker reads.
func TestMaxRequestBytesHoldsTheLargestRequest(t *testing.T) {
	args := make([]string, MaxArgs)
	for i := range args {
		args[i] = strings.Repeat(`"`, MaxArgBytes)
	}
	wire, err := json.Marshal(Request{Script: strings.Repeat("\n", MaxSourceBytes), Args: args})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(wire), MaxRequestBytes)
}

func TestServe(t *testing.T) {
	t.Setenv(WorkerEnv, "1")
	t.Setenv(HostEnv, "host-x")
	wire, err := json.Marshal(Request{Script: `fun main(args: [str]) > str { return "ok"; }`})
	require.NoError(t, err)
	var sawWorker, sawHost bool
	open := func(ctx context.Context) (context.Context, error) {
		_, sawWorker = os.LookupEnv(WorkerEnv)
		_, sawHost = os.LookupEnv(HostEnv)
		return ctx, nil
	}
	var stdout, stderr bytes.Buffer
	require.Zero(t, Serve(context.Background(), bytes.NewReader(wire), &stdout, &stderr, open), stderr.String())
	assert.False(t, sawWorker, "a nested magus would start as another worker")
	assert.False(t, sawHost, "the script could read the transport channel")
	var result Result
	require.NoError(t, json.UnmarshalStrict(stdout.Bytes(), &result))
	assert.JSONEq(t, `"ok"`, string(result.JSON))
}

func TestServeReportsFailures(t *testing.T) {
	ok := func(ctx context.Context) (context.Context, error) { return ctx, nil }
	for name, tc := range map[string]struct {
		in   string
		open func(context.Context) (context.Context, error)
		want string
	}{
		"oversized request": {strings.Repeat("x", MaxRequestBytes+1), ok, "client: request is too large"},
		"unknown field":     {`{"script":"x","input":1}`, ok, "client: decode request:"},
		"no workspace": {`{"script":"x"}`, func(ctx context.Context) (context.Context, error) {
			return ctx, errors.New("no magusfile")
		}, "client: open workspace: no magusfile"},
	} {
		var stdout, stderr bytes.Buffer
		assert.Equal(t, 1, Serve(context.Background(), strings.NewReader(tc.in), &stdout, &stderr, tc.open), name)
		assert.Empty(t, stdout.String(), name)
		assert.Contains(t, stderr.String(), tc.want, name)
	}
}
