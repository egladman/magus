# Rewriting shell as Buzz

{{if .Full}}A shell script ported line by line into Buzz is worse than the shell it replaced: every
`curl`, `jq` and `sed` becomes a `proc\exec` call with a hand-built argument list, the
temp files and traps survive only to feed those tools, and the result still parses text.
Port what the script DOES instead. Name its inputs and outputs, decide whether it should
be a script at all, then write each step with the host module that already does it.
`proc\exec` is left for the one real external program the script exists to run.

This skill teaches the rewrite. {{skill "buzz-lang"}} teaches the syntax; load it first
if you have not written Buzz in this session.{{else}}Port what script DOES, never its lines.
Line-by-line port: every `curl`/`jq`/`sed` becomes `proc\exec`, temp files and traps survive only to feed them, result still parses text.
`proc\exec` only for real external program script exists to run.
Syntax: {{skill "buzz-lang"}}; load it first.{{end}}

## The rewrite, in order

{{if .Full}}1. Write down what the script reads, what it writes, and what it prints. Ignore how.
2. Decide where the result belongs: a target, a `{{cmd "buzz"}}` script, or still shell.
   The next section decides it.
3. Map each external tool to its module with the idiom table below. A tool with no row is
   usually a list or string method.
4. Delete the scaffolding the tools needed: temp files that passed data between them,
   traps that cleaned those files, `[ -f ]` guards before a read that raises anyway,
   `die` helpers, `set -euo pipefail`.
5. Parse arguments with `flags\parse`, and put each step in a named function.
6. Add a `test` block for each pure function, then `{{cmd "buzz"}} --check`,
   `{{cmd "buzz"}} -t`, and a real run.
7. Prove the port: run the old script and the new one on the same input and diff what
   each wrote. A port is done when the outputs match, not when it checks.{{else}}1. List what script reads, writes, prints. Ignore how.
2. Decide: target, `{{cmd "buzz"}}` script, or still shell (next section).
3. Map each tool -> module (idiom table). Tool with no row: usually list or string method.
4. Delete scaffolding tools needed: temp files between them, traps cleaning them, `[ -f ]` before read that raises anyway, `die` helpers, `set -euo pipefail`.
5. `flags\parse` for arguments; each step named function.
6. `test` block per pure function -> `{{cmd "buzz"}} --check` -> `{{cmd "buzz"}} -t` -> real run.
7. Prove port: run old and new on same input, diff what each wrote. Done when outputs match, not when it checks.{{end}}

## Target, script, or still shell

{{if .Full}}Ask two questions. Does running it twice on the same inputs write the same files?
Does other work read what it writes? Two yeses make it a target. Then magus caches it,
sandboxes it, reruns it only when an input changed, and orders it with `ctx.needs`.

| the script | becomes |
| --- | --- |
| generates files a build, test or doc reads | a target that declares them with `ctx.writesFiles` |
| verifies or gates (`--check`, lint, drift) | a target that `ci` needs |
| runs one project's toolchain (a test runner, a bundler) | a target, or a spell op when every project of that type needs it |
| acts on the machine or repository state: worktrees, branches, a download on demand, a report | a `{{cmd "buzz"}}` script with `flags\parse` |
| runs where magus is absent: inside a container image, before magus is installed | still shell, kept small |
| exists to exercise the shell itself: completion checks, a terminal recording | still shell |

A script that writes a generated file is the most common miss. As a script it reruns from
scratch, and nothing notices when the file goes stale.{{else}}Two questions: same inputs twice -> same files written? Other work reads what it writes? Both yes -> target (cached, sandboxed, affected-tracked, ordered by `ctx.needs`).
- generates files build/test/doc reads: target, declare with `ctx.writesFiles`
- verifies or gates (`--check`, lint, drift): target `ci` needs
- runs one project's toolchain: target, or spell op when every project of that type needs it
- acts on machine or repo state (worktrees, branches, download on demand, report): `{{cmd "buzz"}}` script + `flags\parse`
- runs where magus absent (container image, before install): still shell, kept small
- exercises shell itself (completion checks, terminal recording): still shell
Most common miss: script writing generated file. Reruns from scratch; nothing notices when stale.{{end}}

