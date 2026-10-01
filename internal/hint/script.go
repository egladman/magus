package hint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	json "github.com/egladman/magus/internal/json"
)

// A workspace script is served as a breadcrumb when a situation it declares it serves
// comes up: a guard rule refusing a line, a diagnostic, a doctor check, another next,
// or one of the events below. The graph build reads each script's declaration and
// writes scripts.idx; a serving site asks ScriptsFor and never loads the graph.

// ScriptIDPrefix starts the id of every script breadcrumb, so `session hints` counts
// uptake per script beside the built-in templates.
const ScriptIDPrefix = "script-"

// The effects a script declares. Anything else reads as a write.
const (
	ScriptEffectRead  = "read"
	ScriptEffectWrite = "write"
)

// scriptsIndexFormat is bumped whenever ScriptsIndex changes shape. An index in any
// other format serves nothing until the next graph build rewrites it.
const scriptsIndexFormat = 1

// ScriptsIndex is the content of scripts.idx: which scripts serve which situation,
// stamped so a reader can tell when an entry no longer describes its file.
type ScriptsIndex struct {
	Format int `json:"format"`
	// Root is the absolute workspace root every Path is relative to.
	Root string `json:"root"`
	// Revision is the commit the index was built at, "" where no version control
	// answers. ScriptsFor does not compare it: a checkout that moves rewrites the
	// files it changes, which the per-script stamp already catches without a VCS
	// process on the serving path.
	Revision string `json:"revision,omitempty"`
	// Serves maps a situation (see ValidateSituation) to the scripts serving it, in
	// the order they are offered.
	Serves map[string][]ScriptServe `json:"serves"`
}

// ScriptServe is one script offered for one situation.
type ScriptServe struct {
	// Path is the script, workspace-relative and slash-separated.
	Path string `json:"path"`
	// Args follow `--` on the command line. {ref}, {rule} and {project} are filled
	// from the serving site's facts.
	Args []string `json:"args,omitempty"`
	// Effect is ScriptEffectRead or ScriptEffectWrite, read from the committed blob
	// Blob names. The writer records read only when the working file matched that
	// blob, since a served read is pre-authorized for every role.
	Effect string `json:"effect"`
	// Blob is the version-control id of the committed blob Effect was read from.
	Blob string `json:"blob"`
	// Summary is the declaration's one sentence, served as the breadcrumb's Why.
	Summary string `json:"summary,omitempty"`
	// MtimeNs and Size stamp the working file when the index was built. A file whose
	// stamp no longer matches is still served, but never as a read.
	MtimeNs int64 `json:"mtime_ns"`
	Size    int64 `json:"size"`
}

// ScriptsIndexPath is scripts.idx under a workspace cache dir, beside guard.idx in
// knowledge.StoreDir (which imports this package, so the directory is spelled twice).
func ScriptsIndexPath(cacheDir string) string {
	return filepath.Join(cacheDir, "knowledge", "scripts.idx")
}

