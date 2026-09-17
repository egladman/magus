// Package hint is the hints home: everything magus renders to point a reader
// at a better next command. It holds shell-to-graph suggestion translation
// (this file), the canonical MCP tool names and follow-up lines (mcp_tool.go),
// the canonical CLI command paths shown in user-facing output (cli_command.go),
// and the did-you-mean edit distance every "unknown X" site measures with
// (nearest.go). It stays a near-leaf (stdlib plus types), so an emitter
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
	"runtime"
	"slices"
	"strings"
	"sync"

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
// cannot scope. It is not a graph (it never queries one), and the name would
// collide with knowledge.Graph in the callers that hold both.
type Translator struct {
	projects []string
	// variant is the coreutils family this translator assumes is running the commands it
	// grades. Zero value is VariantUnknown, which reads as "do not reason about family";
	// NewTranslator fills it from LocalVariant so the common case needs no option.
	variant Variant
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
	// The host family is resolved once per process by LocalVariant, so every translator
	// starts with the right answer and WithVariant is only for a caller reasoning about a
	// machine other than this one.
	t := &Translator{variant: LocalVariant()}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Variant is the coreutils family this translator assumes will run the commands it grades.
func (t *Translator) Variant() Variant { return t.variant }

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

// searchTools is both the membership set (a name absent from it is not a
// search) and the per-tool facts the parse depends on.
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

// sedSpec is sed's row in the same shape searchTools carries: the short flags that consume
// the NEXT word. -e takes a script and -f takes a script FILE, and reading either as a path
// is what would let a script's own text decide how a caller is graded.
//
// Separate from searchTools rather than an entry in it, because membership of that map is
// the definition of "this is a content search" (IsSearchTool), and sed is a stream editor.
// Sharing the flag-splitting shape without joining the family is the point.
var sedSpec = toolSpec{valueShorts: "ef"}

// Variant names the implementation family behind a POSIX tool name. The same name is
// several different programs, and they disagree in ways that decide what a command does:
// sed's -i suffix, xargs' -r and -J, grep's -P.
//
// Uses: reporting, and choosing which spelling to SUGGEST. Never for relaxing a safety
// decision. Inference reads the flags on one line, so it is evidence rather than a fact
// about the machine, and a rule that trusted it would be trusting a guess about a program
// it cannot see.
type Variant uint8

const (
	// VariantUnknown is the honest default: the line carries nothing family-specific, or
	// the evidence points both ways.
	VariantUnknown Variant = iota
	// VariantGNU is coreutils, the default on Linux.
	VariantGNU
	// VariantBSD covers macOS and the BSDs, which ship the same lineage.
	VariantBSD
)

func (v Variant) String() string {
	switch v {
	case VariantGNU:
		return "gnu"
	case VariantBSD:
		return "bsd"
	default:
		return "unknown"
	}
}

// gnuOnlyFlags are spellings only coreutils accepts. Long options are the strongest
// signal: BSD sed and BSD xargs have none at all, so any `--flag` on one of them is GNU.
var gnuOnlyFlags = map[string][]string{
	"sed":   {"--in-place", "--expression", "--file", "--regexp-extended", "--quiet", "--silent", "--separate", "--null-data"},
	"xargs": {"--no-run-if-empty", "--null", "--delimiter", "--max-args", "--replace", "--arg-file", "-r", "-d"},
	"grep":  {"-P", "--perl-regexp", "--include", "--exclude", "--color"},
	"find":  {"-printf", "-regextype"},
}

// bsdOnlyFlags are spellings only the BSD lineage accepts.
var bsdOnlyFlags = map[string][]string{
	"xargs": {"-J", "-L", "-o"},
	"find":  {"-x", "-s"},
}

// LocalVariant is the coreutils family on THIS host, derived once.
//
// Derived from GOOS rather than probed: a probe means a process per tool, and the answer is
// a property of the machine, not of any command, so paying for it on a hook that runs
// before every tool call would be paying repeatedly for a constant. A caller that wants a
// sharper answer (Alpine ships busybox, which is neither family) can override it with
// [WithVariant] from a spell's version probe, which already runs once per target.
//
// Session-scoped by construction: sync.Once, so the whole process shares one answer and
// nothing recomputes it per line.
func LocalVariant() Variant {
	localVariantOnce.Do(func() {
		switch runtime.GOOS {
		case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
			localVariant = VariantBSD
		case "linux":
			// The common case, and wrong on busybox. A probe is what settles that, and
			// the caller who cares is the one who should pay for it.
			localVariant = VariantGNU
		default:
			localVariant = VariantUnknown
		}
	})
	return localVariant
}

var (
	localVariantOnce sync.Once
	localVariant     Variant
)

// WithVariant overrides the host family a translator assumes, for a caller that probed it
// or is reasoning about another machine.
func WithVariant(v Variant) Option {
	return func(t *Translator) { t.variant = v }
}

// PortabilityGap reports a command written for one coreutils family about to run on the
// other, naming both. Empty when the line carries no family-specific spelling, when the
// families agree, or when either side is unknown.
//
// This is what the local variant buys that flag inference alone cannot: `sed --in-place`
// is correct prose on Linux and simply fails on macOS, and the failure arrives as an
// unrecognized-flag error with nothing saying why. Naming it is context, never a refusal:
// the command may be headed for a container, and a guard that blocked it would be grading
// a machine it cannot see.
func PortabilityGap(cmd Invocation, host Variant) string {
	wrote := InferVariant(cmd)
	if wrote == VariantUnknown || host == VariantUnknown || wrote == host {
		return ""
	}
	return path.Base(cmd.Name) + " is spelled for " + wrote.String() + " and this host is " + host.String()
}

// InferVariant reads a command's flags and reports which family it was SPELLED for, or
// VariantUnknown when nothing on the line decides it.
//
// Distinct from [LocalVariant], and the pair is the point: one says what the author
// assumed, the other what will actually run it, and a disagreement is a portability bug
// worth naming (see [PortabilityGap]). Neither may relax a safety decision on its own.
func InferVariant(cmd Invocation) Variant {
	name := path.Base(cmd.Name)
	gnu := matchesAny(cmd.Args, gnuOnlyFlags[name])
	bsd := matchesAny(cmd.Args, bsdOnlyFlags[name])
	switch {
	case gnu && !bsd:
		return VariantGNU
	case bsd && !gnu:
		return VariantBSD
	default:
		// Both, or neither. A line carrying evidence for both families is one nobody's
		// tool would run, and saying "unknown" is truer than picking a winner.
		return VariantUnknown
	}
}

// matchesAny reports whether any arg is one of flags, or carries it as a --flag=value.
func matchesAny(args, flags []string) bool {
	for _, a := range args {
		for _, f := range flags {
			if a == f || (strings.HasPrefix(f, "--") && strings.HasPrefix(a, f+"=")) {
				return true
			}
		}
	}
	return false
}

// inPlaceSuffixIsUnambiguous reports whether sed's in-place flag says, by itself, whether a
// backup suffix was given.
//
// Unambiguous: a short cluster with characters packed after the `i` (`-i.bak`), and the
// long form in either spelling, since only GNU accepts a long option and GNU's -i never
// consumes a separate word. Ambiguous: any cluster ending at `i` (`-i`, `-ni`, `-ei`),
// where BSD takes the next word as the suffix and GNU takes it as the first file.
func inPlaceSuffixIsUnambiguous(args []string) bool {
	for _, a := range args {
		if a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
			return true
		}
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		cluster := a[1:]
		idx := strings.IndexByte(cluster, 'i')
		if idx < 0 {
			continue
		}
		// Characters after the i are the packed suffix. Nothing after it means the
		// suffix, if any, is the next word, and which it is depends on the family.
		if idx == len(cluster)-1 {
			return false
		}
	}
	return true
}

