package agent

import (
	"bytes"
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
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
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

// modelRowsFile holds the harness's model rows, one JSON object a line; tierFile holds the
// workspace's model-tier declaration as one JSON line, and is empty for none. They leave
// ToolRowsVersion alone: a reader that predates them ignores them, and ToolRows.Models
// reports a directory written before them, so no build misreads the other's directory.
const (
	modelRowsFile = "models"
	tierFile      = "tiers"
)

// HarnessTools is the table of a host's own tools a harness spell declares, by its
// harness_tools op: Dir is the workspace-relative directory the rows are installed in, and
// the directory the spell's hook wiring names to the guard with --tool-rows.
type HarnessTools struct {
	Dir  string        `json:"dir"`
	Rows []HarnessTool `json:"rows"`
	// Models is what install writes beside the rows so a hook prices and tiers a call
	// without loading the workspace; nil writes neither file. No spell declares it.
	Models *ModelFacts `json:"-"`
}

// ModelFacts is what the guard prices and tiers a call by: the model rows the harness
// publishes through harness_models, and the workspace's model-tier declaration, nil when it
// declares none or turned tiers off.
type ModelFacts struct {
	Rows  []types.ModelPricing
	Tiers *types.ModelTierSet
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
	if t.Models != nil {
		files = append(files,
			HarnessAgentFile{Path: path.Join(t.Dir, modelRowsFile), Content: encodeModelRows(t.Models.Rows)},
			HarnessAgentFile{Path: path.Join(t.Dir, tierFile), Content: encodeTiers(t.Models.Tiers)})
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

// InstallHarnessTools writes the tool rows directory harness id declares under root, with
// the harness's model rows and the model tiers of the workspace ctx carries, and returns the
// paths it changed. A harness that declares no tools writes nothing. A ctx carrying no
// workspace is an error: its tiers are unknown, and an empty tiers file would say none.
func InstallHarnessTools(ctx context.Context, root, id string, dryRun bool) ([]string, error) {
	d, _, err := LoadHarness(ctx, root, id)
	if err != nil {
		return nil, err
	}
	if d.Tools == nil {
		return nil, nil
	}
	ws, ok := types.WorkspaceFromContext(ctx).(interface {
		ModelTiers(ctx context.Context) *types.ModelTierSet
	})
	if !ok {
		return nil, fmt.Errorf("agent: harness %s: no workspace is loaded, so its model tiers are unknown", id)
	}
	tools := *d.Tools
	tools.Models = &ModelFacts{Rows: d.Models, Tiers: ws.ModelTiers(ctx)}
	var changed []string
	for _, f := range tools.Files() {
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
	abs          string
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
		rows.Err = errors.New("the hook wiring names no --tool-rows directory, so it predates the tool rows, so reinstall it with `magus agent harness install`")
		return rows
	}
	abs := dir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, dir)
	}
	reinstall := "write it with `magus agent harness install`"
	version, err := os.ReadFile(filepath.Join(abs, toolRowsVersionFile))
	if err != nil {
		rows.Err = fmt.Errorf("no tool rows at %s, %s: %w", dir, reinstall, err)
		return rows
	}
	if got := strings.TrimSpace(string(version)); got != strconv.Itoa(ToolRowsVersion) {
		rows.Err = fmt.Errorf("the tool rows at %s are version %s and this magus reads version %d, %s", dir, got, ToolRowsVersion, reinstall)
		return rows
	}
	rows.abs = abs
	rows.capabilities = map[string]ToolCapability{}
	for _, c := range ToolCapabilities {
		body, err := os.ReadFile(filepath.Join(abs, string(c)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			rows.capabilities, rows.Err = nil, fmt.Errorf("no tool rows at %s, %s: %w", dir, reinstall, err)
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

// Models reads the model facts installed beside the rows. It reads on each call rather than
// in ReadToolRows, so a call the inert fast path answers never pays for them. The error says
// why there are none: the rows did not read, or a file is missing or malformed, each naming
// the install that rewrites it.
func (r ToolRows) Models() (ModelFacts, error) {
	if r.Err != nil {
		return ModelFacts{}, r.Err
	}
	if r.capabilities == nil {
		return ModelFacts{}, errors.New("no tool rows were read")
	}
	reinstall := "rewrite it with `magus agent harness install`"
	body, err := os.ReadFile(filepath.Join(r.abs, modelRowsFile))
	if err != nil {
		return ModelFacts{}, fmt.Errorf("no model rows at %s, %s: %w", r.Dir, reinstall, err)
	}
	rows, err := decodeModelRows(body)
	if err != nil {
		return ModelFacts{}, fmt.Errorf("the model rows at %s do not read, %s: %w", r.Dir, reinstall, err)
	}
	body, err = os.ReadFile(filepath.Join(r.abs, tierFile))
	if err != nil {
		return ModelFacts{}, fmt.Errorf("no model tiers at %s, %s: %w", r.Dir, reinstall, err)
	}
	tiers, err := decodeTiers(body)
	if err != nil {
		return ModelFacts{}, fmt.Errorf("the model tiers at %s do not read, %s: %w", r.Dir, reinstall, err)
	}
	return ModelFacts{Rows: rows, Tiers: tiers}, nil
}

type toolRowsKey struct{}

// ContextWithToolRows attaches the tool rows a hook's wiring named, so what the hook loads
// inside its judgment reads the model facts installed beside them.
func ContextWithToolRows(ctx context.Context, rows ToolRows) context.Context {
	return context.WithValue(ctx, toolRowsKey{}, rows)
}

// ToolRowsFromContext is the tool rows ContextWithToolRows attached, false when none were:
// the caller is no hook, so no wiring named a directory.
func ToolRowsFromContext(ctx context.Context) (ToolRows, bool) {
	rows, ok := ctx.Value(toolRowsKey{}).(ToolRows)
	return rows, ok
}

// modelRowLine is one model row as the models file keeps it, in harness_models' own keys;
// types.ModelPricing carries no wire tags.
type modelRowLine struct {
	ID           string  `json:"id"`
	Tier         string  `json:"tier,omitzero"`
	Window       int64   `json:"window,omitzero"`
	Input        float64 `json:"input,omitzero"`
	CacheWrite5m float64 `json:"cache_write_5m,omitzero"`
	CacheWrite1h float64 `json:"cache_write_1h,omitzero"`
	CacheRead    float64 `json:"cache_read,omitzero"`
	Output       float64 `json:"output,omitzero"`
	PriceStep    int64   `json:"price_step,omitzero"`
	StepFactor   float64 `json:"step_factor,omitzero"`
}

func encodeModelRows(rows []types.ModelPricing) string {
	var b strings.Builder
	for _, r := range rows {
		line := modelRowLine{
			ID: r.ID, Tier: r.Tier, Window: r.Window, Input: float64(r.Input), CacheWrite5m: float64(r.CacheWrite5m),
			CacheWrite1h: float64(r.CacheWrite1h), CacheRead: float64(r.CacheRead), Output: float64(r.Output),
		}
		if r.LongContext != nil {
			line.PriceStep, line.StepFactor = r.LongContext.Above, r.LongContext.Multiplier
		}
		writeJSONLine(&b, line)
	}
	return b.String()
}

func decodeModelRows(body []byte) ([]types.ModelPricing, error) {
	var rows []types.ModelPricing
	for i, raw := range jsonLines(body) {
		var line modelRowLine
		if err := json.Unmarshal(raw, &line); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		row := types.ModelPricing{
			ID: line.ID, Tier: line.Tier, Window: line.Window, Input: types.USDPerMTok(line.Input), CacheWrite5m: types.USDPerMTok(line.CacheWrite5m),
			CacheWrite1h: types.USDPerMTok(line.CacheWrite1h), CacheRead: types.USDPerMTok(line.CacheRead), Output: types.USDPerMTok(line.Output),
		}
		if line.PriceStep > 0 {
			row.LongContext = &types.PriceStep{Above: line.PriceStep, Multiplier: line.StepFactor}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// tierLine is a model-tier declaration as the tiers file keeps it: each list by rank,
// cheapest first, and each host's model alias for every rank.
type tierLine struct {
	Enum    string               `json:"enum"`
	Site    string               `json:"site"`
	Cases   []string             `json:"cases"`
	Default int                  `json:"default"`
	Bounds  []tierBoundsLine     `json:"bounds"`
	Skills  []types.SkillVariant `json:"skills"`
	Hosts   map[string][]string  `json:"hosts,omitempty"`
}

type tierBoundsLine struct {
	MaxLive    int   `json:"max_live,omitzero"`
	MaxContext int64 `json:"max_context,omitzero"`
}

func encodeTiers(set *types.ModelTierSet) string {
	if set == nil {
		return ""
	}
	tiers := set.All()
	line := tierLine{Enum: tierEnum(set), Site: set.Site(), Default: set.Default().Rank()}
	for _, t := range tiers {
		b := set.Bounds(t)
		line.Cases = append(line.Cases, t.Name())
		line.Bounds = append(line.Bounds, tierBoundsLine{MaxLive: b.MaxLive, MaxContext: b.MaxContext})
		line.Skills = append(line.Skills, set.Skills(t))
	}
	for _, host := range set.Hosts() {
		if line.Hosts == nil {
			line.Hosts = map[string][]string{}
		}
		for _, t := range tiers {
			// A host the set lists maps every rank, so Alias cannot fail here.
			alias, _ := set.Alias(host, t)
			line.Hosts[host.ID] = append(line.Hosts[host.ID], alias)
		}
	}
	var b strings.Builder
	writeJSONLine(&b, line)
	return b.String()
}

// tierEnum is the name of the enum that declared set. The set reports it only inside the
// error of a Parse no case answers, and the empty name is one: no case is empty.
//
// TODO: read it from a types.ModelTierSet accessor once one is exported.
func tierEnum(set *types.ModelTierSet) string {
	_, err := set.Parse("")
	if err == nil {
		return ""
	}
	tiers := set.All()
	cases := make([]string, len(tiers))
	for i, t := range tiers {
		cases[i] = t.Name()
	}
	msg, _ := strings.CutPrefix(err.Error(), `"" is not a model tier: enum `)
	return strings.TrimSuffix(msg, " at "+set.Site()+" declares "+strings.Join(cases, ", "))
}

func decodeTiers(body []byte) (*types.ModelTierSet, error) {
	lines := jsonLines(body)
	switch len(lines) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, fmt.Errorf("%d lines, and a declaration is one", len(lines))
	}
	var line tierLine
	if err := json.Unmarshal(lines[0], &line); err != nil {
		return nil, err
	}
	decl := types.ModelTierDeclaration{Enum: line.Enum, Site: line.Site, Cases: line.Cases, Default: line.Default, Skills: line.Skills}
	for _, b := range line.Bounds {
		decl.Bounds = append(decl.Bounds, types.ModelTierBounds{MaxLive: b.MaxLive, MaxContext: b.MaxContext})
	}
	if len(line.Hosts) > 0 {
		decl.Hosts = make(map[types.HarnessKey][]string, len(line.Hosts))
		for id, aliases := range line.Hosts {
			decl.Hosts[types.HarnessKey{ID: id}] = aliases
		}
	}
	return types.NewModelTierSet(decl)
}

// writeJSONLine appends v as one line. Each value written holds only strings, numbers and
// lists and maps of them, which always marshal.
func writeJSONLine(b *strings.Builder, v any) {
	raw, _ := json.Marshal(v)
	b.Write(raw)
	b.WriteByte('\n')
}

func jsonLines(body []byte) [][]byte {
	var lines [][]byte
	for line := range bytes.Lines(body) {
		if line = bytes.TrimSpace(line); len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
