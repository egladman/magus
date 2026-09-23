package main

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/std"
)

// A namespace's Objects are declared like a signature's objects are: with everything
// they reference, ahead of their first use.
func TestCollectMirrorsVisitsNamespaceObjects(t *testing.T) {
	got, err := collectMirrors(std.Module{
		Name:       "probe",
		Namespaces: []std.Namespace{{Name: "guard", Objects: []string{"SpawnRequest"}}},
	})
	require.NoError(t, err)
	require.Contains(t, got, "SpawnRequest")
	require.Contains(t, got, "Job", "SpawnRequest.lease brings Job with it")
	assert.Less(t, slices.Index(got, "Job"), slices.Index(got, "SpawnRequest"), "declared before use")

	_, err = collectMirrors(std.Module{
		Name:       "probe",
		Namespaces: []std.Namespace{{Name: "guard", Objects: []string{"NoSuchObject"}}},
	})
	assert.ErrorContains(t, err, `object "NoSuchObject" is not declared in boundaryTypes`)
}
