package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/file"
)

// ToolCapability is what a host tool can do to a workspace, in magus's own words. The shape
// of a call's input decides how the guard judges it (a command field is a command line, a
// file path is a read or a write); the capability settles what shape cannot, and says which
// tools carry nothing to judge on purpose.
type ToolCapability string

const (
	ToolRuns   ToolCapability = "runs"
	ToolReads  ToolCapability = "reads"
	ToolWrites ToolCapability = "writes"
	ToolSpawns ToolCapability = "spawns"
	// ToolNetwork reaches the network and touches no workspace file: a browser.
	ToolNetwork ToolCapability = "network"
	// ToolInert touches no workspace file and runs nothing: a plan, a todo list, a web
	// lookup, a read of a background task's output.
	ToolInert ToolCapability = "inert"
)

// ToolCapabilities is every capability, each the name of one file in a tool rows directory.
var ToolCapabilities = []ToolCapability{ToolRuns, ToolReads, ToolWrites, ToolSpawns, ToolNetwork, ToolInert}

// ToolRowsVersion is the layout of a tool rows directory this build writes and reads, kept in
// the directory's version file. Bump it when the layout changes, so a directory written for
// another layout is reported rather than misread.
const ToolRowsVersion = 1

// toolRowsVersionFile names the file holding a directory's ToolRowsVersion.
const toolRowsVersionFile = "version"

// HarnessTools is the table of a host's own tools a harness spell declares, by its
// harness_tools op: Dir is the workspace-relative directory the rows are installed in, and
// the directory the spell's hook wiring names to the guard with --tool-rows.
type HarnessTools struct {
	Dir  string        `json:"dir"`
	Rows []HarnessTool `json:"rows"`
}

// HarnessTool is one row: a tool by the host's own name, and what it can do.
type HarnessTool struct {
	Name       string         `json:"name"`
	Capability ToolCapability `json:"capability"`
}

func validateHarnessTools(t HarnessTools) error {
	if t.Dir == "" || filepath.IsAbs(t.Dir) || !isSafeRelativePath(t.Dir) {
		return errors.New("dir must be a workspace-relative path")
	}
	seen := map[string]bool{}
	for i, r := range t.Rows {
		switch {
		case r.Name == "" || strings.ContainsAny(r.Name, "\n\r"):
			return fmt.Errorf("row %d: name must be one non-empty line", i+1)
		case !slices.Contains(ToolCapabilities, r.Capability):
			return fmt.Errorf("row %d (%s): capability %q is none of %s", i+1, r.Name, r.Capability, capabilityNames())
		case seen[r.Name]:
			return fmt.Errorf("row %d: %s is declared twice", i+1, r.Name)
		}
		seen[r.Name] = true
	}
	return nil
}

func capabilityNames() string {
	names := make([]string, len(ToolCapabilities))
	for i, c := range ToolCapabilities {
		names[i] = string(c)
	}
	return strings.Join(names, ", ")
}

// Files renders the rows as the files a tool rows directory holds: one per capability that
// has a row, each tool name on its own line in sorted order, and the version file, which
// is what tells an installed directory from none.
func (t HarnessTools) Files() []HarnessAgentFile {
	byCapability := map[ToolCapability][]string{}
	for _, r := range t.Rows {
		byCapability[r.Capability] = append(byCapability[r.Capability], r.Name)
	}
	files := make([]HarnessAgentFile, 0, len(ToolCapabilities)+1)
	for _, c := range ToolCapabilities {
		names := byCapability[c]
		if len(names) == 0 {
			continue
		}
		slices.Sort(names)
		var b strings.Builder
		for _, n := range names {
			b.WriteString(n + "\n")
		}
		files = append(files, HarnessAgentFile{Path: path.Join(t.Dir, string(c)), Content: b.String()})
	}
	return append(files, HarnessAgentFile{Path: path.Join(t.Dir, toolRowsVersionFile), Content: strconv.Itoa(ToolRowsVersion) + "\n"})
}

// toolFiles is d's tool rows directory as files, nil when d declares no tools.
func (d HarnessDescriptor) toolFiles() []HarnessAgentFile {
	if d.Tools == nil {
		return nil
	}
	return d.Tools.Files()
}

// InstallHarnessTools writes the tool rows directory harness id declares under root, and
// returns the paths it changed. A harness that declares no tools writes nothing.
func InstallHarnessTools(ctx context.Context, root, id string, dryRun bool) ([]string, error) {
	d, _, err := LoadHarness(ctx, root, id)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, f := range d.toolFiles() {
		planned, err := planWholeFile(root, f.Path, f.Content)
		if err != nil {
			return changed, err
		}
		if len(planned.Changes) == 0 {
			continue
		}
		changed = append(changed, f.Path)
		if dryRun {
			continue
		}
		abs := filepath.Join(root, f.Path)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return changed, fmt.Errorf("agent: %w", err)
		}
		if err := file.WriteFileAtomic(abs, []byte(f.Content), 0o644); err != nil {
			return changed, fmt.Errorf("agent: %w", err)
		}
	}
	return changed, nil
}

// ToolRows is a tool rows directory as read back: each tool's capability, or Err saying why
// there is no table. The zero value is a table that was never asked for.
type ToolRows struct {
	// Dir is the directory as the wiring named it, "" when it named none.
	Dir          string
	capabilities map[string]ToolCapability
	Err          error
}

// ReadToolRows reads the tool rows directory dir, relative to root unless absolute. A
// capability file that is absent has no rows. A dir of "", a missing version file, an
// unreadable file and a version other than ToolRowsVersion are each an Err naming the
// command that writes the directory.
func ReadToolRows(root, dir string) ToolRows {
	rows := ToolRows{Dir: dir}
	if dir == "" {
		rows.Err = errors.New("the hook wiring names no --tool-rows directory, so it predates the tool rows; reinstall it with `magus agent harness install`")
		return rows
	}
	abs := dir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, dir)
	}
	reinstall := "; write it with `magus agent harness install`"
	version, err := os.ReadFile(filepath.Join(abs, toolRowsVersionFile))
	if err != nil {
		rows.Err = fmt.Errorf("no tool rows at %s: %w%s", dir, err, reinstall)
		return rows
	}
	if got := strings.TrimSpace(string(version)); got != strconv.Itoa(ToolRowsVersion) {
		rows.Err = fmt.Errorf("the tool rows at %s are version %s and this magus reads version %d%s", dir, got, ToolRowsVersion, reinstall)
		return rows
	}
	rows.capabilities = map[string]ToolCapability{}
	for _, c := range ToolCapabilities {
		body, err := os.ReadFile(filepath.Join(abs, string(c)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			rows.capabilities, rows.Err = nil, fmt.Errorf("no tool rows at %s: %w%s", dir, err, reinstall)
			return rows
		}
		for _, name := range strings.Split(string(body), "\n") {
			if name = strings.TrimSpace(name); name != "" {
				rows.capabilities[name] = c
			}
		}
	}
	return rows
}

// Capability is tool's capability, "" for a tool the rows do not list or when there are none.
func (r ToolRows) Capability(tool string) ToolCapability {
	return r.capabilities[tool]
}
