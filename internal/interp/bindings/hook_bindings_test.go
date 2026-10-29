package bindings

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

const (
	harnessSpellDir = "../../../spells/harness"
	glueDir         = "../../../docs/guides/integrations/agents"
	bindingsGuide   = glueDir + "/README.md"
)

// shippedHarnessIDs reads the harnesses off the spell directory, so a new
// harness owes its bindings without anyone listing it here.
func shippedHarnessIDs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(harnessSpellDir)
	if err != nil {
		t.Fatalf("read %s: %v", harnessSpellDir, err)
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) == 0 {
		t.Fatalf("%s holds no harness spell", harnessSpellDir)
	}
	return ids
}

func readGlue(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(glueDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// HookBindingCompletenessSuite holds every shipped harness to an answer at
// every hook point. It extends TestShippedHookConfigsWireTheSameJobs from two
// configs to every harness, keyed by point. A host whose glue is one script
// or a plugin is held to it too.
type HookBindingCompletenessSuite struct {
	suite.Suite
	tables map[string][]types.HookBinding
	loaded map[string]agent.HarnessDescriptor
}

func TestHookBindingCompletenessSuite(t *testing.T) {
	suite.Run(t, new(HookBindingCompletenessSuite))
}

func (s *HookBindingCompletenessSuite) SetupSuite() {
	s.tables = map[string][]types.HookBinding{}
	s.loaded = map[string]agent.HarnessDescriptor{}
	for _, id := range shippedHarnessIDs(s.T()) {
		d := loadShippedHarness(s.T(), id)
		s.loaded[id] = d
		table, err := agent.HookBindings(d)
		s.Require().NoError(err, "%s leaves a hook point unexplained", id)
		s.tables[id] = table
	}
}

func (s *HookBindingCompletenessSuite) TestEveryHarnessAnswersEveryPoint() {
	for id, table := range s.tables {
		var points []types.HookPoint
		for _, b := range table {
			s.True(b.Status == types.Bound || b.Status == types.Unsupported, "%s %s: %q", id, b.Point, b.Status)
			points = append(points, b.Point)
		}
		s.Equal(types.HookPoints(), points, id)
	}
}

func (s *HookBindingCompletenessSuite) TestAStaleUnsupportedRowFails() {
	d := s.loaded["claude-code"]
	d.UnsupportedHooks = append(slices.Clone(d.UnsupportedHooks), types.HookBinding{
		Point: types.HookPointShell, Status: types.Unsupported, Reason: "not verified: anything"})
	_, err := agent.HookBindings(d)
	s.ErrorContains(err, "claude-code: mgs_listUnsupportedHooks says shell is unsupported, but magus-command binds it")
}

func (s *HookBindingCompletenessSuite) TestARemovedRowFails() {
	d := s.loaded["codex"]
	d.UnsupportedHooks = nil
	_, err := agent.HookBindings(d)
	s.ErrorContains(err, "codex: no hook binds attach")
}

// Attach stays unsupported until an SVG renders inline (decision 135).
func (s *HookBindingCompletenessSuite) TestAttachIsUnsupportedEverywhere() {
	for id, table := range s.tables {
		attach := table[slices.IndexFunc(table, func(b types.HookBinding) bool { return b.Point == types.HookPointAttach })]
		s.Equal(types.Unsupported, attach.Status, id)
	}
}

// The plugin is not a rendered config, so nothing else holds it to the
// hooks the opencode spell lists.
func (s *HookBindingCompletenessSuite) TestTheOpenCodePluginRunsEveryHookItsSpellLists() {
	plugin := readGlue(s.T(), "opencode-plugin.ts")
	for _, h := range s.loaded["opencode"].Hooks {
		s.Contains(plugin, `"`+h.Event+`"`, "the plugin handles no %s", h.Event)
		if h.Script != "opencode-plugin" {
			s.Contains(plugin, h.Script+".buzz", "the plugin never runs %s", h.Script)
		}
	}
}

func (s *HookBindingCompletenessSuite) TestTheCursorHookAnswersEveryEventItsSpellLists() {
	glue := readGlue(s.T(), "cursor-hook.buzz")
	for _, h := range s.loaded["cursor"].Hooks {
		if h.Script == "cursor-hook" {
			s.Contains(glue, `eventName == "`+h.Event+`"`, "cursor-hook.buzz has no %s arm", h.Event)
		}
	}
}

// guideRow reads one matrix row of the guide: the point, then a cell per
// harness in the header's order.
var guideRow = regexp.MustCompile("^\\| `([a-z-]+)` +\\|(.*)\\|$")

func (s *HookBindingCompletenessSuite) TestTheGuideShowsTheTable() {
	guide := readGuide(s.T())
	lines := strings.Split(guide, "\n")
	header := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "| hook point ") })
	s.Require().NotEqual(-1, header, "%s has no matrix; regenerate it", bindingsGuide)
	var columns []string
	for _, cell := range strings.Split(strings.Trim(lines[header], "|"), "|")[1:] {
		columns = append(columns, strings.TrimSpace(cell))
	}
	s.ElementsMatch(shippedHarnessIDs(s.T()), columns)

	seen := 0
	for _, line := range lines[header+2:] {
		m := guideRow.FindStringSubmatch(line)
		if m == nil {
			break
		}
		seen++
		cells := strings.Split(m[2], "|")
		s.Require().Len(cells, len(columns), line)
		for i, id := range columns {
			want := s.cell(id, types.HookPoint(m[1]))
			s.Equal(want, strings.TrimSpace(cells[i]), "%s %s; regenerate %s", id, m[1], bindingsGuide)
		}
	}
	s.Equal(len(types.HookPoints()), seen, "one row per hook point")
	for id, table := range s.tables {
		for _, b := range table {
			if b.Status == types.Unsupported {
				s.Contains(guide, b.Reason, "%s %s's reason", id, b.Point)
			}
		}
	}
}

