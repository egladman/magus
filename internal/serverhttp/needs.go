package serverhttp

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	mcp "github.com/egladman/magus/internal/handler/mcp"
	"github.com/egladman/magus/types"
)

var (
	needMCP          = mcp.ToolNeed
	needTokens       = types.Need{Surface: types.SurfaceTokens, Level: types.LevelWrite}
	needConsoleRead  = types.Need{Surface: types.SurfaceConsole, Level: types.LevelRead}
	needConsoleWrite = types.Need{Surface: types.SurfaceConsole, Level: types.LevelWrite}
)

// procedureNeeds is the Need of every Connect procedure the server serves, keyed by service
// then method. serviceNeeds refuses a service whose methods and entries here disagree, so a
// procedure added to a proto without a Need stops the server from starting.
var procedureNeeds = map[protoreflect.FullName]map[protoreflect.Name]types.Need{
	"magus.activity.v1alpha1.ActivityService": {
		"ListActivityEvents":  needConsoleRead,
		"GetPayload":          needConsoleRead,
		"WatchActivityEvents": needConsoleRead,
	},
	"magus.graph.v1alpha1.GraphService": {
		"QueryNodes":     needConsoleRead,
		"ResolveNodes":   needConsoleRead,
		"ExplainNode":    needConsoleRead,
		"FindPath":       needConsoleRead,
		"FindDependents": needConsoleRead,
		"FindAffected":   needConsoleRead,
		"GetGraphStats":  needConsoleRead,
	},
	"magus.insight.v1alpha1.InsightService": {
		"GetInsight": needConsoleRead,
	},
	"magus.job.v1alpha1.JobService": {
		"ListJobs": needConsoleRead,
		"RunJob":   needConsoleWrite,
	},
	// Memory reads are console=write too: the notes are the operator's own, and reading them
	// is audited like an edit.
	"magus.memory.v1alpha1.MemoryService": {
		"ListMemories": needConsoleWrite,
		"GetCursor":    needConsoleWrite,
		"UpdateMemory": needConsoleWrite,
		"DeleteMemory": needConsoleWrite,
		"UpdateCursor": needConsoleWrite,
	},
	"magus.metrics.v1alpha1.MetricsService": {
		"GetMetrics":    needConsoleRead,
		"StreamMetrics": needConsoleRead,
	},
	"magus.notes.v1alpha1.NotesService": {
		"ListNotes": needConsoleRead,
		"GetNote":   needConsoleRead,
	},
	"magus.status.v1alpha1.StatusService": {
		"GetStatus":    needConsoleRead,
		"StreamStatus": needConsoleRead,
	},
	// Listing tokens is tokens=write as well: only the operator manages tokens at all.
	"magus.token.v1alpha1.TokenService": {
		"ListTokens":  needTokens,
		"CreateToken": needTokens,
		"RevokeToken": needTokens,
	},
	"magus.tool.v1alpha1.ToolService": {
		"ListTools": needConsoleRead,
	},
	"magus.viewer.v1alpha1.ViewerService": {
		"GetInvocation":      needConsoleRead,
		"ListEvents":         needConsoleRead,
		"StreamEvents":       needConsoleRead,
		"ListOutputs":        needConsoleRead,
		"GetOutput":          needConsoleRead,
		"ListInvocations":    needConsoleRead,
		"GetJournal":         needConsoleRead,
		"GetSessionActivity": needConsoleRead,
	},
}

// apiNeeds is the Need of every JSON route under /api/. Only the routes the share listener
// also serves, and the graph document GraphService reads at the same level, are console=read.
// The rest serve unreviewed source, every target's name, or an action, so they need
// console=write.
var apiNeeds = map[string]types.Need{
	"/api/v1/events":        needConsoleRead,
	"/api/v1/insight":       needConsoleRead,
	"/api/v1/graph":         needConsoleRead,
	"/api/v1/diff":          needConsoleWrite,
	"/api/v1/diff/patch":    needConsoleWrite,
	"/api/v1/diff/context":  needConsoleWrite,
	"/api/v1/diff/session":  needConsoleWrite,
	"/api/v1/diff/review":   needConsoleWrite,
	"/api/v1/diff/branches": needConsoleWrite,
	"/api/v1/diff/run":      needConsoleWrite,
	"/api/v1/plan":          needConsoleWrite,
	"/api/v1/attention":     needConsoleWrite,
	"/api/v1/share":         needConsoleWrite,
	"/api/":                 needConsoleRead,
}

// apiNeedsOf is the one-entry needs map a guard over the JSON route at path takes.
func apiNeedsOf(path string) (map[string]types.Need, error) {
	need, ok := apiNeeds[path]
	if !ok {
		return nil, fmt.Errorf("server: route %s has no Need", path)
	}
	return map[string]types.Need{path: need}, nil
}

// apiNeedsFor is apiNeedsOf for the share listener, which refuses to guard a route whose
// Needs are empty, so an unlisted route cannot be served there.
func apiNeedsFor(path string) map[string]types.Need {
	needs, _ := apiNeedsOf(path)
	return needs
}

// serviceNeeds maps each procedure path of the Connect service mounted at path
// ("/<package>.<Service>/") to its Need. It fails when the service is not registered, has no
// entry in procedureNeeds, or when a method and an entry do not match one to one.
func serviceNeeds(path string) (map[string]types.Need, error) {
	name := protoreflect.FullName(strings.Trim(path, "/"))
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
	if err != nil {
		return nil, fmt.Errorf("server: service %s: %w", name, err)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("server: %s is not a service", name)
	}
	table, ok := procedureNeeds[name]
	if !ok {
		return nil, fmt.Errorf("server: service %s has no Needs", name)
	}
	methods := sd.Methods()
	if methods.Len() != len(table) {
		return nil, fmt.Errorf("server: service %s has %d methods and %d Needs", name, methods.Len(), len(table))
	}
	needs := make(map[string]types.Need, len(table))
	for i := range methods.Len() {
		m := methods.Get(i).Name()
		need, ok := table[m]
		if !ok {
			return nil, fmt.Errorf("server: procedure %s/%s has no Need", name, m)
		}
		needs["/"+string(name)+"/"+string(m)] = need
	}
	return needs, nil
}
