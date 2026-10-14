package bindings

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

func init() {
	agent.RegisterHarnessSpellLoader(loadHarnessFromSpell)
}

// loadHarnessFromSpell builds a HarnessDescriptor by invoking the harness_*
// contract on a registered spell. A spell not registered under id at all is a
// miss (ok=false); LoadHarness has no other source to fall back to.
func loadHarnessFromSpell(ctx context.Context, id string) (agent.HarnessDescriptor, string, bool, error) {
	drv, ok := project.DefaultSpellRegistry().Lookup(id)
	if !ok {
		return agent.HarnessDescriptor{}, "", false, nil
	}
	cfgResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessConfigContract})
	if err != nil {
		return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessConfigContract, err)
	}
	if cfgResp.Data == nil {
		return agent.HarnessDescriptor{}, "", false, nil
	}
	d := agent.HarnessDescriptor{
		SchemaVersion: harnessSchemaVersionFromAgent(),
		ID:            id,
		Display:       agent.HarnessDisplay{Name: displayName(id)},
	}
	if err := decodeHarnessConfig(cfgResp.Data, &d); err != nil {
		return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessConfigContract, err)
	}
	skillsResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessSkillsContract})
	if err != nil {
		return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessSkillsContract, err)
	}
	if skillsResp.Data != nil {
		if err := decodeHarnessSkills(skillsResp.Data, &d); err != nil {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessSkillsContract, err)
		}
	}
	entriesResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessEntriesContract})
	if err != nil {
		return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessEntriesContract, err)
	}
	if entriesResp.Data != nil {
		if err := decodeHarnessEntries(entriesResp.Data, &d); err != nil {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessEntriesContract, err)
		}
	}
	mcpResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessMCPContract})
	if err != nil {
		// Optional contract: a missing op is fine; only a failed invoke of a
		// present op should fail the load. Spell drivers return err when the
		// target is unknown, so treat that as absent.
		if !isMissingHarnessOp(err) {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessMCPContract, err)
		}
	} else if mcpResp.Data != nil {
		if err := decodeHarnessMCP(mcpResp.Data, &d); err != nil {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessMCPContract, err)
		}
	}
	promptsResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessPromptsContract, Params: map[string]any{"human_only": humanOnlyReasons(ctx)}})
	if err != nil {
		if !isMissingHarnessOp(err) {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessPromptsContract, err)
		}
	} else if promptsResp.Data != nil {
		if err := decodeHarnessJSON(promptsResp.Data, &d.Prompts); err != nil {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessPromptsContract, err)
		}
	}
	agents, err := agent.ShippedAgentParams()
	if err != nil {
		return agent.HarnessDescriptor{}, "", false, err
	}
	agentsResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessAgentsContract, Params: map[string]any{"agents": agents}})
	if err != nil {
		if !isMissingHarnessOp(err) {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessAgentsContract, err)
		}
	} else if agentsResp.Data != nil {
		if err := decodeHarnessJSON(agentsResp.Data, &d.Agents); err != nil {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessAgentsContract, err)
		}
	}
	settingsResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessSettingsContract})
	if err != nil {
		if !isMissingHarnessOp(err) {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessSettingsContract, err)
		}
	} else if settingsResp.Data != nil {
		if err := decodeHarnessJSON(settingsResp.Data, &d.Settings); err != nil {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessSettingsContract, err)
		}
	}
	limitsResp, err := drv.Invoke(ctx, spells.InvokeRequest{Target: spells.HarnessLimitsContract})
	if err != nil {
		if !isMissingHarnessOp(err) {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessLimitsContract, err)
		}
	} else if limitsResp.Data != nil {
		if err := decodeHarnessJSON(limitsResp.Data, &d.Limits); err != nil {
			return agent.HarnessDescriptor{}, "", false, fmt.Errorf("harness spell %q: %s: %w", id, spells.HarnessLimitsContract, err)
		}
	}
	return d, "spell:" + id, true, nil
}