// WriteScriptsIndex atomically replaces scripts.idx with x, stamped in this
// magus's format.
func WriteScriptsIndex(cacheDir string, x ScriptsIndex) error {
	x.Format = scriptsIndexFormat
	data, err := json.Marshal(x)
	if err != nil {
		return err
	}
	path := ScriptsIndexPath(cacheDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return file.ReplaceFile(path, data, 0o600)
}

// Situation families: the prefix names the registry an id resolves against.
const (
	SituationEvent  = "event:"
	SituationRule   = "rule:"
	SituationMGS    = "mgs:"
	SituationDoctor = "doctor:"
	SituationNext   = "next:"
)

var situationFamilies = []string{SituationEvent, SituationRule, SituationMGS, SituationDoctor, SituationNext}

// The events a script may serve. They have no registry of their own elsewhere, so
// this list is it, and it is closed: an id not here is an error, not a situation that
// never comes up.
const (
	EventDiskLow    = SituationEvent + "disk-low"
	EventPrePR      = SituationEvent + "pre-pr"
	EventSessionEnd = SituationEvent + "session-end"
	EventIndexStale = SituationEvent + "index-stale"
	EventDoctor     = SituationEvent + "doctor"
)

// AllEvents is every event situation.
var AllEvents = []string{EventDiskLow, EventPrePR, EventSessionEnd, EventIndexStale, EventDoctor}

// ValidateSituation reports an error for a situation outside every family, one with
// an empty id, or an event not in AllEvents. Ids of the other families resolve
// against the registries that own them (rules, diagnostics, doctor checks, next ids),
// which this package does not hold.
func ValidateSituation(situation string) error {
	i := slices.IndexFunc(situationFamilies, func(f string) bool { return strings.HasPrefix(situation, f) })
	if i < 0 {
		return fmt.Errorf("hint: situation %q names no family; want one of %s", situation, strings.Join(situationFamilies, " "))
	}
	if situation == situationFamilies[i] {
		return fmt.Errorf("hint: situation %q names no id", situation)
	}
	if situationFamilies[i] == SituationEvent && !slices.Contains(AllEvents, situation) {
		return fmt.Errorf("hint: unknown event %q; want one of %s", situation, strings.Join(AllEvents, " "))
	}
	return nil
}

// scriptPlaceholders are the facts a script's args may name.
var scriptPlaceholders = []string{"ref", "rule", "project"}

var placeholderRe = regexp.MustCompile(`\{([a-z]+)\}`)

// ScriptsFor returns the breadcrumbs for every script the index under cacheDir lists
// for situation, in index order and uncapped: the serving site merges them with its
// own and ServableTo caps what survives.
//
// It errors only on a situation ValidateSituation rejects. A missing, unreadable or
// foreign-format index serves nothing, as does an entry whose file is gone, whose
// args name a placeholder facts does not bind, or whose id an earlier entry already
// took. An entry is a read only when it declares read and its file still matches the
// stamp; Reads is never set otherwise.
func ScriptsFor(cacheDir, situation string, facts map[string]string) ([]Next, error) {
	if err := ValidateSituation(situation); err != nil {
		return nil, err
	}
	if cacheDir == "" {
		return nil, nil
	}
	data, err := os.ReadFile(ScriptsIndexPath(cacheDir))
	if err != nil {
		return nil, nil
	}
	var x ScriptsIndex
	if err := json.Unmarshal(data, &x); err != nil || x.Format != scriptsIndexFormat || x.Root == "" {
		return nil, nil
	}
	var next []Next
	for _, s := range x.Serves[situation] {
		n, ok := scriptNext(x.Root, s, facts)
		if !ok || slices.ContainsFunc(next, func(o Next) bool { return o.ID == n.ID }) {
			continue
		}
		next = append(next, n)
	}
	return next, nil
}

// scriptNext builds one entry's breadcrumb, and reports false when it cannot be served.
func scriptNext(root string, s ScriptServe, facts map[string]string) (Next, bool) {
	if !filepath.IsLocal(filepath.FromSlash(s.Path)) || filepath.Ext(s.Path) != ".buzz" {
		return Next{}, false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(s.Path)))
	if err != nil || !info.Mode().IsRegular() {
		return Next{}, false
	}
	args, ok := bindScriptArgs(s.Args, facts)
	if !ok {
		return Next{}, false
	}
	operands := []string{s.Path}
	if len(args) > 0 {
		operands = append(append(operands, "--"), args...)
	}
	n := breadcrumb(ScriptIDPrefix+strings.TrimSuffix(filepath.Base(s.Path), ".buzz"), Buzz, s.Summary, operands...)
	n.Reads = s.Effect == ScriptEffectRead && info.ModTime().UnixNano() == s.MtimeNs && info.Size() == s.Size
	return n, true
}

// bindScriptArgs fills every placeholder from facts, and reports false when any arg
// names one facts leaves empty or one outside scriptPlaceholders: a served command
// never carries a placeholder (see Next).
func bindScriptArgs(args []string, facts map[string]string) ([]string, bool) {
	bound := make([]string, len(args))
	ok := true
	for i, a := range args {
		bound[i] = placeholderRe.ReplaceAllStringFunc(a, func(m string) string {
			name := m[1 : len(m)-1]
			v := facts[name]
			if !slices.Contains(scriptPlaceholders, name) || v == "" {
				ok = false
			}
			return v
		})
	}
	return bound, ok
}
