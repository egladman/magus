package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/embed/rankeval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func oovTarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader}))
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(body)), Mode: 0o644}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestOOVScanCountsTokensAbsentFromTheVocabulary(t *testing.T) {
	tarball := oovTarball(t, map[string]string{
		"r/a.py":     "get value",
		"r/b.PY":     "get zzz",
		"r/README":   "ignored zzz zzz",
		"r/sub/c.rs": "ignored zzz",
	})
	files, err := scanOOV(bytes.NewReader(tarball), []string{".py"}, map[string]bool{"get": true, "value": true})
	require.NoError(t, err)
	assert.ElementsMatch(t, []oovFile{{tokens: 2, missing: 0}, {tokens: 2, missing: 1}}, files)
}

func TestOOVScanCountsAnIdentifierAsWholeAndParts(t *testing.T) {
	files, err := scanOOV(bytes.NewReader(oovTarball(t, map[string]string{"a.py": "getValue"})), []string{".py"},
		map[string]bool{"get": true, "value": true})
	require.NoError(t, err)
	assert.Equal(t, []oovFile{{tokens: 3, missing: 1}}, files, "getvalue has no row; get and value do")
}

func TestOOVRecordAppendsThroughTheEvalWriter(t *testing.T) {
	rec, err := oovRecord(oovRepos[0], inputRecord{Revision: "abc"}, "digest", []oovFile{{tokens: 10, missing: 1}, {tokens: 10, missing: 3}})
	require.NoError(t, err)
	assert.Equal(t, "oov-python-requests", rec.Set)
	assert.Equal(t, "abc", rec.Export)
	assert.Equal(t, 20, rec.Queries)

	path := filepath.Join(t.TempDir(), "evals.jsonl")
	require.NoError(t, rankeval.AppendEvalRecord(path, rec))
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	got, err := rankeval.ReadEvalRecords(f)
	require.NoError(t, err)
	assert.Equal(t, []rankeval.EvalRecord{rec}, got)
}

func TestOOVReposArePinnedByCommit(t *testing.T) {
	inputs, err := readInputs(filepath.Join("..", "..", "inputs.yaml"))
	require.NoError(t, err)
	for _, r := range oovRepos {
		in, err := findInput(inputs, r.name)
		require.NoError(t, err)
		assert.Len(t, in.Revision, 40, r.name)
		assert.Contains(t, in.Source, in.Revision, r.name)
	}
}
