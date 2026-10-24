package notes

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const gapSymbol = "gomod example `example/guard`/Judge()."

// gapResolver answers like the graph's resolver: node ids are kind:target, and a file#decl
// resolves only when decls names it.
type gapResolver struct {
	givenAnchorsResolver
	decls map[string]string
}

func (r gapResolver) NodeID(a Anchor) string { return fmt.Sprintf("%s:%s", string(a.Kind), a.Target) }

func (r gapResolver) Declaration(_ context.Context, file, name string) (Anchor, error) {
	key, ok := r.decls[fmt.Sprintf("%s#%s", file, name)]
	if !ok {
		return Anchor{}, fmt.Errorf("%s declares no symbol named %q", file, name)
	}
	return Anchor{Kind: AnchorSymbol, Target: key}, nil
}

func liveGapResolver() gapResolver {
	return gapResolver{
		givenAnchorsResolver: givenAnchorsResolver{
			live: map[string]bool{
				"symbol:" + gapSymbol:          true,
				"diagnostic:MGS3009":           true,
				"file:internal/guard/shell.go": true,
			},
			digests: map[string]string{"file:internal/guard/shell.go": "d1"},
			sigs:    map[string]string{"symbol:" + gapSymbol: "func Judge(cmd string) Verdict"},
		},
		decls: map[string]string{"internal/guard/judge.go#Judge": gapSymbol},
	}
}

func validGap() Gap {
	return Gap{
		Title:    "wait poll denied as sleep",
		Anchors:  []string{"file:internal/guard/judge.go#Judge", "diagnostic:MGS3009", "file:internal/guard/shell.go"},
		Refs:     []string{"grd6392b52c2ea13c5a"},
		AsOf:     "5943ea176",
		Captured: capturedAt(),
	}
}

func TestGapNoteRefusesAnchorThatResolvesToNothing(t *testing.T) {
	for _, raw := range []string{"diagnostic:MGS9999", "file:internal/guard/judge.go#Gone", "symbol:" + gapSymbol + "x"} {
		g := validGap()
		g.Anchors = append(g.Anchors, raw)
		_, err := g.Note(context.Background(), "", liveGapResolver())
		require.Error(t, err, raw)
		assert.Contains(t, err.Error(), fmt.Sprintf("%q", raw), "the refusal names the anchor")
		assert.Contains(t, err.Error(), "resolves to nothing")
	}
}

func TestGapNoteRefusesFileOnlyAnchors(t *testing.T) {
	g := validGap()
	g.Anchors = []string{"file:internal/guard/shell.go"}
	_, err := g.Note(context.Background(), "", liveGapResolver())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbol, file#declaration or diagnostic")
}

func TestGapNoteDerivesFindLines(t *testing.T) {
	n, err := validGap().Note(context.Background(), "", liveGapResolver())
	require.NoError(t, err)

	assert.Regexp(t, `^gap-[0-9a-f]{12}$`, n.Name)
	// The name is a digest and the body rendered prose; both are normalized before comparing.
	assert.Equal(t, Note{
		Name:  n.Name,
		ID:    n.Name,
		Title: "wait poll denied as sleep",
		Tags:  []string{TagGap, TagGapOpen},
		Anchors: []Anchor{
			{Kind: AnchorSymbol, Target: gapSymbol},
			{Kind: AnchorDiagnostic, Target: "MGS3009"},
			{Kind: AnchorFile, Target: "internal/guard/shell.go"},
		},
		Source: &Source{Kind: SourceHarnessGap, Ref: "grd6392b52c2ea13c5a", AsOf: "5943ea176", Captured: capturedAt()},
		Find: []string{
			"magus refs 'symbol:gomod example `example/guard`/Judge().'",
			"magus query 'kind=diagnostic id=MGS3009'",
			"magus explain file:internal/guard/shell.go",
		},
		Body: n.Body,
	}, n, "file#decl is stored as the symbol it names")
}

func TestGapNoteRefusesADispositionAtCapture(t *testing.T) {
	g := validGap()
	g.Tags = []string{"gap/ruled"}
	_, err := g.Note(context.Background(), "", liveGapResolver())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gap/ruled")
}

func TestGapFindLinesSurviveTheStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	n, err := validGap().Note(context.Background(), "wait-poll", liveGapResolver())
	require.NoError(t, err)
	require.NoError(t, Save(dir, n))

	got, err := Get(dir, "wait-poll")
	require.NoError(t, err)
	assert.Equal(t, n.Find, got.Find)
}

func TestFindBlockShowsStaleAfterTheCodeMoved(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "notes")
	res := liveGapResolver()
	n, err := validGap().Note(ctx, "wait-poll", res)
	require.NoError(t, err)
	require.NoError(t, Save(dir, n))
	_, err = RecordDigests(ctx, dir, "wait-poll", "5943ea176", res)
	require.NoError(t, err)
	saved, err := Get(dir, "wait-poll")
	require.NoError(t, err)

	assert.Equal(t, []string{
		"find it:",
		"  magus refs 'symbol:gomod example `example/guard`/Judge().'",
		"  magus query 'kind=diagnostic id=MGS3009'",
		"  magus explain file:internal/guard/shell.go",
	}, FindBlock(ctx, res, saved), "fresh anchors add no stale line")

	moved := liveGapResolver()
	delete(moved.live, "symbol:"+gapSymbol)
	moved.digests["file:internal/guard/shell.go"] = "d2"
	block := FindBlock(ctx, moved, saved)
	require.Len(t, block, 6)
	assert.Contains(t, block[4], "stale (dangling-anchor)")
	assert.Contains(t, block[4], gapSymbol)
	assert.Contains(t, block[5], "stale (drifted-anchor)")

	unchecked := FindBlock(ctx, nil, saved)
	assert.Contains(t, unchecked[len(unchecked)-1], "not checked")
}

func TestFindBlockIsEmptyWithoutFindLines(t *testing.T) {
	assert.Nil(t, FindBlock(context.Background(), nil, Note{Name: "plain"}))
}
