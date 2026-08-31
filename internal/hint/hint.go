// Package hint is the hints home: everything magus renders to point a reader
// at a better next command. It holds shell-to-graph suggestion translation
// (this file), the canonical MCP tool names and follow-up lines (mcptool.go),
// and the canonical CLI command paths shown in user-facing output
// (clicommand.go). It stays a near-leaf - stdlib plus types - so an emitter
// anywhere in the tree can render a hint without acquiring a dependency set
// to get command strings.
//
// The translator turns parsed shell commands into magus knowledge-graph
// suggestions: given the search an agent just typed, the magus command most
// likely to answer the same question.
//
// The translator is SYNTACTIC, not SEMANTIC. Its job is to shape the best
// magus suggestion from a command's own arguments; it never claims the
// suggestion will hit, never asks the graph whether a symbol exists, and
// every suggestion is hedged and advisory-only. False positives are prevented
// by ABSTENTION: when a command has no honest graph equivalent (a transform,
// a single-file read, an unrecognized tool), Suggest returns nil.
//
// Tokenization stays with the caller: hint receives already-parsed commands
// and never touches shell syntax.
package hint

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Invocation is one parsed tool invocation the agent ran: the program name
// (base name, no path) and its arguments with quoting already resolved.
type Invocation struct {
	Name string
	Args []string
}

// Confidence orders suggestions by how likely each is to answer the search it
// replaces. It is never a promise of a hit; the Hedge says what a miss means.
type Confidence int

const (
	ConfidenceLow Confidence = iota
	ConfidenceMedium
	ConfidenceHigh
)

// Suggestion is one magus command worth trying in place of the caught search.
type Suggestion struct {
	Run        string // a command to paste, e.g. `magus refs HandleFoo`
	Why        string // one line: why this is the graph answer to that search
	Confidence Confidence
	Hedge      string // what an empty result means, so a miss reads as an answer
}

// Class is the tool taxonomy Classify reports. Classification follows what
// the tool would actually scan: a non-recursive grep pointed at files reads
// them, so it classifies as ClassRead, while rg and ag are recursive by
// default and stay ClassSearchSource even with file operands narrowing them.
type Class int

const (
	ClassNone         Class = iota // unrecognized tool
	ClassSearchSource              // repo-wide text search over source (grep -r, rg, ag)
	ClassSearchProse               // search whose operands name a .md file
	ClassFileFind                  // find -name / fd: filename lookup
	ClassRead                      // cat/bat/head/tail/less/more/sed -n: reading a named file
	ClassTransform                 // awk/sed/sd: text transformation
)

// Translator holds the workspace facts the syntactic translation may use. All
// of them are optional: a zero-option Translator still translates, it just
// cannot scope. It is not a graph - it never queries one - and the name would
// collide with knowledge.Graph in the callers that hold both.
type Translator struct {
	projects []string
}

type Option func(*Translator)

// WithProjects supplies workspace-relative project directories, enabling
// project= scoping of query suggestions from a search's path operands.
func WithProjects(paths []string) Option {
	return func(t *Translator) {
		for _, p := range paths {
			// The root project is dropped: `project=.` scopes a query to everything,
			// which says nothing.
			if c := path.Clean(p); c != "." {
				t.projects = append(t.projects, c)
			}
		}
	}
}

