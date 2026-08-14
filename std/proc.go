//go:build !wasm

package std

//go:generate go run ../cmd/magus-utils bindings -module proc -lang buzz -out ../internal/interp/bindings/gen/proc.go

func init() { Register(Proc) }

// Proc is running OTHER processes, and it is a separate module from os for a
// reason that is structural rather than cosmetic.
//
// magus layers its host methods onto Buzz's stdlib, so one `import "os"` carries both
// surfaces. Buzz owns os.execute, which returns an exit code the caller must remember to
// check; magus's verb raises, captures, streams and enforces the sandbox. Two verbs that
// are synonyms in English, in one namespace, differing on whether a failure is SILENT, is
// a trap.
//
// Renaming inside os would only be safe until upstream picked another name - Buzz is at
// 0.6.0-dev, and the day it ships proc.exec the collision is back. A module magus owns
// cannot collide with a language magus does not control.
//
// What lives here answers "what process is about to run, and under what constraints".
// What stayed in os is the machine itself - platform, CPU count, hostname.
var Proc = Module{
	Name: "proc",
	Doc:  "Run other processes. proc.exec is the one verb that runs anything: it streams output live, captures it, honors the sandbox, and raises on failure instead of handing back a code to check. Needing a shell is not a second verb - proc.shell builds the {bin, args} to hand it, so which shell ran stays visible at the call site instead of hidden inside it. Distinct from Buzz's own os.execute, which returns an exit code and stays silent when you do not read it.",
	Methods: []Method{
		{
			Name: "exec",
			Doc:  "Run cmd directly (no shell; args are never shell-interpolated). Output streams live and is captured. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. Optional dir runs cmd there (relative to the target's cwd). opts.stdin is fed to the process as standard input - pipe by passing a prior call's stdout. opts.quiet captures the output without echoing it to the console. opts.tty runs cmd on a pseudo-terminal so it behaves as it would for a person: tools that check isatty keep their color and progress output instead of the plain form they emit to a pipe. A terminal is a single stream, so stderr arrives merged into stdout and the captured text carries ANSI escapes. Unix only.",
			Args: []Arg{
				{Name: "cmd", Type: TypeString},
				{Name: "args", Type: TypeStringSlice, Optional: true},
				{Name: "dir", Type: TypeString, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    OsExec,
		},
		{
			Name: "shell",
			Doc:  "Build the command line that runs `line` through the platform shell, WITHOUT running it: returns {bin, args} for proc.exec. Default shell is /bin/sh (cmd on Windows); pass shell (e.g. \"bash\") to override, resolved via PATH. This is a pure function, so the argv is inspectable before anything executes - print it, log it, or assert on it. A shell line is written in the platform shell's dialect, so sh and cmd lines are not portable across OSes; for cross-platform logic prefer proc.exec plus the fs/os helpers.",
			Args: []Arg{
				{Name: "line", Type: TypeString},
				{Name: "shell", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ShellCommand"}},
			Impl:    OsShell,
		},
		{
			Name:    "which",
			Doc:     "Resolve cmd against PATH and return its absolute path. RAISES when the command is not found - wrap it in try/catch to check a tool is installed and emit a clear hint instead of a cryptic exec failure.",
			Args:    []Arg{{Name: "cmd", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    OsWhich,
		},
		{
			Name: "with_slots",
			Doc:  "Reserve n slots from magus's concurrency budget for the duration of callback. Use when callback runs a command with its own internal parallelism (make -j, a test runner) that magus can't see, so the global budget is not oversubscribed.",
			Args: []Arg{
				{Name: "n", Type: TypeInt},
				{Name: "callback", Type: TypeFunc},
			},
			Returns: nil,
			Raises:  true,
			Impl:    OsWithSlots,
		},
		{
			Name:    "stdin_is_terminal",
			Doc:     "Report whether standard input is a terminal (TTY) rather than a pipe, file, or /dev/null. Use it to fail fast with a clear message instead of blocking on a read of stdin that will never receive piped input.",
			Args:    nil,
			Returns: []Ret{{Type: TypeBool}},
			Impl:    OsStdinIsTerminal,
		},
	},
}
