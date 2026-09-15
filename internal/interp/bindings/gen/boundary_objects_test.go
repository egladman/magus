package gen

import (
	"testing"
	"time"

	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The key names a magusfile reads. Pinned per type rather than left to
// TestEveryBoundaryTypeEncodes, which proves only that nothing arrives null: a field
// renamed on the Go side would still encode, under a key nobody annotates for.
//
// These moved here with the encoders. They were assertions about BuzzObject methods in
// package types until the two encoder generators collapsed into this one.
func TestObjectKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  map[string]any
		want map[string]any
	}{
		{
			// serve's staleness stamp reads st["mtime"] and st["size"] by name, and
			// std/examples/fs/stat.buzz once implied a "modTime" key that never existed.
			// Exact equality is what keeps both claims true.
			name: "FileInfo",
			got:  objectMap(t, ObjectFileInfo(types.FileInfo{Size: 42, Mtime: 1000.5, Mode: 0o644, IsDir: true})),
			want: map[string]any{"size": int64(42), "mtime": 1000.5, "mode": int64(0o644), "is_dir": true},
		},
		{
			name: "HTTPResponse",
			got: objectMap(t, ObjectHTTPResponse(types.HTTPResponse{
				Status: 200, Body: "ok", Headers: map[string]string{"Content-Type": "text/plain"},
			})),
			want: map[string]any{
				"status":  int64(200),
				"body":    "ok",
				"headers": map[string]any{"Content-Type": "text/plain"},
			},
		},
		{
			name: "SemverVersion",
			got: objectMap(t, ObjectSemverVersion(types.SemverVersion{
				Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1", Metadata: "build5", Original: "1.2.3-rc1+build5",
			})),
			want: map[string]any{
				"major": int64(1), "minor": int64(2), "patch": int64(3),
				"prerelease": "rc1", "metadata": "build5", "original": "1.2.3-rc1+build5",
			},
		},
		{
			name: "SemverNext",
			got:  objectMap(t, ObjectSemverNext(types.SemverNext{Major: "v1.0.0", Minor: "v0.2.0", Patch: "v0.1.1"})),
			want: map[string]any{"major": "v1.0.0", "minor": "v0.2.0", "patch": "v0.1.1"},
		},
		{
			name: "URL",
			got: objectMap(t, ObjectURL(types.URL{
				Scheme: "https", Host: "example.com", Port: "8443", Path: "/a", Query: "x=1", Fragment: "top",
			})),
			want: map[string]any{
				"scheme": "https", "host": "example.com", "port": "8443",
				"path": "/a", "query": "x=1", "fragment": "top",
			},
		},
		{
			// The nested SemverVersion record and the RFC3339 date formatting in one case.
			name: "Tag",
			got: objectMap(t, ObjectVCSTag(types.VCSTag{
				Name:    "libs/gopherbuzz/v0.1.0",
				Prefix:  "libs/gopherbuzz/",
				Version: types.SemverVersion{Major: 0, Minor: 1, Patch: 0, Original: "0.1.0"},
				Date:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				ID:      "deadbeef",
			})),
			want: map[string]any{
				"name":   "libs/gopherbuzz/v0.1.0",
				"prefix": "libs/gopherbuzz/",
				"version": map[string]any{
					"major": int64(0), "minor": int64(1), "patch": int64(0),
					"prerelease": "", "metadata": "", "original": "0.1.0",
				},
				"date": "2026-01-02T03:04:05Z",
				"id":   "deadbeef",
			},
		},
		{
			name: "ExecResult",
			got:  objectMap(t, ObjectExecResult(types.ExecResult{Stdout: "out", Stderr: "err", Code: 2, OK: false})),
			want: map[string]any{"stdout": "out", "stderr": "err", "code": int64(2), "ok": false},
		},
		{
			name: "ModuleMethodEntry",
			got: objectMap(t, ObjectModuleMethodEntry(types.ModuleMethodEntry{
				Name: "glob", Doc: "list files", Buzz: "fs.glob(pat)", BuzzStdlib: "glob(pat)",
			})),
			want: map[string]any{"name": "glob", "doc": "list files", "buzz": "fs.glob(pat)", "buzzStdlib": "glob(pat)"},
		},
		{
			name: "ModuleFieldEntry",
			got:  objectMap(t, ObjectModuleFieldEntry(types.ModuleFieldEntry{Name: "name", Type: "string", Doc: "repo name"})),
			want: map[string]any{"name": "name", "type": "string", "doc": "repo name"},
		},
		{
			name: "ModuleEntry",
			got: objectMap(t, ObjectModuleEntry(types.ModuleEntry{
				Name:    "vcs",
				Doc:     "version control",
				Fields:  []types.ModuleFieldEntry{{Name: "name", Type: "string"}},
				Methods: []types.ModuleMethodEntry{{Name: "commit", Buzz: "vcs.commit()"}},
			})),
			want: map[string]any{
				"name":    "vcs",
				"doc":     "version control",
				"fields":  []any{map[string]any{"name": "name", "type": "string", "doc": ""}},
				"methods": []any{map[string]any{"name": "commit", "doc": "", "buzz": "vcs.commit()", "buzzStdlib": ""}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// A summary ModuleEntry carries its list fields EMPTY, never absent: a magusfile
// iterating entry.fields on a summary must get a list to iterate, not null.
func TestObjectModuleEntryEmptyLists(t *testing.T) {
	assert.Equal(t, map[string]any{
		"name":    "fs",
		"doc":     "",
		"fields":  []any{},
		"methods": []any{},
	}, objectMap(t, ObjectModuleEntry(types.ModuleEntry{Name: "fs"})))
}

// A field typed as a DEFINED type over string must cross as a PLAIN string. A Go type
// switch matches on identity, so a named string matches neither `case string` nor
// `case []string`; before the generator converted these, `check.status == "fail"` in a
// magusfile compared against null. Asserted on the encoder because that conversion is
// the generator's contract.
func TestObjectNamedStringsCrossAsPlainStrings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		got   map[string]any
		field string
		want  string
	}{
		{"DoctorCheck.status", objectMap(t, ObjectDoctorCheck(types.DoctorCheck{Status: types.DoctorFail})), "status", "fail"},
		{"TargetRun.state", objectMap(t, ObjectStatusTargetRun(types.StatusTargetRun{State: types.TargetRunPassed})), "state", "passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.got[tc.field]
			require.True(t, ok, "field must be present")
			assert.IsType(t, "", got, "must be a plain string, not the named type, or it crosses as null")
			assert.Equal(t, tc.want, got)
		})
	}
}

func objectMap(t *testing.T, v vm.Value) map[string]any {
	t.Helper()
	m, ok := ValueToAny(v).(map[string]any)
	require.True(t, ok, "encoder produced %T, not a map", ValueToAny(v))
	return m
}
