package spells

// ListProjectsContract is the reserved contract-function name a workspace-provider
// spell exports. magus invokes it by name once per workspace load and folds the
// [ProvidedProject] values it returns into the workspace, so a repo whose projects
// are owned by another tool (nx, gradle, pnpm, cargo) needs no magusfile per
// project.
//
// It is a CONTRACT FUNCTION, not an op, for the same reason the remote cache
// backend's get_artifact/put_artifact are: the work happens in the VM (it shells out
// to the foreign tool and shapes the answer) and it returns data rather than a
// Command for magus to fork. The invoker reaches an exported function of this name
// when no op matches, which is what makes one name enough. Hence Contract, not Op:
// [Op] is this package's type for the other thing.
//
// It is deliberately NOT mgs_-prefixed. An mgs_ function takes no arguments and must
// be pure, because magus calls it before it has selected a target; a provider is
// neither. Its signature matches every other contract function, and the input
// callback yields {root}, the absolute workspace root:
//
//	fun list_projects(target: Target, cb: fun(any)) > [Project]
const ListProjectsContract = "list_projects"

// ProvidedProject is one project a workspace provider supplies: the same facts a
// magusfile author would have written in magus\project({...}), reported by the tool
// that already knows them. Buzz sees it as `object Project` in magus/spell (the Go
// name carries the adjective because types.Project and types.ProjectEntry already
// exist; the authoring surface does not need it).
//
// The fields match that options map one for one, so a workspace has ONE vocabulary
// for configuring a project instead of one for hand-authored projects and another
// for provided ones. Two options are missing: "targets" (per-target
// skip_cache/exclusive/slots) and "watch_ignore" are magus execution POLICY, which
// no foreign tool knows. They stay in the magusfile, layered onto a provided project
// with the central form magus\project("libs/foo", {...}), which runs after the fold.
//
// Spells is a list of NAMES rather than the handle list magus\project takes: a spell
// cannot hold another spell's handle (spells do not import spells), so a provider
// has only the name to give. Every name must resolve to a registered spell.
//
// The record crosses INTO magus as a Buzz object (the generated mirror reads the
// buzz tags and the field names) and is marshalled back out as JSON by the provider
// cache, keyed by the json tags below. Those tags are not decoration: without them
// the cache encoded under Go field names, which musttag could not see and a reader
// could not predict. So a rename here is a wire change in two directions - regenerate
// the mirror, and bump providerCacheVersion so existing entries miss.
type ProvidedProject struct {
	// Path is the project's directory relative to the WORKSPACE ROOT, forward
	// slashes. Required. It must stay inside the root and must not be "." - the root
	// project is the one the magusfile that wired the provider already owns.
	Path string `json:"path"`
	// Name is the human label, for a foreign tool whose project name is not its
	// directory (an nx project named "@acme/ui" rooted at libs/ui). Empty derives
	// one from the path, exactly as a magusfile-declared project does.
	Name string `json:"name"`
	// Spells names the spells to bind, contributing their ops, sources and outputs.
	Spells []string `json:"spells"`
	// DependsOn names upstream projects, resolved exactly as magus\project's
	// "depends_on" is: a bare path is workspace-relative, a dot-relative one is
	// relative to this project.
	//
	// The buzz tag is load-bearing, not cosmetic: this record mirrors the option keys
	// an AUTHOR writes in magus\project({...}), which is snake_case, so the generator's
	// lowerCamel default would silently rename the key the decoder reads. (The
	// host-returned ProjectEntry mirror spells the same concept dependsOn because it
	// mirrors a returned struct, not an authored map.)
	DependsOn []string `buzz:"depends_on" json:"depends_on"`
	// Sources and Outputs are globs relative to THIS PROJECT'S directory, not to the
	// workspace root - the same anchor magus\project's options use, and the anchor
	// baseStep joins against the project path. A provider reporting a foreign tool's
	// workspace-relative globs must re-anchor them first: for a project at libs/foo,
	// "libs/foo/**/*.ts" is wrong and "**/*.ts" is right.
	//
	// The anchor also bounds what is expressible: an output the foreign tool writes
	// OUTSIDE the project directory (nx's dist/{projectRoot} convention) has no
	// project-relative spelling and cannot be declared here.
	Sources []string `json:"sources"`
	Outputs []string `json:"outputs"`
	// Exclusive marks the project as must-not-run-alongside-peers in a batch.
	Exclusive bool `json:"exclusive"`
}