// humanOnlyReasons maps each target the workspace on ctx declares human_only to its
// reason, joining the reasons of one name declared in several projects. A host's native
// prompt matches a run by target name alone, so the project is not kept. Empty when ctx
// carries no workspace.
func humanOnlyReasons(ctx context.Context) map[string]any {
	out := map[string]any{}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return out
	}
	for _, p := range ws.All() {
		for _, name := range slices.Sorted(maps.Keys(p.TargetPolicies)) {
			pol := p.TargetPolicies[name]
			if !pol.HumanOnly {
				continue
			}
			if prior, ok := out[name].(string); ok {
				out[name] = prior + "; " + pol.HumanOnlyReason
				continue
			}
			out[name] = pol.HumanOnlyReason
		}
	}
	return out
}

// decodeHarnessJSON reads a spell's return value into the descriptor field through the
// field's own JSON shape, so a spell and a JSON descriptor cannot disagree about a key.
// A slice field wants a list and any other a map; on error into is left as it was.
func decodeHarnessJSON[T any](data any, into *T) error {
	if reflect.TypeFor[T]().Kind() == reflect.Slice {
		if _, ok := data.([]any); !ok {
			return fmt.Errorf("want list, got %T", data)
		}
	} else if _, ok := data.(map[string]any); !ok {
		return fmt.Errorf("want map, got %T", data)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var decoded T
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	*into = decoded
	return nil
}

func isMissingHarnessOp(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no such") ||
		strings.Contains(msg, "undefined")
}

func harnessSchemaVersionFromAgent() int {
	// Keep in lock-step with agent.harnessSchemaVersion without exporting it.
	return 2
}

func displayName(id string) string {
	if id == "" {
		return ""
	}
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func decodeHarnessConfig(data any, d *agent.HarnessDescriptor) error {
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("want map, got %T", data)
	}
	if path, ok := m["path"].(string); ok {
		d.Config.Path = path
	}
	if defaults, ok := m["defaults"].(map[string]any); ok {
		d.ConfigDefaults = defaults
	}
	return nil
}

func decodeHarnessSkills(data any, d *agent.HarnessDescriptor) error {
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("want map, got %T", data)
	}
	if paths, ok := m["paths"].([]any); ok {
		for i, p := range paths {
			s, ok := p.(string)
			if !ok {
				return fmt.Errorf("paths[%d] must be a string", i)
			}
			d.Skills.Paths = append(d.Skills.Paths, s)
		}
	}
	if form, ok := m["form"].(string); ok {
		d.Skills.Form = agent.Form(form)
	}
	return nil
}

func decodeHarnessEntries(data any, d *agent.HarnessDescriptor) error {
	list, ok := data.([]any)
	if !ok {
		return fmt.Errorf("want list, got %T", data)
	}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("[%d] want map, got %T", i, item)
		}
		var he agent.HarnessEntries
		if path, ok := m["path"].([]any); ok {
			for j, p := range path {
				s, ok := p.(string)
				if !ok {
					return fmt.Errorf("[%d].path[%d] must be a string", i, j)
				}
				he.Path = append(he.Path, s)
			}
		}
		if entries, ok := m["entries"].([]any); ok {
			for j, e := range entries {
				em, ok := e.(map[string]any)
				if !ok {
					return fmt.Errorf("[%d].entries[%d] must be a map", i, j)
				}
				he.Entries = append(he.Entries, em)
			}
		}
		d.ManagedEntries = append(d.ManagedEntries, he)
	}
	return nil
}

func decodeHarnessMCP(data any, d *agent.HarnessDescriptor) error {
	m, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("want map, got %T", data)
	}
	mcp := &agent.HarnessMCP{}
	if v, ok := m["enabled"].(bool); ok {
		mcp.Enabled = &v
	}
	if v, ok := m["token_ref"].(string); ok {
		mcp.TokenRef = v
	}
	if v, ok := m["url"].(string); ok {
		mcp.URL = v
	}
	if v, ok := m["hint"].(string); ok {
		mcp.Hint = v
	}
	if v, ok := m["docs"].(string); ok {
		mcp.Docs = v
	}
	if v, ok := m["register"].([]any); ok {
		for i, a := range v {
			s, ok := a.(string)
			if !ok {
				return fmt.Errorf("register[%d] must be a string", i)
			}
			mcp.Register = append(mcp.Register, s)
		}
	}
	d.MCP = mcp
	return nil
}