```buzz
import "magus";
import "proc";

export fun generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles(["gen/api.md"]);
    proc\exec("node", args: ["tools/gen-api.mjs", "--write", "."]);
}
```

{{if .Full}}Here `proc\exec` is right: the generator IS the external program. Everything around it
(checking the output, writing a file, diffing) is a module call.{{else}}`proc\exec` right here: generator IS external program. Everything around it: module calls.{{end}}

## Idiom table

{{if .Full}}`{{cmd "describe"}} module <name>` prints every member with its signature. Check it before
assuming a row's call takes the arguments you expect.

| shell | Buzz |
| --- | --- |
| `set -euo pipefail`, `\|\| exit 1` | nothing: a host call raises; declare `!> any` |
| `echo "msg" >&2; exit 1` | `throw "msg";` |
| `trap 'rm -rf "$TMP"' EXIT` | usually nothing: keep the data in memory |
| `curl -fsSL --retry 5 URL` | `http\get(url, opts: http\Options{ fail = true }, retry: http\Retry{ attempts = 5 })` |
| `curl -o file URL` | `http\download(url, dest: file)` |
| `jq`, `yq` | `json\parse`, `yaml\parse`, then index; `json\stringify` |
| `grep PAT file`, `grep -c` | `fs\readLines(file).filter(...)`, `.len()` |
| `sed 's/a/b/g'` | `text.replace("a", with: "b")` |
| `cut -d: -f2`, `awk '{print $2}'` | `line.split(":")[1]`, `strings\fields(line)[1]` |
| `wc -l`, `sort`, `uniq`, `head -n` | `.len()`, `.sort(fn)`, a map as a set, `.sub(0, len: n)` |
| `KEY=value` file | `env\parseDotenv(fs\readFile(file))` |
| `sha256sum`, `shasum -a 256` | `crypto\sha256File(path)` |
| `tar xzf a.tgz --strip-components=1 -C d` | `archive\uncompress(src, dest: d, opts: archive\Options{ strip = 1 })` |
| `tar czf`, `zip` | `archive\compress(src, dest: "a.tar.gz")` |
| `find . -name '*.md'`, `dir/*/` | `fs\glob("**/*.md")`; `fs\walk` to prune while walking |
| `[ -f ]`, `[ -d ]`, `[ -e ]` | `fs\isFile`, `fs\isDir`, `fs\exists`, often not needed |
| `command -v tool` | `proc\which(tool)`, which raises when absent |
| `mkdir -p`, `rm -rf`, `cp -R`, `mv` | `fs\mkdirAll`, `fs\removeAll`, `fs\copyDir`, `fs\rename` |
| `cat > f <<EOF` | `fs\writeFile`, or `template\render` for a generated file |
| `${VAR:?}`, `${VAR:-x}` | `env\require("VAR")`, `env\getOr("VAR", def: "x")` |
| `VAR=x cmd` | `os\withEnv({"VAR": "x"}, callback: ...)` |
| `"${1:?usage}"`, `getopts` | `flags\parse(args, switches: [...], valued: [...])` |
| `until cmd; do sleep 2; done` | `os\retry(5, fn: ...)`, or `http\Retry` for HTTP |
| `git ...` | `vcs\status`, `vcs\ref`, `vcs\commit`, `vcs\history`, `vcs\changedFiles`; `vcs\cmd` for the rest |
| `( cd d && tool )` | `proc\exec("tool", args: [...], dir: d)` |
| `a \| b` across two real tools | `proc\exec("b", opts: proc\ExecOptions{ stdin = first.stdout })` |
| `magus ... -o json \| jq` | `magus\describe.<noun>`, `magus\query`, `magus\refs`: typed records |
| `magus run t p` | `ctx.needs(t)` inside a target; `magus\run([...])` in a script |{{else}}Signatures: `{{cmd "describe"}} module <name>`. Check before assuming arguments.
shell: Buzz
`set -euo pipefail`, `|| exit 1`: nothing; host call raises; declare `!> any`
`echo "msg" >&2; exit 1`: `throw "msg";`
`trap 'rm -rf "$TMP"' EXIT`: usually nothing; keep data in memory
`curl -fsSL --retry 5 URL`: `http\get(url, opts: http\Options{ fail = true }, retry: http\Retry{ attempts = 5 })`
`curl -o file URL`: `http\download(url, dest: file)`
`jq`, `yq`: `json\parse`, `yaml\parse`, then index; `json\stringify`
`grep PAT file`, `grep -c`: `fs\readLines(file).filter(...)`, `.len()`
`sed 's/a/b/g'`: `text.replace("a", with: "b")`
`cut -d: -f2`, `awk '{print $2}'`: `line.split(":")[1]`, `strings\fields(line)[1]`
`wc -l`, `sort`, `uniq`, `head -n`: `.len()`, `.sort(fn)`, map as set, `.sub(0, len: n)`
`KEY=value` file: `env\parseDotenv(fs\readFile(file))`
`sha256sum`, `shasum -a 256`: `crypto\sha256File(path)`
`tar xzf a.tgz --strip-components=1 -C d`: `archive\uncompress(src, dest: d, opts: archive\Options{ strip = 1 })`
`tar czf`, `zip`: `archive\compress(src, dest: "a.tar.gz")`
`find . -name '*.md'`, `dir/*/`: `fs\glob("**/*.md")`; `fs\walk` to prune while walking
`[ -f ]`, `[ -d ]`, `[ -e ]`: `fs\isFile`, `fs\isDir`, `fs\exists`; often not needed
`command -v tool`: `proc\which(tool)`, raises when absent
`mkdir -p`, `rm -rf`, `cp -R`, `mv`: `fs\mkdirAll`, `fs\removeAll`, `fs\copyDir`, `fs\rename`
`cat > f <<EOF`: `fs\writeFile`, or `template\render` for generated file
`${VAR:?}`, `${VAR:-x}`: `env\require("VAR")`, `env\getOr("VAR", def: "x")`
`VAR=x cmd`: `os\withEnv({"VAR": "x"}, callback: ...)`
`"${1:?usage}"`, `getopts`: `flags\parse(args, switches: [...], valued: [...])`
`until cmd; do sleep 2; done`: `os\retry(5, fn: ...)`, or `http\Retry` for HTTP
`git ...`: `vcs\status`, `vcs\ref`, `vcs\commit`, `vcs\history`, `vcs\changedFiles`; `vcs\cmd` for rest
`( cd d && tool )`: `proc\exec("tool", args: [...], dir: d)`
`a | b` across two real tools: `proc\exec("b", opts: proc\ExecOptions{ stdin = first.stdout })`
`magus ... -o json | jq`: `magus\describe.<noun>`, `magus\query`, `magus\refs`: typed records
`magus run t p`: `ctx.needs(t)` inside target; `magus\run([...])` in script{{end}}