func (s *HookBindingCompletenessSuite) cell(id string, point types.HookPoint) string {
	for _, b := range s.tables[id] {
		if b.Point != point {
			continue
		}
		if b.Status == types.Unsupported {
			return "unsupported"
		}
		return "`" + b.Adapter + "`"
	}
	return ""
}

func readGuide(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(bindingsGuide)
	if err != nil {
		t.Fatalf("read %s: %v", bindingsGuide, err)
	}
	return string(body)
}

// AdapterConformanceSuite ties each Bound adapter to the cases that run it.
// guard_templates.txtar and opencode-plugin.test.ts execute the verdict each
// host gets for one recorded event. This suite holds each Bound judging point
// to a declaration those gates read, and pins each host's fail stance.
type AdapterConformanceSuite struct {
	suite.Suite
	loaded map[string]agent.HarnessDescriptor
}

func TestAdapterConformanceSuite(t *testing.T) {
	suite.Run(t, new(AdapterConformanceSuite))
}

func (s *AdapterConformanceSuite) SetupSuite() {
	s.loaded = map[string]agent.HarnessDescriptor{}
	for _, id := range shippedHarnessIDs(s.T()) {
		s.loaded[id] = loadShippedHarness(s.T(), id)
	}
}

func (s *AdapterConformanceSuite) TestEveryBoundAdapterIsAShippedGlue() {
	for id, d := range s.loaded {
		table, err := agent.HookBindings(d)
		s.Require().NoError(err, id)
		for _, b := range table {
			if b.Status != types.Bound {
				continue
			}
			for _, script := range strings.Split(b.Adapter, ", ") {
				s.FileExists(filepath.Join(glueDir, glueFile(script)), "%s %s", id, b.Point)
			}
		}
	}
}

func glueFile(script string) string {
	if script == "opencode-plugin" {
		return script + ".ts"
	}
	return script + ".buzz"
}

// judgedInput is the guard input a judging point hands magus, as the
// magus-guard-coverage markers name it.
var judgedInput = map[types.HookPoint]string{
	types.HookPointShell: "command",
	types.HookPointEdit:  "path",
	types.HookPointMCP:   "mcp",
}

var coverageLine = regexp.MustCompile(`magus-guard-coverage: schema=\d+ host=(\S+) input=(\S+) deny=(\S+)`)

func (s *AdapterConformanceSuite) TestEveryBoundJudgingPointDeclaresItsVerdictTransport() {
	declared := map[string]bool{}
	entries, err := os.ReadDir(glueDir)
	s.Require().NoError(err)
	for _, e := range entries {
		if ext := filepath.Ext(e.Name()); ext != ".buzz" && ext != ".ts" {
			continue
		}
		for _, m := range coverageLine.FindAllStringSubmatch(readGlue(s.T(), e.Name()), -1) {
			for _, host := range strings.Split(m[1], ",") {
				declared[host+"/"+m[2]] = m[3] != "none"
			}
		}
	}
	for id, d := range s.loaded {
		table, err := agent.HookBindings(d)
		s.Require().NoError(err, id)
		for _, b := range table {
			input, judging := judgedInput[b.Point]
			if !judging || b.Status != types.Bound {
				continue
			}
			s.True(declared[id+"/"+input],
				"%s binds %s, but no glue declares how %s carries a deny on the %s input", id, b.Point, id, input)
		}
	}
}

// failClosed says which hosts block a gated call when the guard cannot
// answer. Only Cursor's hook config can say so (failClosed on an entry).
var failClosed = map[string]bool{"cursor": true}

func (s *AdapterConformanceSuite) TestEachHostsFailStanceIsTheOneDecided() {
	for id, d := range s.loaded {
		for _, group := range d.ManagedEntries {
			for _, entry := range group.Entries {
				if closed, _ := entry["failClosed"].(bool); closed {
					s.True(failClosed[id], "%s %v fails closed, which failClosed does not record", id, group.Path)
				}
			}
		}
	}
	for _, event := range []string{"preToolUse", "beforeShellExecution", "beforeMCPExecution"} {
		s.True(entryFailsClosed(s.loaded["cursor"], event), "cursor's %s entry must fail closed", event)
	}
	s.Contains(readGlue(s.T(), "opencode-plugin.ts"), "this call is UNGUARDED",
		"the plugin fails open, and says so")
}

func entryFailsClosed(d agent.HarnessDescriptor, event string) bool {
	for _, group := range d.ManagedEntries {
		if group.Path[len(group.Path)-1] != event {
			continue
		}
		for _, entry := range group.Entries {
			if closed, _ := entry["failClosed"].(bool); closed {
				return true
			}
		}
	}
	return false
}
