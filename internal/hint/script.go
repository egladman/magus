package hint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// A workspace script declares the situations it serves (BuildScriptsIndex);
// `magus graph build` indexes every tracked script into scripts.idx, and a serving site
// asks MatchScripts or MatchReplaced, never the graph.

// ScriptIDPrefix starts the id of every script breadcrumb, so `session hints` counts
// uptake per script beside the built-in templates.
const ScriptIDPrefix = "script-"

// The effects a script declares. A script that declares none is a write.
const (
	ScriptEffectRead  = "read"
	ScriptEffectWrite = "write"
)

// scriptsIndexFormat is bumped whenever ScriptsIndex changes shape. An index in any
// other format serves nothing until the next graph build rewrites it.
const scriptsIndexFormat = 2

// ScriptsIndex is the content of scripts.idx: which scripts serve which situation,
// stamped so a reader can tell when an entry no longer describes its file.
type ScriptsIndex struct {
	Format int `json:"format"`
	// Root is the absolute workspace root every Path is relative to.
	Root string `json:"root"`
	// Serves maps a situation (see ValidateSituation) to the scripts serving it, in
	// the order they are offered.
	Serves map[string][]ScriptServe `json:"serves"`
}

// ScriptServe is one script offered for one situation.
type ScriptServe struct {
	// Path is the script, workspace-relative and slash-separated.
	Path string `json:"path"`
	// Args follow `--` on the command line. Each placeholder in scriptPlaceholders is
	// filled from the serving site's facts.
	Args []string `json:"args,omitempty"`
	// Effect is ScriptEffectRead only when the script declares read and its file was
	// the committed one when indexed, since a served read is pre-authorized for every role.
	Effect string `json:"effect"`
	// Summary is the declaration's one line, served as the breadcrumb's Why.
	Summary string `json:"summary"`
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

// readScriptsIndex reads scripts.idx under cacheDir, false when cacheDir is "" or the
// index is missing, unreadable, or written in another magus's format.
func readScriptsIndex(cacheDir string) (ScriptsIndex, bool) {
	var x ScriptsIndex
	if cacheDir == "" {
		return x, false
	}
	data, err := os.ReadFile(ScriptsIndexPath(cacheDir))
	if err != nil {
		return x, false
	}
	if err := json.Unmarshal(data, &x); err != nil || x.Format != scriptsIndexFormat || x.Root == "" {
		return x, false
	}
	return x, true
}

// Situation families: the prefix names the registry an id resolves against.
const (
	SituationEvent  = "event:"
	SituationRule   = "rule:"
	SituationMGS    = "mgs:"
	SituationDoctor = "doctor:"
	SituationNext   = "next:"
	// situationReplaces is a command shape the script replaces: a program and the
	// arguments a line must carry, in any order, as a workspace shell rule matches them.
	situationReplaces = "replaces:"
)

var situationFamilies = []string{SituationEvent, SituationRule, SituationMGS, SituationDoctor, SituationNext, situationReplaces}

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
		return fmt.Errorf("hint: situation %q names no family, want one of %s", situation, strings.Join(situationFamilies, " "))
	}
	if strings.TrimSpace(strings.TrimPrefix(situation, situationFamilies[i])) == "" {
		return fmt.Errorf("hint: situation %q names no id", situation)
	}
	if situationFamilies[i] == SituationEvent && !slices.Contains(AllEvents, situation) {
		return fmt.Errorf("hint: unknown event %q, want one of %s", situation, strings.Join(AllEvents, " "))
	}
	return nil
}

// scriptDeclaration is what one script declares about itself.
type scriptDeclaration struct {
	Summary string
	Effect  string
	Serves  []scriptSituation
}

// scriptSituation is one SERVES entry: a situation, then optionally ` -- ` and the
// arguments the script is served with.
type scriptSituation struct {
	Situation string
	Args      []string
}

var (
	summaryDeclRe = regexp.MustCompile(`(?m)^(?:export\s+)?final\s+SUMMARY\s*=\s*("(?:[^"\\\n]|\\.)*")\s*;`)
	effectDeclRe  = regexp.MustCompile(`(?m)^(?:export\s+)?final\s+EFFECT\s*=\s*("(?:[^"\\\n]|\\.)*")\s*;`)
	servesDeclRe  = regexp.MustCompile(`(?ms)^(?:export\s+)?final\s+SERVES\s*=\s*\[(.*?)\]\s*;`)
	quotedRe      = regexp.MustCompile(`"(?:[^"\\\n]|\\.)*"`)
)