func NewTranslator(opts ...Option) *Translator {
	t := &Translator{}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// toolSpec is everything the translator knows about one search tool, so a
// reviewer checks a row against one manpage instead of hunting the same fact
// through several sets.
type toolSpec struct {
	valueShorts        string // short flags that consume the NEXT word, for THIS tool
	recursiveByDefault bool   // rg and ag walk the tree with no -r
	fixedByDefault     bool   // fgrep is grep -F by definition
}

// commonValueShorts take a value in every search tool, so a row carries only
// the letters where the tools disagree. Merging the disagreements would eat the
// pattern of every `grep -E <pat>` (rg's -E is an encoding, grep's is boolean),
// and the same conflict recurs for grep's boolean -G/-T (ag's -G and rg's -T
// take values) and ag's boolean -t/-D (rg's -t and grep's -D take values).
const commonValueShorts = "ABCm"

// searchTools is both the membership set - a name absent from it is not a
// search - and the per-tool facts the parse depends on.
var searchTools = map[string]toolSpec{
	"grep":  {valueShorts: "dD"},
	"egrep": {valueShorts: "dD"},
	"fgrep": {valueShorts: "dD", fixedByDefault: true},
	"rg":    {valueShorts: "ETtjg", recursiveByDefault: true},
	"ag":    {valueShorts: "gG", recursiveByDefault: true},
}

var readTools = map[string]bool{"cat": true, "bat": true, "head": true, "tail": true, "less": true, "more": true}

// IsSearchTool reports whether name is a content-search tool the translator
// models, so a caller ranking or filtering commands shares one definition of
// the family instead of keeping its own copy.
func IsSearchTool(name string) bool {
	_, ok := searchTools[name]
	return ok
}

var (
	// bareIdentRe recognizes a search pattern that is a single identifier, so the
	// suggestion can route it to `magus refs` (the occurrence-precise symbol answer)
	// rather than a free-text query.
	bareIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{2,}$`)
	// diagnosticCodeRe and buzzOpRe route a pattern to `magus query` instead of `magus refs`,
	// because refs resolves only compiled-language symbols. Measured against real session history:
	// a grep for MGS2011 (a diagnostic) or mgs_listManifests (a Buzz spell op) has a graph answer,
	// but it is a diagnostic/function node that query finds and refs misses. See the guard doc's
	// adoption section for the measurement.
	diagnosticCodeRe = regexp.MustCompile(`^MGS[0-9]{4}$`)
	buzzOpRe         = regexp.MustCompile(`^mgs_[A-Za-z0-9_]+$`)
)

const (
	hedgeRefs  = "An empty result means it was text, not a symbol, and grep is right."
	hedgeQuery = "If it misses, the text is not in the graph and grep is the right tool."
	hedgeFile  = "An empty result means those files are not indexed, and the raw listing is right."
	hedgeProse = "If it misses, the passage is not under an indexed heading and grep is right."
	// Appended to a hedge when the search asked for -i: the graph does not.
	hedgeCase = " The graph matches case-sensitively, so an -i match may differ only in case."
)

// Suggest returns the magus commands most likely to answer cmd, most-confident
// first, or nil when the command has no honest graph equivalent.
func (t *Translator) Suggest(cmd Invocation) []Suggestion {
	if spec, ok := searchTools[cmd.Name]; ok {
		return t.suggestSearch(parseSearch(spec, cmd.Args))
	}
	switch cmd.Name {
	case "find":
		return t.suggestFind(cmd.Args)
	case "fd":
		return t.suggestFd(cmd.Args)
	}
	return nil
}

// Classify reports the tool taxonomy without composing suggestions.
func Classify(cmd Invocation) Class {
	if spec, ok := searchTools[cmd.Name]; ok {
		sa := parseSearch(spec, cmd.Args)
		switch {
		case anyMarkdown(sa.paths()):
			return ClassSearchProse
		case !sa.recursive:
			// Only the grep family can land here: a recursive-by-default tool
			// carries recursive from the seed and nothing clears it.
			return ClassRead
		default:
			return ClassSearchSource
		}
	}
	switch {
	case cmd.Name == "find" || cmd.Name == "fd":
		return ClassFileFind
	case readTools[cmd.Name]:
		return ClassRead
	case cmd.Name == "sed":
		// -n suppresses the default print, the shape of an address-print read
		// (`sed -n '10,20p' file`); -i is a write and outranks it.
		if sedPrints(cmd.Args) {
			return ClassRead
		}
		return ClassTransform
	case cmd.Name == "awk" || cmd.Name == "sd":
		return ClassTransform
	}
	return ClassNone
}

// Patterns returns the search patterns a search command would look for
// (all -e/--regexp values, else the first non-flag operand), nil for
// non-search commands.
func Patterns(cmd Invocation) []string {
	spec, ok := searchTools[cmd.Name]
	if !ok {
		return nil
	}
	sa := parseSearch(spec, cmd.Args)
	pats := sa.pats()
	if len(pats) == 0 || pats[0] == "" {
		return nil
	}
	return pats
}

// IsIdentifier reports whether s has the bare-identifier shape Suggest routes
// to `magus refs`: a letter or underscore followed by at least two word
// characters. Exported so callers layering stricter symbol heuristics share
// one definition of the shape.
func IsIdentifier(s string) bool {
	return bareIdentRe.MatchString(s)
}

func sedPrints(args []string) bool {
	hasN := false
	for _, a := range args {
		if a == "-n" || a == "--quiet" || a == "--silent" {
			hasN = true
		}
		if strings.HasPrefix(a, "-i") || a == "--in-place" {
			return false
		}
	}
	return hasN
}

// searchArgs is a grep/rg/ag invocation reduced to the parts the translator
// reads. Flag parsing is deliberately lenient: an unknown flag is skipped as
// a boolean and must never break pattern extraction.
type searchArgs struct {
	spec       toolSpec
	patterns   []string // every -e/--regexp value
	operands   []string // non-flag words in order
	recursive  bool
	word       bool
	fixed      bool
	ignoreCase bool
	fromFile   bool // -f/--file: patterns come from a file
}

// pats returns the patterns the search would look for: the -e values, else
// the first operand. Empty under -f, where the pattern is unknowable.
func (sa *searchArgs) pats() []string {
	if sa.fromFile {
		return nil
	}
	if len(sa.patterns) > 0 {
		return sa.patterns
	}
	if len(sa.operands) > 0 {
		return sa.operands[:1]
	}
	return nil
}

// paths returns the path operands: everything after the pattern position.
func (sa *searchArgs) paths() []string {
	if len(sa.patterns) > 0 || sa.fromFile {
		return sa.operands
	}
	if len(sa.operands) > 1 {
		return sa.operands[1:]
	}
	return nil
}

// searchLongValueFlags is the union of grep/rg/ag long flags that consume the
// next word, so that word is never mistaken for the pattern or a path.
var searchLongValueFlags = map[string]bool{
	"--include": true, "--exclude": true, "--exclude-dir": true,
	"--glob": true, "--iglob": true, "--type": true, "--type-not": true,
	"--type-add": true, "--threads": true, "--max-count": true,
	"--max-depth": true, "--encoding": true, "--ignore": true,
	"--after-context": true, "--before-context": true, "--context": true,
	"--color": true, "--sort": true, "--binary-files": true, "--label": true,
}

// parseSearch takes the resolved spec rather than a tool name so a caller
// cannot reach it without the table lookup that decides the command is a
// search at all: a zero spec would read as a tool with no value flags.
func parseSearch(spec toolSpec, args []string) searchArgs {
	sa := searchArgs{spec: spec, recursive: spec.recursiveByDefault, fixed: spec.fixedByDefault}
	operandsOnly := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if operandsOnly {
			sa.operands = append(sa.operands, a)
			continue
		}
		switch {
		case a == "--":
			operandsOnly = true
		case a == "-e" || a == "--regexp":
			if i+1 < len(args) {
				i++
				sa.patterns = append(sa.patterns, args[i])
			}
		case strings.HasPrefix(a, "--regexp="):
			sa.patterns = append(sa.patterns, strings.TrimPrefix(a, "--regexp="))
		case a == "-f" || a == "--file":
			sa.fromFile = true
			if i+1 < len(args) {
				i++
			}
		case strings.HasPrefix(a, "--file="):
			sa.fromFile = true
		case a == "-r" || a == "-R" || a == "--recursive":
			sa.recursive = true
		case a == "-w" || a == "--word-regexp":
			sa.word = true
		case a == "-F" || a == "--fixed-strings":
			sa.fixed = true
		case a == "-i" || a == "--ignore-case":
			sa.ignoreCase = true
		case strings.HasPrefix(a, "--"):
			// --flag=value is self-contained; a known value flag consumes the
			// next word; an unknown long flag is a boolean and skipped.
			if !strings.Contains(a, "=") && searchLongValueFlags[a] {
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if sa.shortFlags(a[1:], args, i) {
				i++
			}
		default:
			sa.operands = append(sa.operands, a)
		}
	}
	return sa
}

// shortFlags reads a short-flag bundle. A value-taking flag either ends the
// bundle with its value attached (-A3) or consumes the next word; either way
// it terminates the scan, matching how the tools themselves read bundles.
// The return reports whether the next word was consumed.
func (sa *searchArgs) shortFlags(bundle string, args []string, i int) bool {
	for j := 0; j < len(bundle); j++ {
		switch c := bundle[j]; c {
		case 'r', 'R':
			sa.recursive = true
		case 'w':
			sa.word = true
		case 'F':
			sa.fixed = true
		case 'i':
			sa.ignoreCase = true
		case 'e':
			if j+1 < len(bundle) {
				sa.patterns = append(sa.patterns, bundle[j+1:])
				return false
			}
			if i+1 < len(args) {
				sa.patterns = append(sa.patterns, args[i+1])
				return true
			}
			return false
		case 'f':
			sa.fromFile = true
			if j+1 < len(bundle) {
				return false
			}
			return i+1 < len(args)
		default:
			if sa.valueShort(c) {
				if j+1 < len(bundle) {
					return false
				}
				return i+1 < len(args)
			}
			// An unknown boolean flag: skip and keep scanning.
		}
	}
	return false
}

func (sa *searchArgs) valueShort(c byte) bool {
	return strings.IndexByte(commonValueShorts, c) >= 0 || strings.IndexByte(sa.spec.valueShorts, c) >= 0
}

// suggestSearch decides whether a search has an honest graph translation and
// composes it. It ROUTES by the pattern's shape, because the right verb
// differs: a diagnostic code and a Buzz op have graph answers that `magus
// query` finds but `magus refs` (compiled-language symbols only) misses -
// measured against real session history, where routing every identifier to
// refs sent MGS2011 and mgs_* greps to a dead end.
//
// It is deliberately a suggestion, not a promise. grep is TEXTUAL and the
// graph is SEMANTIC: they agree only when the pattern names something the
// graph models, and a bare word like "error" matches the identifier shape
// without being one. So the wording hedges - an empty result means the
// pattern was text, and grep was the right tool. A grep-accurate translator
// is not worth building; an honest "try this" is.
func (t *Translator) suggestSearch(sa searchArgs) []Suggestion {
	if sa.fromFile {
		// The pattern lives in a file the translator will not read: unknowable
		// syntactically, so abstain.
		return nil
	}
	pats := sa.pats()
	if len(pats) == 0 || pats[0] == "" {
		return nil
	}
	paths := sa.paths()
	prose := anyMarkdown(paths)
	scope := t.scope(paths)
	if !prose {
		// A non-recursive grep, or one pointed only at files, reads those files
		// rather than asking a repo-wide question - no graph equivalent. The
		// prose case stays: a docsection query replaces scanning the .md itself.
		if !sa.recursive {
			return nil
		}
		// A recursive-by-default tool keeps asking a repo-wide question when a
		// file operand narrows it, so only the grep family abstains here.
		if !sa.spec.recursiveByDefault && len(paths) > 0 && allFiles(paths) {
			return nil
		}
	}
	if prose {
		return []Suggestion{{
			Run:        "magus query kind=" + types.KindDocSection + " " + quoted(pats[0]) + scope,
			Why:        "markdown headings are indexed as doc sections, so the query lands on the passage instead of the whole file",
			Confidence: ConfidenceMedium,
			Hedge:      hedge(hedgeProse, sa),
		}}
	}
	if len(pats) > 1 {
		if sa.fixed {
			// An alternation is a regex, and -F promised there is none; there is
			// no single honest translation of several fixed strings.
			return nil
		}
		return []Suggestion{{
			// One query, joined with |: space-separated matchers AND, and the
			// grammar has no + join.
			Run:        "magus query " + matcherArg("id=~"+strings.Join(pats, "|")) + scope,
			Why:        "one query covers every -e pattern as an id regex alternation",
			Confidence: ConfidenceLow,
			Hedge:      hedge(hedgeQuery, sa),
		}}
	}
	return t.routePattern(pats[0], sa, scope)
}

// routePattern picks the verb by the pattern's shape. See suggestSearch for
// why the routing exists and why every branch hedges.
func (t *Translator) routePattern(pat string, sa searchArgs, scope string) []Suggestion {
	switch {
	case diagnosticCodeRe.MatchString(pat):
		return []Suggestion{{
			Run:        "magus query " + pat + scope,
			Why:        "a diagnostic code has a graph node with its docs",
			Confidence: ConfidenceHigh,
			Hedge:      hedge("If it misses, the code is not one this workspace defines.", sa),
		}}
	case buzzOpRe.MatchString(pat):
		return []Suggestion{{
			Run:        "magus query " + pat + scope,
			Why:        "a Buzz op resolves to the spell functions defining it; refs covers compiled-language symbols only",
			Confidence: ConfidenceHigh,
			Hedge:      hedge(hedgeQuery, sa),
		}}
	case bareIdentRe.MatchString(pat):
		refsConf := ConfidenceMedium
		if sa.word {
			// -w asked for word boundaries: the caller already believes the
			// pattern is a whole symbol, not a substring.
			refsConf = ConfidenceHigh
		}
		return []Suggestion{
			{
				Run:        "magus refs " + pat,
				Why:        "the pattern reads like a code symbol, and refs answers with verified occurrences",
				Confidence: refsConf,
				Hedge:      hedge(hedgeRefs, sa),
			},
			{
				Run:        "magus query " + pat + scope,
				Why:        "a domain entity (project, target, spell, op, diagnostic, doc) is query's side of the graph",
				Confidence: ConfidenceLow,
				Hedge:      hedge(hedgeQuery, sa),
			},
		}
	case hasRegexMeta(pat) && !sa.fixed:
		return []Suggestion{{
			Run:        "magus query " + matcherArg("id=~"+pat) + scope,
			Why:        "the pattern is a regex, and id=~ runs it over node ids",
			Confidence: ConfidenceLow,
			Hedge:      hedge(hedgeQuery, sa),
		}}
	default:
		return []Suggestion{{
			Run:        "magus query " + quoted(pat) + scope,
			Why:        "query matches node ids, labels, and docs",
			Confidence: ConfidenceLow,
			Hedge:      hedge(hedgeQuery, sa),
		}}
	}
}

// suggestFind translates `find <paths> -name <glob>` (also -iname, -path)
// into a file-node query. Everything from -exec on is a payload the caller's
// parser judges separately, never find's own filter.
func (t *Translator) suggestFind(args []string) []Suggestion {
	for i, a := range args {
		if a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir" {
			args = args[:i]
			break
		}
	}
	for _, a := range args {
		switch a {
		case "!", "-not", "-o", "-prune":
			// The filter inverts or branches, so a translated -name would say
			// the opposite of the ask, or only half of it. Abstain.
			return nil
		}
	}
	var paths []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || a == "(" {
			break
		}
		paths = append(paths, a)
	}
	var glob string
	var pathGlob, foldCase bool
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-name", "-iname":
			glob, pathGlob, foldCase = args[i+1], false, args[i] == "-iname"
		case "-path", "-ipath":
			glob, pathGlob, foldCase = args[i+1], true, args[i] == "-ipath"
		}
	}
	if glob == "" {
		return nil
	}
	scope := t.scope(paths)
	if re, ok := globToRe(glob, !pathGlob, foldCase); ok {
		return []Suggestion{{
			Run:        "magus query kind=" + types.KindFile + " " + matcherArg("id=~"+re) + scope,
			Why:        "file nodes are indexed by path, and the glob converts to an id regex",
			Confidence: ConfidenceHigh,
			Hedge:      hedgeFile,
		}}
	}
	// The glob does not convert cleanly; a bare file listing, scoped when the
	// search path maps to a project, is the honest remainder.
	return []Suggestion{{
		Run:        "magus query kind=" + types.KindFile + scope,
		Why:        "the glob has no clean regex form, so list file nodes and narrow from there",
		Confidence: ConfidenceLow,
		Hedge:      hedgeFile,
	}}
}