// SedFiles returns the file operands of a sed invocation, and whether the set is one a
// reader can see in full.
//
// bounded is false when an operand carries a glob or substitution metacharacter, or when
// sed was given no file at all and reads a stream instead. Both mean the files edited are
// not knowable from the line, which is the distinction a caller grading a rewrite needs:
// a sed naming its files is a targeted edit, a sed naming a traversal is a blind one.
//
// Exported because the guard asks the question and this package owns the parsing. A second
// flag-splitter in the guard is exactly the drift this file's per-tool tables exist to
// prevent, and it had already happened once.
func SedFiles(args []string) (files []string, bounded bool) {
	// An in-place flag carrying no packed suffix cannot be split reliably, so it is never
	// bounded. BSD sed reads the next word as the backup SUFFIX and GNU sed reads it as the
	// first FILE, which is the portability trap this tool is best known for; guessing
	// either way gets the operand list wrong on half the machines that run it.
	if !inPlaceSuffixIsUnambiguous(args) {
		return nil, false
	}
	sa := parseSearch(sedSpec, args)
	ops := sa.operands
	// The first bare word is the script, unless -e or -f already supplied one. BOTH have
	// to be checked: -e fills patterns, while -f names a script FILE and sets fromFile
	// with patterns left empty, so testing patterns alone dropped a real file from the
	// list and reported a bounded edit as unbounded.
	if len(sa.patterns) == 0 && !sa.fromFile && len(ops) > 0 {
		ops = ops[1:]
	}
	if len(ops) == 0 {
		return nil, false
	}
	for _, op := range ops {
		// An EMPTY operand is what a command substitution leaves behind: the shell parser
		// renders `$(git ls-files)` as a word with no literal text, so the operand survives
		// the split but names nothing. It is never a real filename, and reading it as one
		// let a run-time file list (the least knowable operand set there is) pass as a
		// bounded edit.
		if op == "" || strings.ContainsAny(op, "*?[]{}$`") {
			return ops, false
		}
	}
	return ops, true
}