## Pairs

{{if .Full}}Each pair is the shell as it is usually written, then the Buzz it becomes. Every Buzz
block checks as written.{{else}}Shell as usually written, then Buzz. Every Buzz block checks.{{end}}

### Failure and exit codes

```sh
set -euo pipefail
[ -f "$1" ] || { echo "pin: $1 missing" >&2; exit 1; }
pinned=$(cut -d' ' -f1 "$1")
```

```buzz
import "std";
import "fs";

fun main(args: [str]) > void !> any {
    if (args.len() != 1) {
        throw "usage: pin <file>";
    }
    final pinned = fs\readFile(args[0]).split(" ")[0];
    std\print(pinned);
}
```

{{if .Full}}`fs\readFile` raises with the path in its message, so the `[ -f ]` guard has nothing left
to add. `throw` ends the run non-zero with the message on stderr. Check a condition only
when the message you would write says more than the raised one.{{else}}`fs\readFile` raises naming path; `[ -f ]` guard adds nothing. `throw` ends run non-zero, message on stderr. Check condition only when your message says more than raised one.{{end}}

### HTTP, JSON and temp files

```sh
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
curl -fsSL --retry 5 --retry-all-errors --retry-delay 2 "$API&offset=$o" -o "$TMP/page.json"
jq -e '(.rows | length) == 100' "$TMP/page.json" >/dev/null || exit 1
```