// fdValueFlags are the fd flags that consume the next word.
var fdValueFlags = map[string]bool{
	"-e": true, "--extension": true, "-E": true, "--exclude": true,
	"-t": true, "--type": true, "-d": true, "--max-depth": true,
	"--min-depth": true,
}

// suggestFd translates an fd invocation. -x/-X payloads are a command fd
// runs, not fd's pattern, so scanning stops there.
func (t *Translator) suggestFd(args []string) []Suggestion {
	var exts, operands []string
	globMode := false
scan:
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-x" || a == "-X" || a == "--exec" || a == "--exec-batch":
			break scan
		case a == "-e" || a == "--extension":
			if i+1 < len(args) {
				i++
				exts = append(exts, args[i])
			}
		case strings.HasPrefix(a, "--extension="):
			exts = append(exts, strings.TrimPrefix(a, "--extension="))
		case a == "-g" || a == "--glob":
			globMode = true
		case strings.HasPrefix(a, "--"):
			if !strings.Contains(a, "=") && fdValueFlags[a] {
				i++
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if fdValueFlags[a] {
				i++
			}
		default:
			operands = append(operands, a)
		}
	}
	if len(exts) > 0 {
		re := `\.` + regexp.QuoteMeta(exts[0]) + `$`
		if len(exts) > 1 {
			quotedExts := make([]string, len(exts))
			for i, e := range exts {
				quotedExts[i] = regexp.QuoteMeta(e)
			}
			re = `\.(` + strings.Join(quotedExts, "|") + `)$`
		}
		matchers := matcherArg("id=~" + re)
		why := "file nodes are indexed by path, and -e is exactly an extension match"
		conf := ConfidenceHigh
		paths := operands
		if len(operands) > 0 && operands[0] != "" {
			// fd's first operand is the PATTERN, not a path: `fd -e go parse`
			// asks for names matching parse AND carrying the extension.
			// Matchers AND, so both are emitted; dropping the pattern listed
			// every file with the extension.
			pat := operands[0]
			if globMode {
				re, ok := globToRe(pat, false, false)
				if !ok {
					return nil
				}
				pat = re
			}
			matchers = matcherArg("id=~"+pat) + " " + matchers
			why = "matchers AND: fd's pattern and its -e extension each become an id regex"
			conf = ConfidenceMedium
			paths = operands[1:]
		}
		return []Suggestion{{
			Run:        "magus query kind=" + types.KindFile + " " + matchers + t.scope(paths),
			Why:        why,
			Confidence: conf,
			Hedge:      hedgeFile,
		}}
	}
	if len(operands) == 0 || operands[0] == "" {
		return nil
	}
	pat, paths := operands[0], operands[1:]
	scope := t.scope(paths)
	if globMode {
		re, ok := globToRe(pat, false, false)
		if !ok {
			return []Suggestion{{
				Run:        "magus query kind=" + types.KindFile + scope,
				Why:        "the glob has no clean regex form, so list file nodes and narrow from there",
				Confidence: ConfidenceLow,
				Hedge:      hedgeFile,
			}}
		}
		return []Suggestion{{
			Run:        "magus query kind=" + types.KindFile + " " + matcherArg("id=~"+re) + scope,
			Why:        "file nodes are indexed by path, and the glob converts to an id regex",
			Confidence: ConfidenceHigh,
			Hedge:      hedgeFile,
		}}
	}
	// fd patterns are regexes already, so the pattern passes through as one.
	return []Suggestion{{
		Run:        "magus query kind=" + types.KindFile + " " + matcherArg("id=~"+pat) + scope,
		Why:        "file nodes are indexed by path, and fd's pattern is already a regex over names",
		Confidence: ConfidenceMedium,
		Hedge:      hedgeFile,
	}}
}

