package buzzindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/libs/scipbuzz"
)

// formatVersion changes whenever a shard this indexer writes would come out different
// for the same source, so a pack written before is rebuilt whole.
const formatVersion = "buzzindex 1"

// Indexer indexes the Buzz files under Dir in this process, one unit per file: a Buzz
// module is a file, and an import names one. Symbols are named against WorkspaceRoot
// (scipbuzz.Options), so two projects that import one file agree on its symbols. Index
// takes units from the same Indexer's last Units call. Not safe for concurrent use.
type Indexer struct {
	Dir           string
	WorkspaceRoot string

	project *scipbuzz.Project
	files   map[string]scipbuzz.File
}

// Version is formatVersion and the scipbuzz version that binds names.
func (ix *Indexer) Version() string {
	return fmt.Sprintf("%s scipbuzz %s", formatVersion, scipbuzz.Version)
}

// Units parses every .buzz file under Dir and lists each as a unit whose deps are the
// project's files it imports. A unit's digest covers its own bytes and what each file it
// imports exports, the project's and other projects' alike: an import binds those names,
// so a file that gains or loses an export re-indexes its importers, and an edit inside a
// body does not.
func (ix *Indexer) Units(ctx context.Context) ([]symbols.Unit, error) {
	p, err := scipbuzz.NewProject(scipbuzz.Options{ProjectRoot: ix.Dir, WorkspaceRoot: ix.WorkspaceRoot})
	if err != nil {
		return nil, err
	}
	files, err := p.Files(ctx)
	if err != nil {
		return nil, err
	}
	ix.project, ix.files = p, make(map[string]scipbuzz.File, len(files))
	units := make([]symbols.Unit, 0, len(files))
	for _, f := range files {
		ix.files[f.Path] = f
		u, err := ix.unit(f)
		if err != nil {
			return nil, err
		}
		units = append(units, u)
	}
	return units, nil
}

func (ix *Indexer) unit(f scipbuzz.File) (symbols.Unit, error) {
	data, err := os.ReadFile(filepath.Join(ix.Dir, filepath.FromSlash(f.Path)))
	if err != nil && f.Err == nil {
		return symbols.Unit{}, err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%x\n", f.Path, sha256.Sum256(data))
	var deps []string
	for _, imp := range f.Imports {
		fmt.Fprintf(h, "import %s\x00%s\n", imp.Path, strings.Join(ix.project.Exports(imp.Path), "\x00"))
		if imp.Document != "" && imp.Document != f.Path {
			deps = append(deps, imp.Document)
		}
	}
	return symbols.Unit{ID: f.Path, Digest: hex.EncodeToString(h.Sum(nil)), Deps: deps}, nil
}

// Index returns each unit's shard: the file's document, or, for a file that did not
// parse, a shard with no document and a gap naming the file. A construct the binder could
// not place is left out of the document and named as a gap too.
func (ix *Indexer) Index(ctx context.Context, units []symbols.Unit) ([]symbols.Shard, error) {
	shards := make([]symbols.Shard, 0, len(units))
	for _, u := range units {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		f, ok := ix.files[u.ID]
		if !ok {
			return nil, fmt.Errorf("unit %q was not listed by Units", u.ID)
		}
		s := symbols.Shard{Unit: u.ID}
		if f.Err != nil {
			// scipbuzz's message opens with the file's workspace-relative path.
			s.Gaps = []string{f.Err.Error()}
			shards = append(shards, s)
			continue
		}
		doc, problems, err := ix.project.Document(f.Path)
		if err != nil {
			return nil, err
		}
		s.Documents = []symbols.Document{symbols.DocumentFromSCIP(doc)}
		s.Gaps = problems
		shards = append(shards, s)
	}
	return shards, nil
}
