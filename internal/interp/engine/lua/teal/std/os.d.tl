-- Type declarations for magus.extra.os (process execution).
-- os.exec runs a command directly (no shell; args are never shell-interpolated).
-- os.exec_sh runs a line through the shell — for pipes, redirection, and globs.
-- Both stream output live and return an ExecResult.

-- Every primitive runs in the current working directory — the target's project
-- dir during dispatch. Pass the optional dir to run in a nested subdirectory
-- (relative to that cwd); omit it in the common case.

global record ExecResult
   stdout: string
   stderr: string
   code: integer
   ok: boolean
end

-- Options for exec/exec_sh. allow_failure defaults to false (a non-zero exit
-- raises); set allow_failure = true to get the ExecResult back instead. shell
-- (exec_sh only) overrides the default /bin/sh — e.g. "bash" — resolved via PATH.
global record ExecOpts
   allow_failure: boolean
   shell: string
end

global record Os
   -- Run cmd directly (no shell). Output streams live and is captured. Raises on
   -- a non-zero exit unless opts.allow_failure is true.
   exec:     function(cmd: string, args?: {string}, dir?: string, opts?: ExecOpts): ExecResult
   -- Run line through the shell (/bin/sh -c; cmd /c on Windows). Same result and
   -- raise semantics as exec.
   exec_sh:  function(line: string, dir?: string, opts?: ExecOpts): ExecResult
   -- Set env vars for the duration of callback; subprocesses see the merged env.
   with_env: function(env: {string:string}, fn: function())
   -- Reserve n slots from magus's concurrency budget for the duration of fn —
   -- use when fn runs a command with its own parallelism (make -j) magus can't see.
   with_slots: function(n: integer, fn: function())
   -- Docker/OCI platform triple for this host: (os, arch, variant).
   -- Examples: ("linux","amd64",""), ("linux","arm64","v8"), ("linux","arm","v7")
   -- Variant is "" for non-ARM architectures.
   platform: function(): string, string, string
   -- Abort the current run with the given exit code (typically after logging an
   -- error). Does not call os.Exit; the code becomes magus's process exit status.
   exit: function(code: integer)
   -- Pause for milliseconds (fractional allowed), matching Buzz's os.sleep;
   -- cancellable if the run is interrupted.
   sleep: function(ms: number)
   -- Resolve cmd against PATH: returns its absolute path, or "" if not found.
   which: function(cmd: string): string
end
