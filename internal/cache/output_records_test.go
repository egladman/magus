package cache

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func outputRecordLine(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	key := strings.Repeat("ab", 32)
	rec := map[string]any{
		"api_version": types.StoredOutputAPIVersion,
		"ref":         PortableRef(key), "key": key, "attempt": "out1234abcd",
		"project": "libs/x", "target": "lint", "failed": true, "timestamp_ms": 1, "duration_ms": 2,
		"output": "FAIL: x\n",
	}
	if mutate != nil {
		mutate(rec)
	}
	b, err := json.Marshal(rec)
	require.NoError(t, err)
	return string(b) + "\n"
}

func readRecords(in string) ([]types.StoredOutput, error) {
	var got []types.StoredOutput
	err := readOutputRecords(strings.NewReader(in), defaultImportLimits, func(r types.StoredOutput) error {
		got = append(got, r)
		return nil
	})
	return got, err
}

func TestOutputImportReadsRecordsInOrder(t *testing.T) {
	got, err := readRecords(fmt.Sprintf("%s\n%s", outputRecordLine(t, nil), outputRecordLine(t, func(r map[string]any) { r["project"] = "libs/y" })))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "libs/x", got[0].Project)
	assert.Equal(t, "libs/y", got[1].Project)
	assert.Equal(t, []byte("FAIL: x\n"), got[0].Bytes())
}

func TestOutputImportRefusesNewerSchema(t *testing.T) {
	_, err := readRecords(outputRecordLine(t, func(r map[string]any) { r["api_version"] = "magus.output-record/v1alpha2" }))
	require.ErrorIs(t, err, types.StoreRecordsNewer)
	assert.ErrorContains(t, err, "line 1: ")
	assert.ErrorContains(t, err, "output record: record is magus.output-record/v1alpha2, newer than the magus.output-record/v1alpha1 this reader reads")

	_, err = readRecords(outputRecordLine(t, func(r map[string]any) { delete(r, "api_version") }))
	assert.ErrorContains(t, err, "line 1: not an output record: it has no api_version")

	_, err = readRecords(outputRecordLine(t, func(r map[string]any) { r["requires"] = []string{"future-feature"} }))
	require.ErrorIs(t, err, types.StoreRecordsNewer)
	assert.ErrorContains(t, err, "output record requires [future-feature]")
}

// Nothing reaches each when any line is bad, so a caller never prints half an input.
func TestOutputImportMalformedLineNamesTheLine(t *testing.T) {
	calls := 0
	err := readOutputRecords(strings.NewReader(outputRecordLine(t, nil)+"{not json\n"), defaultImportLimits, func(types.StoredOutput) error {
		calls++
		return nil
	})
	assert.ErrorContains(t, err, "line 2: not an output record")
	assert.Zero(t, calls)

	_, err = readRecords(outputRecordLine(t, nil) + outputRecordLine(t, func(r map[string]any) { r["target"] = "x\x1b]0;t\x07" }))
	assert.ErrorContains(t, err, "line 2: target: contains control character U+001B")

	_, err = readRecords(outputRecordLine(t, func(r map[string]any) { r["output_base64"] = []byte("x") }))
	assert.ErrorContains(t, err, "line 1: output record carries both output and output_base64")
}

func TestOutputImportRefusesAJSONBomb(t *testing.T) {
	_, err := readRecords(fmt.Sprintf("%s%s\n", strings.Repeat("[", 100_000), strings.Repeat("]", 100_000)))
	assert.ErrorContains(t, err, "line 1: not an output record")

	err = readOutputRecords(strings.NewReader(outputRecordLine(t, nil)+outputRecordLine(t, nil)),
		importLimits{LineBytes: 1 << 20, TotalBytes: 1 << 20, Records: 1}, func(types.StoredOutput) error { return nil })
	assert.EqualError(t, err, "line 2: more than 1 records")
}

func TestOutputImportCarriesInvalidUTF8Exactly(t *testing.T) {
	raw := []byte("ok \xff\xfe end")
	line := outputRecordLine(t, func(r map[string]any) { delete(r, "output"); r["output_base64"] = raw })
	got, err := readRecords(line)
	require.NoError(t, err)
	assert.Equal(t, raw, got[0].Bytes())
}

// An imported record's failed_need_target and failed_need_ref survive the read when real; a
// forged pair is made harmless (the target neutralized, the ref dropped unless minted)
// and the record is still read; only an oversized target is refused, by the descriptor check.
func TestOutputImportReadsTheFailedNeed(t *testing.T) {
	got, err := readRecords(outputRecordLine(t, func(r map[string]any) {
		r["failed_need_target"] = "lint-go"
		r["failed_need_ref"] = "out0123456789ab"
	}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, [2]string{"lint-go", "out0123456789ab"}, [2]string{got[0].FailedNeedTarget, got[0].FailedNeedRef})

	got, err = readRecords(outputRecordLine(t, func(r map[string]any) {
		r["failed_need_target"] = "lint\x1b]0;t\x07"
		r["failed_need_ref"] = "x; rm -rf ~"
	}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, [2]string{"lint<U+001B>]0;t<U+0007>", ""}, [2]string{got[0].FailedNeedTarget, got[0].FailedNeedRef})

	got, err = readRecords(outputRecordLine(t, func(r map[string]any) { r["failed_need_target"] = "lint\n" }))
	require.NoError(t, err, "a line break in the member is escaped, not refused")
	require.Len(t, got, 1)
	assert.Equal(t, "lint<U+000A>", got[0].FailedNeedTarget)

	_, err = readRecords(outputRecordLine(t, func(r map[string]any) { r["failed_need_target"] = strings.Repeat("n", 513) }))
	assert.ErrorContains(t, err, "line 1: failed_need_target: longer than")
}

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