```buzz
import "http";
import "encoding/json";

fun fetchPage(api: str, offset: int) > [any] !> any {
    final res = http\get("{api}&offset={offset}&length=100",
        opts: http\Options{ fail = true },
        retry: http\Retry{ attempts = 5, delayMs = 2000.0, allErrors = true });
    final rows: [any] = json\parse(res.body)["rows"];
    if (rows.len() != 100) {
        throw "page at offset {offset} returned {rows.len()} rows, not 100";
    }
    return rows;
}
```

{{if .Full}}The temp directory and its trap existed because `jq` reads files. With the body in memory
both go. Without a `retry` argument a request runs exactly once. When a real directory is
needed, remove it on both paths:{{else}}Temp dir and trap existed because `jq` reads files; body in memory -> both go. No `retry` argument: request runs once. Real directory needed -> remove on both paths:{{end}}

```buzz
import "fs";
import "archive";

fun unpackInto(tarball: str, dest: str) > void !> any {
    final work = fs\tempDir("unpack-");
    try {
        archive\uncompress(tarball, dest: work, opts: archive\Options{ strip = 1 });
        fs\copyDir(fs\join(work, name: "bin"), dst: dest);
    } catch (err: any) {
        fs\removeAll(work);
        throw err;
    }
    fs\removeAll(work);
}
```

### Digests, counts and key=value files

```sh
rows=$(wc -l <"$OUT" | tr -d ' ')
sum=$(sha256sum "$OUT" 2>/dev/null | cut -d' ' -f1 || shasum -a 256 "$OUT" | cut -d' ' -f1)
SHA="$(grep '^upstream_sha=' versions.lock | cut -d= -f2)"
```

```buzz
import "std";
import "fs";
import "crypto";
import "env";

fun main(args: [str]) > void !> any {
    final rows = fs\readLines("verified.jsonl").len();
    final sum = crypto\sha256File("verified.jsonl");
    final sha = env\parseDotenv(fs\readFile("versions.lock"))["upstream_sha"];
    if (sha == null) {
        throw "versions.lock has no upstream_sha";
    }
    std\print("{rows} rows, sha256 {sum}, upstream {sha!}");
}
```

{{if .Full}}The `sha256sum`-or-`shasum` fallback is a portability workaround for a tool, and the
module needs none.{{else}}`sha256sum`-or-`shasum` fallback: portability workaround for tool; module needs none.{{end}}

### Globs, find and directory loops

```sh
for appdir in "$REPO"/apps/*/; do
    app="$(basename "$appdir")"
done
find . -name '*.md' -not -path './node_modules/*'
```

```buzz
import "std";
import "fs";
import "path";

fun main(args: [str]) > void !> any {
    foreach (p in fs\glob("apps/*")) {
        if (fs\isDir(p.value)) {
            std\print(fs\basename(p.value));
        }
    }
    foreach (p in fs\glob("**/*.md")) {
        if (!path\matches("node_modules/**", path: p.value)) {
            std\print(p.value);
        }
    }
}
```

{{if .Full}}`fs\glob` yields `Path` objects; read `.value`. A glob with no match is an empty list,
never the literal pattern the shell hands back.{{else}}`fs\glob` yields `Path`; read `.value`. No match: empty list, never literal pattern.{{end}}

### Editing a file in place

```sh
sed -i '' "s|$pattern|$replacement|g" "$file"
```

```buzz
import "fs";

fun replaceIn(file: str, pattern: str, replacement: str) > void !> any {
    final source = fs\readFile(file);
    if (source.indexOf(pattern) == null) {
        throw "{file}: pattern not found: {pattern}";
    }
    fs\writeFile(file, content: source.replace(pattern, with: replacement));
}
```

{{if .Full}}`sed` exits 0 when the pattern matches nothing, so the shell edit silently does nothing
once the source moves. The Buzz edit fails loudly. It also needs no regex escaping and
behaves the same on GNU and BSD.{{else}}`sed` exits 0 when pattern matches nothing: edit silently no-ops once source moves. Buzz edit fails loudly; no regex escaping; same on GNU and BSD.{{end}}

### Heredocs that generate a file

```sh
cat > "$dir/magusfile.buzz" <<EOF
import "spells/$spell";
magus\\project({"spells": [$spell]});
EOF
```

