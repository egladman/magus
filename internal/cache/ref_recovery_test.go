package cache

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A 12-hex ref is 48 bits, and the design ACCEPTS a birthday collision at roughly
// 1e-3 per million distinct steps rather than paying for a longer id. That trade is
// only defensible if colliding refs stay RECOVERABLE, which is the one path a user
// hits precisely when they are already confused. These tests prove the recovery
// works rather than trusting the comment that says it does.

// TestCollidingRefsListDistinguishableCandidates: two cache keys sharing their first
// refHexLen hex render the SAME portable ref. Asking for it must not dead-end - the
// ambiguity error has to name candidates that are distinct AND that actually resolve,
// or the user has no way back (they cannot see the full keys to lengthen the prefix
// themselves).
func TestCollidingRefsListDistinguishableCandidates(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	// Identical for the first 12 hex, different immediately after.
	const keyA = "abcdef012345" + "aaaa000000000000000000000000000000000000000000000000"
	const keyB = "abcdef012345" + "bbbb000000000000000000000000000000000000000000000000"
	require.Equal(t, PortableRef(keyA), PortableRef(keyB), "the fixture must actually collide")

	mustPersist(t, s, keyA, []byte("output from A\n"), OutputDescriptor{Project: "pkg/a", Target: "build"})
	mustPersist(t, s, keyB, []byte("output from B\n"), OutputDescriptor{Project: "pkg/b", Target: "build"})

	_, _, err := s.ByRef(PortableRef(keyA))
	var amb *AmbiguousRefError
	require.True(t, errors.As(err, &amb), "a collided ref must report ambiguity, got %v", err)
	require.Len(t, amb.Candidates, 2)
	require.NotEqual(t, amb.Candidates[0], amb.Candidates[1],
		"candidates must differ; two identical strings leave the user no way to disambiguate")

	// Every listed candidate must resolve on its own - the error is only useful if
	// what it prints can be pasted straight back in.
	got := map[string]string{}
	for _, cand := range amb.Candidates {
		data, _, cerr := s.ByRef(cand)
		require.NoError(t, cerr, "candidate %q from the error message must resolve", cand)
		got[cand] = string(data)
	}
	assert.ElementsMatch(t, []string{"output from A\n", "output from B\n"},
		[]string{got[amb.Candidates[0]], got[amb.Candidates[1]]},
		"the two candidates must reach the two different outputs")

	// The rendered message names the ref and both candidates, since that string is
	// the entire recovery affordance.
	msg := amb.Error()
	assert.Contains(t, msg, "ambiguous")
	assert.Contains(t, msg, amb.Candidates[0])
	assert.Contains(t, msg, amb.Candidates[1])
}

// TestDescriptorByRefSkipsTheBlob: the identity views want metadata only. It must
// agree with ByRef's descriptor, and - unlike ByRef, which still has bytes to hand
// back - it must ERROR when the descriptor is unreadable rather than quietly
// returning a zero-valued record that reads as a real run.
func TestDescriptorByRefSkipsTheBlob(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	const key = "1122334455667788990011223344556677889900112233445566"

	ref := mustPersist(t, s, key, []byte("a very large captured log\n"),
		OutputDescriptor{Project: "svc/api", Target: "test", TimestampMs: 42, DurationMs: 7})

	desc, err := s.DescriptorByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "svc/api", desc.Project)
	assert.Equal(t, "test", desc.Target)
	assert.Equal(t, int64(42), desc.TimestampMs)

	_, viaBytes, err := s.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, viaBytes, desc, "both readers must report the same identity")

	// Remove the sidecar: the blob still resolves, so ByRef degrades to a zero
	// descriptor while DescriptorByRef must say it cannot answer.
	require.NoError(t, os.Remove(filepath.Join(dir, "outputs", key, desc.Attempt+descExt)))
	_, zero, err := s.ByRef(ref)
	require.NoError(t, err, "bytes are still retrievable")
	assert.Empty(t, zero.Project, "ByRef degrades to a zero descriptor")
	_, err = s.DescriptorByRef(ref)
	assert.Error(t, err, "a metadata reader must not invent an empty run")

	_, err = s.DescriptorByRef("outffffffffffff")
	assert.ErrorIs(t, err, fs.ErrNotExist, "an unknown ref is not-exist, not a zero record")
}

// TestRefNotFoundNamesTheStoresConsulted: "not found" is only actionable if the
// reader learns WHERE magus looked - a never-published foreign ref and a typo are
// otherwise the same message. It must also keep matching fs.ErrNotExist so the CLI's
// existing MGS8001 path still fires.
func TestRefNotFoundNamesTheStoresConsulted(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	_, _, err := c.OutputByRef(context.Background(), "outdeadbeefcafe")
	require.Error(t, err)
	assert.ErrorIs(t, err, fs.ErrNotExist, "the CLI's not-found diagnostic keys on this")

	var missing *RefNotFoundError
	require.True(t, errors.As(err, &missing), "a miss must name the stores, got %v", err)
	assert.Equal(t, []string{"local cache"}, missing.Stores,
		"with no remote configured, claiming a remote was consulted would be a lie")
	msg := missing.Error()
	assert.Contains(t, msg, "outdeadbeefcafe")
	assert.Contains(t, msg, "local cache")
	assert.True(t, strings.Contains(msg, "consulted"), "the message must say where magus looked: %q", msg)
}
