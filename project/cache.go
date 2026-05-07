package project

import (
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

const cacheSchemaVersion = 1

const wsCacheFile = ".magus/workspace.cache.json"

type wsCache struct {
	SchemaVersion int                      `json:"v"`
	Projects      map[string]cachedProject `json:"projects"`
	DirMtimes     map[string]int64         `json:"dir_mtimes"`
}

type cachedProject struct {
	Path   string   `json:"path"`
	Dir    string   `json:"dir"`
	Spells []string `json:"spells,omitempty"`
}

func loadWSCache(root string) (*wsCache, bool) {
	data, err := os.ReadFile(filepath.Join(root, wsCacheFile))
	if err != nil {
		return nil, false
	}
	var c wsCache
	if err := codec.Unmarshal(data, &c); err != nil {
		return nil, false
	}
	if c.SchemaVersion != cacheSchemaVersion {
		return nil, false
	}
	return &c, true
}

// valid reports whether all recorded directory mtimes are still current.
func (c *wsCache) valid() bool {
	for path, expectedNs := range c.DirMtimes {
		info, err := os.Lstat(path)
		if err != nil || info.ModTime().UnixNano() != expectedNs {
			return false
		}
	}
	return true
}

// saveWSCache writes the workspace cache; errors are non-fatal (triggers full walk next time).
func saveWSCache(root string, ws *types.Workspace, dirMtimes map[string]int64) {
	projects := make(map[string]cachedProject, len(ws.Projects))
	for k, p := range ws.Projects {
		spells := make([]string, len(p.Spells))
		copy(spells, p.Spells)
		projects[k] = cachedProject{
			Path:   p.Path,
			Dir:    p.Dir,
			Spells: spells,
		}
	}
	c := wsCache{
		SchemaVersion: cacheSchemaVersion,
		Projects:      projects,
		DirMtimes:     dirMtimes,
	}
	data, err := codec.Marshal(c)
	if err != nil {
		return
	}
	_ = file.WriteFileAtomic(filepath.Join(root, wsCacheFile), data, 0o600)
}

// restoreFromCache reconstructs a *types.Workspace from c, re-binding spells from the registry.
func restoreFromCache(root string, c *wsCache) *types.Workspace {
	all := defaultRegistry.All()
	spellByName := make(map[string]*types.Spell, len(all))
	for _, s := range all {
		spellByName[s.Name()] = s
	}

	ws := &types.Workspace{Root: root, Projects: make(map[string]*types.Project, len(c.Projects))}
	for key, cp := range c.Projects {
		p := &types.Project{Path: cp.Path, Dir: cp.Dir}
		for _, name := range cp.Spells {
			if s, ok := spellByName[name]; ok {
				p.AttachSpell(s)
			}
		}
		ws.Projects[key] = p
	}
	return ws
}