// parseScriptDeclaration reads the declarations BuildScriptsIndex shows as text, running
// nothing; none of them is false. SERVES without a one-line SUMMARY, an unknown EFFECT,
// or an entry ValidateSituation or the placeholder set rejects is an error.
func parseScriptDeclaration(src []byte) (scriptDeclaration, bool, error) {
	var d scriptDeclaration
	summary, hasSummary := declaredString(summaryDeclRe, src)
	effect, hasEffect := declaredString(effectDeclRe, src)
	serves := servesDeclRe.FindSubmatch(src)
	if !hasSummary && !hasEffect && serves == nil {
		return d, false, nil
	}
	if serves == nil {
		return d, true, errors.New("declares SUMMARY or EFFECT but no SERVES list")
	}
	if strings.TrimSpace(summary) == "" {
		return d, true, errors.New("declares SERVES without a SUMMARY line")
	}
	d.Summary, d.Effect = summary, ScriptEffectWrite
	if hasEffect {
		if effect != ScriptEffectRead && effect != ScriptEffectWrite {
			return d, true, fmt.Errorf("EFFECT %q is neither %q nor %q", effect, ScriptEffectRead, ScriptEffectWrite)
		}
		d.Effect = effect
	}
	var errs []error
	for _, q := range quotedRe.FindAll(serves[1], -1) {
		entry, err := strconv.Unquote(string(q))
		if err != nil {
			errs = append(errs, fmt.Errorf("SERVES entry %s: %w", q, err))
			continue
		}
		s, err := parseScriptSituation(entry)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		d.Serves = append(d.Serves, s)
	}
	if len(d.Serves) == 0 && len(errs) == 0 {
		errs = append(errs, errors.New("declares an empty SERVES list"))
	}
	return d, true, errors.Join(errs...)
}

func declaredString(re *regexp.Regexp, src []byte) (string, bool) {
	m := re.FindSubmatch(src)
	if m == nil {
		return "", false
	}
	s, err := strconv.Unquote(string(m[1]))
	return s, err == nil
}

func parseScriptSituation(entry string) (scriptSituation, error) {
	situation, args, _ := strings.Cut(entry, " -- ")
	s := scriptSituation{Situation: strings.Join(strings.Fields(situation), " ")}
	if args = strings.TrimSpace(args); args != "" {
		s.Args = strings.Fields(args)
	}
	if err := ValidateSituation(s.Situation); err != nil {
		return s, fmt.Errorf("SERVES entry %q: %w", entry, err)
	}
	for _, a := range s.Args {
		for _, m := range placeholderRe.FindAllStringSubmatch(a, -1) {
			if !slices.Contains(scriptPlaceholders, m[1]) {
				return s, fmt.Errorf("SERVES entry %q: placeholder {%s} is not one of %s", entry, m[1], strings.Join(scriptPlaceholders, " "))
			}
		}
	}
	return s, nil
}

