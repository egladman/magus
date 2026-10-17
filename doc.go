// Package magus is the high-level library for the magus build orchestrator.
//
// Entry points: [Open] returns a [Magus] for build/test cycles, [Inspect] for
// read-only commands. A [Magus] runs work via [Magus.Run] (one target),
// [Magus.RunCI] (the configured CI pipeline), and [Magus.RunAffected]
// (only projects touched since a baseline). Behavior is tuned with [Option]
// values passed to [Open]/[Inspect] (e.g. [WithLimiter]). [Limiter] caps
// concurrent spell executions and can be shared across server workspaces.
//
// Boundary: the library links the engine-agnostic interp package and the Buzz VM,
// but deliberately not the host bindings or the Buzz engine backend. A program
// that evaluates magusfiles imports _ "github.com/egladman/magus/buzzengine";
// without it, [Open] and [Inspect] on a workspace with a magusfile return a
// [types.EngineNotLinkedError]. A script-driven backend (e.g. the spell-backed
// remote backend) reaches the library only through registered hooks such as
// [cache.RegisterRemoteBackendOpener], never a direct import.
package magus
