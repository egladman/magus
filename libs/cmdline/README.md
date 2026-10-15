# cmdline

A command tree declared as data, and the files rendered from it. A program
declares its commands once as a `cmdline.Command` value; generators read that
value to write its flag binders, man pages, completions and API lock, so none of
them is a second copy that can drift.

| Package             | Holds                                                                       |
| ------------------- | --------------------------------------------------------------------------- |
| `cmdline`           | `Command`, `Flag`, `Kind`, `Operand`, `Example`, `ExitCode`, `Env`, `File`, `Walk` |
| `cmdline/generate`  | `Binders`, `Roff`, `Markdown`, `Completion`, `Lock`                         |
| `cmdline/output`    | `Format`, `Parse`, `Join`, `Writer`, `Codec`, `Fields`: the `-o` value and its json, yaml, jsonl and template renderers |

The root `Command` names the program: its `Flags` are the global flags and its
`Env` and `Files` fill the root man page. A flag's `Doc` may quote one word in
backquotes to name its value in the man page, as `flag.UnquoteUsage` reads it.

A declaration that cannot be rendered fails loudly: a flag with an unknown `Kind`,
or a `Default` of a different Go type than its `Kind` binds, panics rather than
documenting a flag the program does not have.
