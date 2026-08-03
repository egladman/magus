package manpage

const (
	MainDescription = `magus is a standalone build orchestrator and content-addressed cache for
multi-language monorepos, and an evolution of Mage. It provides workspace-aware
subcommands for building, testing, linting, and inspecting projects without
requiring Mage to be installed.

magus reads optional configuration from magus.yaml (XDG, workspace root, or
CWD) and MAGUS_* environment variables. All configuration can be overridden
with CLI flags.`

	GlobalFlagsIntro = `Global flags are accepted by every subcommand and may appear before or
after the subcommand word. Last-write-wins, matching kubectl conventions.`

	FlagRoot        = `Workspace root. Default: walk up from cwd until go.mod is found. Must precede the subcommand.`
	FlagConfig      = `Config file path. Default: search for magus.yaml in CWD, workspace root, and $XDG_CONFIG_HOME/magus/. Must precede the subcommand.`
	FlagOutput      = `Output format: text (default), json, yaml, name, jsonl, or template[=<go-template>]. Honored by subcommands that emit structured data. A template body renders a Go text/template over the same value -o json emits (field names are the json keys); a bare -o template with no body lists that output's fields instead of rendering - the json keys usable in -o json and -o template, with each field's type and doc.`
	FlagConcurrency = `Maximum number of concurrent build steps. 0 means use the configured value (or MAGUS_CONCURRENCY, or min(NumCPU,8)).`
	FlagVerbose     = `Increase log verbosity. Repeat for more detail (-v, -vv, -vvv).`

	FilesConfig = `Configuration file. Searched in CWD, workspace root, and
$XDG_CONFIG_HOME/magus/ in ascending priority order. Both plain and
dot-prefixed names are accepted; having both in the same directory is an error.`
	FilesCache = `Content-addressed build cache in the workspace root. Override with
MAGUS_CACHE_DIR.`
)