// globToRe converts a SIMPLE glob (only *, ?, literal characters) to a regex
// over file-node ids. Ids are workspace-relative paths while -name matches a
// basename, so the start stays unanchored; the end anchors unless the glob
// ends open. Glob wildcards never cross a path separator, so * and ? map to
// [^/] classes rather than dot. foldCase carries -iname/-ipath as (?i).
// basenameOnly rejects path separators, which -name globs cannot carry. ok is
// false for a glob that does not convert cleanly ([ ] { }), or one so open it
// would match every id.
func globToRe(glob string, basenameOnly, foldCase bool) (string, bool) {
	if glob == "" || strings.ContainsAny(glob, "[]{}") {
		return "", false
	}
	if basenameOnly && strings.ContainsRune(glob, '/') {
		return "", false
	}
	var b strings.Builder
	for _, r := range glob {
		switch r {
		case '*':
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	re := b.String()
	if strings.HasSuffix(re, "[^/]*") {
		re = strings.TrimSuffix(re, "[^/]*")
	} else {
		re += "$"
	}
	re = strings.TrimPrefix(re, "[^/]*")
	// A glob of nothing but wildcards ('?', '?*') survives the trims as bare
	// [^/] classes, and those match every id.
	literals := strings.ReplaceAll(strings.ReplaceAll(re, "[^/]*", ""), "[^/]", "")
	if strings.TrimSuffix(literals, "$") == "" {
		return "", false
	}
	if foldCase {
		re = "(?i)" + re
	}
	return re, true
}

// scope maps a search's directory operands onto a configured project and
// renders the query filter. File operands are skipped: a file names one
// document, not a project's worth of them. Longest prefix wins. Applied to
// query suggestions only - refs takes no filters.
//
// The filter is an anchored regex rather than project=<path>, because query
// matches a project EXACTLY and a node resolves to the LONGEST project owning
// it: with docs and docs/guides/integrations/agents both configured,
// project=docs drops every node under the nested project that the grep it
// replaces WOULD have matched, so the suggestion would be strictly narrower
// than the search it claims to answer.
//
// Operands landing in different projects abstain: one filter cannot carry both,
// and emitting the longest silently drops the rest of the search.
func (t *Translator) scope(paths []string) string {
	var matched []string
	for _, p := range paths {
		if p == "" || fileLooking(p) {
			continue
		}
		c := path.Clean(p)
		best := ""
		for _, proj := range t.projects {
			if (c == proj || strings.HasPrefix(c, proj+"/")) && len(proj) > len(best) {
				best = proj
			}
		}
		if best != "" && !slices.Contains(matched, best) {
			matched = append(matched, best)
		}
	}
	if len(matched) != 1 {
		return ""
	}
	return " " + matcherArg("project=~^"+regexp.QuoteMeta(matched[0])+"(/|$)")
}

// fileLooking reports an operand that names a single file rather than a
// directory: a dotted extension on the last segment, with a trailing slash
// or a dot-path counting as a directory.
func fileLooking(p string) bool {
	if p == "" || p == "." || p == ".." || strings.HasSuffix(p, "/") {
		return false
	}
	base := path.Base(p)
	i := strings.LastIndexByte(base, '.')
	return i > 0 && i < len(base)-1
}

func allFiles(paths []string) bool {
	for _, p := range paths {
		if !fileLooking(p) {
			return false
		}
	}
	return len(paths) > 0
}

func anyMarkdown(paths []string) bool {
	for _, p := range paths {
		if strings.HasSuffix(strings.ToLower(p), ".md") {
			return true
		}
	}
	return false
}

func hasRegexMeta(pat string) bool {
	return strings.ContainsAny(pat, `.*+?[](){}|^$\`)
}

func hedge(base string, sa searchArgs) string {
	if sa.ignoreCase {
		return base + hedgeCase
	}
	return base
}

// Suggestions are paste-ready shell, and patterns reach the formatter with
// quoting already resolved, so embedding is a safety property: a $( ), a
// backtick, or a " re-embedded in double quotes would EXECUTE (or break) on
// paste, a bare backslash is eaten, handing magus a different regex, and a !
// still fires history expansion inside double quotes in the INTERACTIVE shell
// a suggestion is pasted into ("event not found", or a silently substituted
// command). Anything the shell still interprets inside double quotes is
// emitted single-quoted instead, with an embedded single quote spliced through
// the close-escape-reopen sequence singleQuoted writes.
func shellUnsafeInDoubles(s string) bool {
	return strings.ContainsAny(s, "$\\\"`!")
}

func singleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func quoted(pat string) string {
	if shellUnsafeInDoubles(pat) {
		return singleQuoted(pat)
	}
	return `"` + pat + `"`
}

// matcherArg quotes a query matcher only when the shell would split or
// interpret it bare: id=~foo stays as the docs write it, an alternation is
// wrapped.
func matcherArg(m string) string {
	switch {
	case shellUnsafeInDoubles(m):
		return singleQuoted(m)
	case strings.ContainsAny(m, " |(){}[]<>&;!*?'"):
		return `"` + m + `"`
	}
	return m
}