// xargsSpec is xargs' row: the short flags taking the next word. -I names a replace string,
// -n a max-args count, -P a parallelism, -d/-E/-s their own values. Everything after those
// is the COMMAND xargs runs, which is the part a caller needs.
var xargsSpec = toolSpec{valueShorts: "IndPsE"}

// DrivenCommand returns the command a driver tool would run over a file list it produced,
// and whether one was found.
//
// The two drivers are xargs, whose operands after its own flags ARE the command, and find,
// whose command follows -exec or -execdir. Both hand a downstream tool a set of paths that
// appears nowhere on the line, so a caller grading what that tool will do needs the tool's
// own name and argv rather than the driver's.
//
// This is the parser that was missing. The guard had been asking the question with a regex
// over the whole line, which cannot tell a driven rewrite from a line that merely mentions
// find, and cannot report WHICH command is about to be driven.
func DrivenCommand(cmd Invocation) (Invocation, bool) {
	switch path.Base(cmd.Name) {
	case "xargs":
		// Split the RAW args at the first bare word rather than reading parseSearch's
		// operands. Everything from there is the child's own argv, and flag-parsing it
		// as xargs' would strip the child's flags: `xargs sed -i ...` came back as sed
		// with no -i, so a driven in-place rewrite read as a harmless one.
		for i := 0; i < len(cmd.Args); i++ {
			a := cmd.Args[i]
			if !strings.HasPrefix(a, "-") {
				return Invocation{Name: a, Args: cmd.Args[i+1:]}, true
			}
			// xargs' own value-taking shorts consume the next word; a long flag
			// spelled --max-args=3 carries its value inline and consumes nothing.
			if len(a) == 2 && strings.ContainsRune(xargsSpec.valueShorts, rune(a[1])) && i+1 < len(cmd.Args) {
				i++
			}
		}
		// Bare xargs runs echo, which writes nothing.
		return Invocation{}, false
	case "find":
		for i, a := range cmd.Args {
			if a != "-exec" && a != "-execdir" && a != "-ok" && a != "-okdir" {
				continue
			}
			rest := cmd.Args[i+1:]
			if len(rest) == 0 {
				return Invocation{}, false
			}
			// The command runs until the terminator find requires; anything past it is
			// another predicate, not this command's argv.
			end := len(rest)
			for j, r := range rest {
				if r == ";" || r == "+" || r == `\;` {
					end = j
					break
				}
			}
			return Invocation{Name: rest[0], Args: rest[1:end]}, true
		}
	}
	return Invocation{}, false
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
// without being one. So the wording hedges: an empty result means the
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
		// rather than asking a repo-wide question, so no GRAPH verb answers it. The
		// prose case stays: a docsection query replaces scanning the .md itself.
		//
		// `refs --text` does answer it, and takes the same path operands, so these
		// two branches hand that back instead of abstaining. They are the shapes the
		// translator was silent on for as long as magus had no raw-text search, and
		// silence here is what sent a reader back to grep with nothing to try.
		if !sa.recursive {
			return textOnly(pats[0], sa)
		}
		// A recursive-by-default tool keeps asking a repo-wide question when a
		// file operand narrows it, so only the grep family lands here.
		if !sa.spec.recursiveByDefault && len(paths) > 0 && allFiles(paths) {
			return textOnly(pats[0], sa)
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

// textOnly wraps textSuggestion for the branches whose only honest answer it is, so a
// pattern it abstains on (a regex) still yields no suggestion rather than a wrong one.
func textOnly(pat string, sa searchArgs) []Suggestion {
	if s, ok := textSuggestion(pat, sa); ok {
		return []Suggestion{s}
	}
	return nil
}

// textSuggestion is the literal-for-literal translation: `refs --text` is a substring
// search over the workspace's source files with grep's own exit codes, and it takes the
// same trailing path operands, so the caller's scope carries over unchanged.
//
// It is what makes a raw-text search answerable AT ALL. Every other suggestion in this
// file translates a text match into a semantic question and is honest that the two agree
// only when the pattern is a real symbol; this one asks the same question grep asked.
//
// It abstains for a REGEX, which is the one shape it cannot honor: `refs --text` matches
// a substring, so offering it for `Handle.*Foo` would hand back a command that quietly
// answers something narrower. `-F` settles that in the caller's favor whatever the
// pattern looks like, since it promised there is no regex in it.
func textSuggestion(pat string, sa searchArgs) (Suggestion, bool) {
	if pat == "" || (!sa.fixed && hasRegexMeta(pat)) {
		return Suggestion{}, false
	}
	run := "magus refs --text " + quoted(pat)
	for _, p := range sa.paths() {
		// `.` is the whole workspace, which is what no scope already means. Carrying
		// it through would spell the default out as an argument on the commonest
		// grep there is (`grep -rn Foo .`).
		if p == "." || p == "./" {
			continue
		}
		run += " " + p
	}
	hedge := "Case-sensitive and literal; generated files are searched and counted separately."
	if sa.ignoreCase {
		hedge = "Case-SENSITIVE, unlike the -i you asked for, and literal."
	}
	return Suggestion{
		Run:        run,
		Why:        "the same literal search, over the workspace's source files, with grep's exit codes",
		Confidence: ConfidenceHigh,
		Hedge:      hedge,
	}, true
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
		out := []Suggestion{
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
		// Last, because both graph verbs answer a better question when the pattern really
		// is a symbol. It is here so the hedge above ("grep is right") names a magus
		// command rather than sending the reader out of the workspace to act on it.
		if s, ok := textSuggestion(pat, sa); ok {
			out = append(out, s)
		}
		return out
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
// query suggestions only; refs takes no filters.
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
