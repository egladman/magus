//go:build !wasm

package std

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/readlog"
	"github.com/egladman/magus/internal/risk"
	"github.com/egladman/magus/spells"
)

//go:generate go run ../cmd/magus-utils bindings -module comments -lang buzz -out ../internal/interp/bindings/gen/comments.go

func init() { Register(Comments) }

// Comments is the "comments" host module: the comment scanner the change classifier
// reads source with, exposed so a script judges comment prose without parsing source
// itself.
var Comments = Module{
	Name: "comments",
	Doc:  "Comment blocks of source files, read with the comment syntax their spells declare.",
	Methods: []Method{
		{
			Name: "blocks",
			Doc: "Return the prose of each comment block in paths as [{path, lines: [{line, col, text}]}], in path order. " +
				"syntax maps a file extension (\".go\") to the comment syntax a spell declares for it, as " +
				"magus\\describe.spell() reports it; a path whose extension it lacks is skipped. A block is a run of " +
				"own-line line comments on consecutive lines, one block comment, or one trailing comment; directive " +
				"comments, blank comment lines and indented code examples are left out. line and col are 1-based, " +
				"col counted in runes, and text is the source from col on, so a column into text maps back by addition.",
			Args: []Arg{
				{Name: "paths", Type: TypeStringSlice},
				{Name: "syntax", Type: TypeAnyMap},
			},
			Returns: []Ret{{Type: TypeAny}},
			Raises:  true,
			Impl:    CommentsBlocks,
		},
	},
}

// CommentsBlocks reads each path and returns the prose of its comment blocks.
func CommentsBlocks(ctx context.Context, paths []string, syntax map[string]any) (any, error) {
	byExt, err := decodeCommentSyntax(syntax)
	if err != nil {
		return nil, err
	}
	out := []any{}
	for _, p := range paths {
		syn, ok := byExt[filepath.Ext(p)]
		if !ok {
			continue
		}
		full := resolvePath(ctx, p)
		if err := checkRead(ctx, full); err != nil {
			return nil, err
		}
		readlog.File(ctx, full)
		src, err := os.ReadFile(full)
		if err != nil {
			return nil, fmt.Errorf("comments.blocks %q: %w", p, err)
		}
		for _, b := range risk.ProseBlocks(risk.CommentBlocks(p, string(src), syn)) {
			lines := make([]any, len(b.Lines))
			for i, l := range b.Lines {
				lines[i] = map[string]any{"line": l.Line, "col": l.Col, "text": l.Text}
			}
			out = append(out, map[string]any{"path": b.Path, "lines": lines})
		}
	}
	return out, nil
}

func decodeCommentSyntax(syntax map[string]any) (map[string]spells.CommentSyntax, error) {
	raw, err := json.Marshal(syntax)
	if err != nil {
		return nil, fmt.Errorf("comments.blocks: syntax: %w", err)
	}
	byExt := map[string]spells.CommentSyntax{}
	if err := json.Unmarshal(raw, &byExt); err != nil {
		return nil, fmt.Errorf("comments.blocks: syntax: %w", err)
	}
	return byExt, nil
}
