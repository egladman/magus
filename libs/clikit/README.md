# clikit

A command tree declared as data, and the files rendered from it. A program
declares its commands once as a `command.Command` value; generators read that
value to write its flag binders, man pages, completions and API lock, so none of
them is a second copy that can drift.

| Package            | Holds                                                                       |
| ------------------ | --------------------------------------------------------------------------- |
| `command`          | `CommandSpec`, `Command`, `Flag`, `Kind`, `Operand`, `Example`, `ExitCode`, `File`, `Walk`, `Main`, `Execute`, `Invoke`, `Invocation`, `WithVersion` |
| `configuration`    | `ConfigurationSpec`, `Setting`, `FlagNames`: every config key, environment variable and flag a program reads |
| `generate`         | `Binders`, `Roff`, `Markdown`, `Completion`, `Lock`, `FromStruct`, `SettingsTable`, `ApplyEnv` |
| `exit`             | `OK`, `Failure`, `Usage`, `TempFail`, `Usagef`, `Reported`, `Code`, `AlreadyReported` |
| `output`           | `Format`, `Parse`, `Join`, `Writer`, `Codec`, `Fields`: the `-o` value and its json, yaml, jsonl and template renderers |

The root `Command` names the program: its `Flags` are the global flags and its
`Files` fill the root man page. A flag's `Doc` may quote one word in
backquotes to name its value in the man page, as `flag.UnquoteUsage` reads it.

The same `Command` value runs the program. `command.Main(root)` parses the command
line, dispatches to the child it names, prints help from the declaration, and exits
through `exit.Code`. Each `Flag` stores into its `Target` and each `Command` runs its
`Run`; `Execute` and `Invoke` are the same without the process exit, for tests.
A Go main package builds its command this way, and the `clikitmain` analyzer in
`libs/conventions` reports one that parses flags or `os.Args` by hand.

A `CommandSpec` and a `ConfigurationSpec` are the specs of `Command` and
`Configuration` records in `clikit.command/v1alpha1` and
`clikit.configuration/v1alpha1`, read with `libs/apiversion`. `FromStruct` lists a
config struct's settings, the program adds the variables no key carries, and
`Roff` and `Markdown` print every variable in ENVIRONMENT.

A declaration that cannot be rendered fails loudly: a flag with an unknown `Kind`,
or a `Default` of a different Go type than its `Kind` binds, is an error rather
than documentation of a flag the program does not have.
