package cache

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

func outputRecordLine(t *testing.T, mutate func(map[string]any)) string {
	t.Helper()
	key := strings.Repeat("ab", 32)
	rec := map[string]any{
		"schema_version": types.StoredOutputSchemaVersion,
		"ref":            PortableRef(key), "key": key, "attempt": "out1234abcd",
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
	err := ReadOutputRecords(strings.NewReader(in), func(r types.StoredOutput) error {
		got = append(got, r)
		return nil
	})
	return got, err
}

func TestOutputImportReadsRecordsInOrder(t *testing.T) {
	got, err := readRecords(outputRecordLine(t, nil) + "\n" + outputRecordLine(t, func(r map[string]any) { r["project"] = "libs/y" }))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "libs/x", got[0].Project)
	assert.Equal(t, "libs/y", got[1].Project)
	assert.Equal(t, []byte("FAIL: x\n"), got[0].Bytes())
}

func TestOutputImportRefusesNewerSchema(t *testing.T) {
	_, err := readRecords(outputRecordLine(t, func(r map[string]any) { r["schema_version"] = types.StoredOutputSchemaVersion + 1 }))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 1: output record schema_version 2 is newer than this magus reads (1)")

	_, err = readRecords(outputRecordLine(t, func(r map[string]any) { delete(r, "schema_version") }))
	assert.ErrorContains(t, err, "line 1: not an output record: it has no schema_version")

	_, err = readRecords(outputRecordLine(t, func(r map[string]any) { r["requires"] = []string{"future-feature"} }))
	assert.ErrorContains(t, err, "line 1: output record requires [future-feature]")
}

// Nothing reaches each when any line is bad, so a caller never prints half an input.
func TestOutputImportMalformedLineNamesTheLine(t *testing.T) {
	calls := 0
	err := ReadOutputRecords(strings.NewReader(outputRecordLine(t, nil)+"{not json\n"), func(types.StoredOutput) error {
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
	_, err := readRecords(strings.Repeat("[", 100_000) + strings.Repeat("]", 100_000) + "\n")
	assert.ErrorContains(t, err, "line 1: not an output record")

	err = readOutputRecords(strings.NewReader(outputRecordLine(t, nil)+outputRecordLine(t, nil)),
		ImportLimits{LineBytes: 1 << 20, TotalBytes: 1 << 20, Records: 1}, func(types.StoredOutput) error { return nil })
	assert.EqualError(t, err, "line 2: more than 1 records")
}

func TestOutputImportCarriesInvalidUTF8Exactly(t *testing.T) {
	raw := []byte("ok \xff\xfe end")
	line := outputRecordLine(t, func(r map[string]any) { delete(r, "output"); r["output_base64"] = raw })
	got, err := readRecords(line)
	require.NoError(t, err)
	assert.Equal(t, raw, got[0].Bytes())
}