```buzz
import "fs";
import "template";

final PROJECT = `import "spells/{{"{{spell}}"}}";
magus\project({"spells": [{{"{{spell}}"}}]});
`;

fun writeProject(dir: str, spell: str) > void !> any {
    fs\writeFile(fs\join(dir, name: "magusfile.buzz"), content: template\render(PROJECT, data: {"spell": spell}));
}
```

{{if .Full}}Put Mustache in a backtick string: in a double-quoted string `{name}` interpolates. A
template keeps the generated text readable as one block, where a run of `echo` and
`printf` lines hides it.{{else}}Mustache in backtick string: double-quoted `{name}` interpolates. Template keeps generated text one readable block; `echo`/`printf` runs hide it.{{end}}

### Environment and arguments

```sh
WT="$(cd "${1:?usage: check.sh <worktree>}" && pwd)"
TOKEN="${GITHUB_TOKEN:?}"
LEVEL="${LOG_LEVEL:-info}"
MAGUS_X=0 some-tool
```

```buzz
import "std";
import "env";
import "os";
import "path";
import "proc";
import "flags";

final USAGE = "usage: magus buzz check.buzz -- [--dry-run] <worktree>";

fun main(args: [str]) > void !> any {
    final parsed = flags\parse(args, switches: ["--dry-run"], valued: ["--level"]);
    if (parsed.unknown.len() > 0 or parsed.positionals.len() != 1) {
        throw USAGE;
    }
    final worktree = path\abs(parsed.positionals[0]);
    final token = env\require("GITHUB_TOKEN");
    final level = parsed.values["--level"] ?? env\getOr("LOG_LEVEL", def: "info");
    std\print("{worktree} {level} token={token.len() > 0}");
    os\withEnv({"MAGUS_X": "0"}, callback: fun () > void !> any {
        proc\exec("some-tool");
    });
}
```

{{if .Full}}`flags\parse` returns every undeclared argument in `unknown` rather than guessing. Refuse
them. `os\withEnv` sets the variable for the processes the callback starts, never for the
script itself.{{else}}`flags\parse` returns undeclared arguments in `unknown`; refuse them. `os\withEnv` sets variable for processes callback starts, never script itself.{{end}}

### Version control

```sh
branch=$(git rev-parse --abbrev-ref HEAD)
[ -z "$(git status --porcelain)" ] || { echo dirty >&2; exit 1; }
git -C "$REPO" checkout -q --detach "$SHA"
```

```buzz
import "std";
import "vcs";

fun main(args: [str]) > void !> any {
    final branch = vcs\ref() ?? "(detached)";
    if (!vcs\status().clean) {
        throw "the working tree has uncommitted changes";
    }
    vcs\cmd(["checkout", "-q", "--detach", args[0]], opts: vcs\CommandOptions{ dir = "repo" });
    std\print("left {branch}; head now {vcs\commit().short}");
}
```

{{if .Full}}`proc\exec("git", ...)` is refused: version-control binaries run through `vcs\cmd`,
which holds them to the lease rules. Prefer the typed members; they also work for hg, sl
and jj. Reach for `vcs\cmd` only when no member answers.{{else}}`proc\exec("git", ...)` refused: VCS binaries run through `vcs\cmd` (lease rules). Prefer typed members (also hg, sl, jj); `vcs\cmd` only when no member answers.{{end}}

### Running a real tool, and pipes between tools

```sh
( cd "$wt/packages/http" && node --test )
node gen.mjs | prettier --stdin-filepath out.md > out.md
```

```buzz
import "fs";
import "proc";

fun main(args: [str]) > void !> any {
    proc\exec("node", args: ["--test"], dir: "packages/http");
    final generated = proc\exec("node", args: ["gen.mjs"], opts: proc\ExecOptions{ quiet = true });
    final pretty = proc\exec("prettier", args: ["--stdin-filepath", "out.md"],
        opts: proc\ExecOptions{ stdin = generated.stdout, quiet = true });
    fs\writeFile("out.md", content: pretty.stdout);
}
```

{{if .Full}}This is where `proc\exec` belongs: `node` and `prettier` are the programs the script
exists to run. It takes an argument list, never a command line, so nothing needs quoting.
It raises on a non-zero exit.{{else}}`proc\exec` belongs here: `node`, `prettier` ARE programs script exists to run. Argument list, never command line; nothing to quote. Raises on non-zero exit.{{end}}