// BuildScriptsIndex indexes the top-level declarations of each slash-separated .buzz path
// under root (EFFECT is optional, a write when absent):
//
//	final SUMMARY = "rename a code symbol at every occurrence the index verifies";
//	final SERVES = ["rule:sed-in-place", "replaces:git worktree remove -- --apply"];
//	final EFFECT = "read";
//
// committed says a path is as committed, which a read needs; a bad script is named in the error.
func BuildScriptsIndex(root string, paths []string, committed func(path string) bool) (ScriptsIndex, error) {
	x := ScriptsIndex{Root: root, Serves: map[string][]ScriptServe{}}
	var errs []error
	for _, rel := range slices.Sorted(slices.Values(paths)) {
		if filepath.Ext(rel) != ".buzz" {
			continue
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		src, err := os.ReadFile(abs)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		d, ok, err := parseScriptDeclaration(src)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		if !ok {
			continue
		}
		effect := d.Effect
		if effect == ScriptEffectRead && !committed(rel) {
			effect = ScriptEffectWrite
		}
		for _, s := range d.Serves {
			x.Serves[s.Situation] = append(x.Serves[s.Situation], ScriptServe{
				Path: rel, Args: s.Args, Effect: effect, Summary: d.Summary,
				MtimeNs: info.ModTime().UnixNano(), Size: info.Size(),
			})
		}
	}
	return x, errors.Join(errs...)
}

// MissingScripts lists, sorted, every script the index under cacheDir serves whose file
// is gone, false when there is no index to read.
func MissingScripts(cacheDir string) ([]string, bool) {
	x, ok := readScriptsIndex(cacheDir)
	if !ok {
		return nil, false
	}
	var missing []string
	for _, entries := range x.Serves {
		for _, s := range entries {
			info, err := os.Stat(filepath.Join(x.Root, filepath.FromSlash(s.Path)))
			if (err != nil || !info.Mode().IsRegular()) && !slices.Contains(missing, s.Path) {
				missing = append(missing, s.Path)
			}
		}
	}
	slices.Sort(missing)
	return missing, true
}

// scriptPlaceholders are the facts a script's args may name.
var scriptPlaceholders = []string{"ref", "rule", "project", "file"}

var placeholderRe = regexp.MustCompile(`\{([a-z]+)\}`)

// MatchScripts returns the breadcrumbs for every script the index under cacheDir lists
// for situation, in index order and uncapped: the serving site merges them with its
// own and FilterServable caps what survives.
//
// It errors only on a situation ValidateSituation rejects. A missing, unreadable or
// foreign-format index serves nothing, as does an entry whose file is gone (which
// MissingScripts reports), whose args name a placeholder facts does not bind, or whose
// id an earlier entry already took. Reads is set only for a read whose stamp holds.
func MatchScripts(cacheDir, situation string, facts map[string]string) ([]types.NextStep, error) {
	if err := ValidateSituation(situation); err != nil {
		return nil, err
	}
	x, ok := readScriptsIndex(cacheDir)
	if !ok {
		return nil, nil
	}
	return x.serve(x.Serves[situation], facts, nil), nil
}

// MatchReplaced returns the breadcrumbs for every script the index under cacheDir
// declares replaces a command shape one of cmds carries, in situation order, then index
// order, served as MatchScripts serves them.
func MatchReplaced(cacheDir string, cmds []Invocation, facts map[string]string) []types.NextStep {
	x, ok := readScriptsIndex(cacheDir)
	if !ok {
		return nil
	}
	var shapes []string
	for s := range x.Serves {
		if strings.HasPrefix(s, situationReplaces) {
			shapes = append(shapes, s)
		}
	}
	slices.Sort(shapes)
	var next []types.NextStep
	for _, situation := range shapes {
		shape := strings.TrimPrefix(situation, situationReplaces)
		if slices.ContainsFunc(cmds, func(c Invocation) bool { return carriesShape(c, shape) }) {
			next = x.serve(x.Serves[situation], facts, next)
		}
	}
	return next
}

// carriesShape reports whether c runs shape's program with every one of its arguments,
// in any order: the match a workspace shell rule makes.
func carriesShape(c Invocation, shape string) bool {
	words := strings.Fields(shape)
	if len(words) == 0 || filepath.Base(c.Name) != words[0] {
		return false
	}
	return !slices.ContainsFunc(words[1:], func(w string) bool { return !slices.Contains(c.Args, w) })
}

// serve appends to next the breadcrumb of each entry it can serve whose id next does not
// already hold.
func (x ScriptsIndex) serve(entries []ScriptServe, facts map[string]string, next []types.NextStep) []types.NextStep {
	for _, s := range entries {
		n, ok := scriptNext(x.Root, s, facts)
		if !ok || slices.ContainsFunc(next, func(o types.NextStep) bool { return o.ID == n.ID }) {
			continue
		}
		next = append(next, n)
	}
	return next
}

// scriptNext builds one entry's breadcrumb, and reports false when it cannot be served.
func scriptNext(root string, s ScriptServe, facts map[string]string) (types.NextStep, bool) {
	if !filepath.IsLocal(filepath.FromSlash(s.Path)) || filepath.Ext(s.Path) != ".buzz" {
		return types.NextStep{}, false
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(s.Path)))
	if err != nil || !info.Mode().IsRegular() {
		return types.NextStep{}, false
	}
	args, ok := bindScriptArgs(s.Args, facts)
	if !ok {
		return types.NextStep{}, false
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
// never carries a placeholder (see types.NextStep).
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
