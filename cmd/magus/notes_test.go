package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/auth"
	store "github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
)

func TestNotesLsByTagListsOpenGapsWithTheirFindLines(t *testing.T) {
	root := t.TempDir()
	prev := globalCfg
	globalCfg.Knowledge.Notes.Shared = "notes"
	globalCfg.Knowledge.Notes.Private = ""
	t.Cleanup(func() { globalCfg = prev })
	dir := filepath.Join(root, "notes")
	require.NoError(t, store.Save(dir, store.Note{
		Name:    "gap-wait-poll",
		Title:   "wait poll denied as sleep",
		Tags:    []string{store.TagGap, store.TagGapOpen},
		Anchors: []store.Anchor{{Kind: store.AnchorDiagnostic, Target: "MGS3009"}},
		Find:    []string{"magus query 'kind=diagnostic id=MGS3009'"},
	}))
	require.NoError(t, store.Save(dir, store.Note{
		Name:    "cache-pairing",
		Title:   "two caches",
		Anchors: []store.Anchor{{Kind: store.AnchorProject, Target: "."}},
	}))

	var err error
	out := captureStdout(t, func() {
		err = notesList(context.Background(), root, []string{"--tag", store.TagGapOpen})
	})
	require.NoError(t, err)
	assert.Contains(t, out, "gap-wait-poll")
	assert.NotContains(t, out, "cache-pairing", "a note without the tag is not listed")
	assert.Contains(t, out, "  find it:\n    magus query 'kind=diagnostic id=MGS3009'\n")
}

func TestServerReviewCommentsRevokesItsToken(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var presented string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		cred, ok := auth.Verify(presented)
		if !ok || cred.Kind != types.KindStored {
			http.Error(w, "no", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	prevAddr := globalCfg.MCP.Address
	t.Cleanup(func() { globalCfg.MCP.Address = prevAddr })
	globalCfg.MCP.Address = strings.TrimPrefix(srv.URL, "http://")

	_, _, served := review.ServerReviewComments(context.Background(), reviewServerGet)

	assert.True(t, served)
	_, ok := auth.Verify(presented)
	assert.False(t, ok)
}