### Asking magus

```sh
role=$(./magus describe file go.mod -o json | jq -r '.files[0].role')
./magus run generate docs
```

```buzz
import "std";
import "magus";

fun main(args: [str]) > void !> any {
    final report: magus\FileReport = magus\describe.file(["go.mod"]);
    std\print(report.files[0].role);
    magus\run(["generate", "docs"]);
}
```

{{if .Full}}A typed record fails the check on a misspelled field. A `jq` path returns `null` and
the script carries on.{{else}}Typed record fails check on misspelled field; `jq` path returns `null`, script carries on.{{end}}

## Anti-patterns

{{if .Full}}Each WRONG block checks and runs. That is the trouble: nothing flags it but review.{{else}}Each WRONG block checks and runs; only review catches it.{{end}}

{{if .Full}}**Shelling out to what a module does.** The call depends on a binary being installed,
on its flags (GNU or BSD), and on parsing its text.{{else}}Shelling out to what module does: depends on installed binary, its flags (GNU or BSD), parsing its text.{{end}}

```buzz
import "proc";

fun wrongDigest(file: str) > str !> any {
    return proc\exec("sha256sum", args: [file], opts: proc\ExecOptions{ quiet = true }).stdout.split(" ")[0];
}
```

```buzz
import "crypto";

fun digest(file: str) > str !> any {
    return crypto\sha256File(file);
}
```

{{if .Full}}**Building a command line from strings.** `proc\shell`, or `sh -c` with interpolated
values, brings back quoting, word splitting and injection. A path with a space breaks it.{{else}}Building command line from strings: `proc\shell` or `sh -c` with interpolated values brings back quoting, word splitting, injection. Path with space breaks it.{{end}}

```buzz
import "proc";

fun wrongUnpack(src: str, dest: str) > void !> any {
    proc\exec("sh", args: ["-c", "tar xzf {src} -C {dest} --strip-components=1"]);
}
```

```buzz
import "archive";

fun unpack(src: str, dest: str) > void !> any {
    archive\uncompress(src, dest: dest, opts: archive\Options{ strip = 1 });
}
```

{{if .Full}}**Ignoring a typed result.** `allowFailure` hands the decision to you. Dropping the
result turns a failure into success, which is exactly what `set -e` was preventing.{{else}}Ignoring typed result: `allowFailure` hands decision to you; dropping result turns failure into success.{{end}}

```buzz
import "proc";

fun wrongLint() > void !> any {
    proc\exec("eslint", args: ["."], opts: proc\ExecOptions{ allowFailure = true });
}
```

```buzz
import "proc";

fun lint() > bool !> any {
    final res = proc\exec("eslint", args: ["."], opts: proc\ExecOptions{ allowFailure = true, quiet = true });
    if (!res.ok and res.code != 1) {
        throw "eslint crashed (exit {res.code}): {res.stderr}";
    }
    return res.ok;
}
```

{{if .Full}}Omit `allowFailure` unless you read `.ok` or `.code` on the next line.{{else}}Omit `allowFailure` unless next line reads `.ok` or `.code`.{{end}}

{{if .Full}}**Parsing text a typed member returns.** Splitting `magus\cmd(...).stdout` or
`vcs\cmd(["status", "--porcelain"]).stdout` re-derives a record magus already built, and
breaks when the text format changes.{{else}}Parsing text typed member returns: splitting `magus\cmd(...).stdout` or `vcs\cmd(["status", "--porcelain"]).stdout` re-derives record magus built; breaks when format changes.{{end}}

```buzz
import "vcs";

fun wrongDirty() > bool !> any {
    return vcs\cmd(["status", "--porcelain"], opts: vcs\CommandOptions{ quiet = true }).stdout.trim() != "";
}
```

```buzz
import "vcs";

fun dirty() > bool !> any {
    return vcs\isDirty();
}
```

{{if .Full}}**Porting the scaffolding.** These have no job left in Buzz. Delete them, never
translate them:

- a `die` helper: use `throw`;
- an existence check before a read: the read raises;
- a temp file that passed data from one tool to the next;
- a trap cleaning that temp file;
- a retry loop with `sleep`: use `http\Retry` or `os\retry`;
- `command -v` choosing between two tools that do the same thing;
- globals assigned in one function and read in another: return a value.{{else}}Porting scaffolding: delete, never translate:
- `die` helper: `throw`
- existence check before read: read raises
- temp file passing data tool -> tool, and trap cleaning it
- retry loop with `sleep`: `http\Retry` or `os\retry`
- `command -v` choosing between two tools doing same thing
- globals set in one function, read in another: return value{{end}}

{{if .Full}}**Keeping a generator as a script.** A script that writes files other work reads belongs
in a target. Otherwise its output is never cached, never checked for drift, and never
rerun when its inputs change.{{else}}Generator kept as script: writes files other work reads -> target. Otherwise never cached, never drift-checked, never rerun on input change.{{end}}
{{if .Full}}
## A whole rewrite

A dataset fetch script, 50 lines of bash: `mktemp` and a trap, five `curl` calls into
page files, a `jq -e` length check per page, `jq -s` to merge, sort and drop a field,
`wc -l` to count rows, a `sha256sum`-or-`shasum` fallback, and `cut` to compare against a
pinned digest. It reads one URL and writes one JSONL file, so it stays a script: it runs on
demand and nothing builds from its output.

```buzz
import "std";
import "fs";
import "http";
import "crypto";
import "encoding/json";

final API = "https://datasets-server.huggingface.co/rows?dataset=SWE-bench%2FSWE-bench_Verified&config=default&split=test";

fun fetchPage(offset: int) > [any] !> any {
    final res = http\get("{API}&offset={offset}&length=100",
        opts: http\Options{ fail = true },
        retry: http\Retry{ attempts = 5, delayMs = 2000.0, allErrors = true });
    final rows: [any] = json\parse(res.body)["rows"];
    if (rows.len() != 100) {
        throw "page at offset {offset} returned {rows.len()} rows, not 100";
    }
    return rows;
}

fun main(args: [str]) > void !> any {
    final table = "verified.jsonl";
    final pin = "verified.sha256";
    final byId = mut {<str: str>};
    foreach (offset in [0, 100, 200, 300, 400]) {
        foreach (row in fetchPage(offset)) {
            final record: {str: any} = row["row"];
            final kept = record.cloneMutable();
            kept.remove("hints_text");
            byId["{record["instance_id"]}"] = json\stringify(kept);
        }
    }
    if (byId.len() != 500) {
        throw "expected 500 rows, got {byId.len()}";
    }
    final ids = byId.keys().cloneMutable();
    ids.sort(fun (a: str, b: str) > bool => a < b);
    fs\writeLines(table, lines: ids.map(fun (i: int, id: str) > str => byId[id]!));

    final sum = crypto\sha256File(table);
    std\print("{sum}  {table}");
    if (fs\isFile(pin)) {
        final pinned = fs\readFile(pin).split(" ")[0];
        if (pinned != sum) {
            throw "{table} digest {sum} differs from the pinned {pinned}; the upstream table changed";
        }
    }
}
```

What went: the temp directory, the trap, every `jq` program, the row-count pipeline, and the
digest tool fallback. What stayed: the five pages, the two counts, the pinned-digest
comparison. Those are what the script does. Before replacing the shell, run both and
compare the JSONL files byte for byte. A `jq -c` and a `json\stringify` can order keys
differently, and only the diff says so.
{{end}}
## Verify the port

```sh
{{cmd "buzz"}} --check new.buzz
{{cmd "buzz"}} -t new.buzz
{{cmd "buzz"}} new.buzz -- <args>
```

{{if .Full}}Then run the old script and the new one on the same input and diff what each wrote. A
target is verified by `{{cmd "run"}} <target> <project>`, then once more to show the cache
hit. Delete the `.sh` in the same commit that adds its replacement, so no caller keeps
running the old one.{{else}}Then run old and new on same input, diff what each wrote. Target: `{{cmd "run"}} <target> <project>`, then again to show cache hit. Delete `.sh` in same commit adding replacement; no caller keeps running old one.{{end}}
