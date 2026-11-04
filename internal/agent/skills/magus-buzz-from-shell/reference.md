# magus-buzz-from-shell reference

The longer examples behind the magus-buzz-from-shell skill. Every Buzz block checks as written.

## A temp directory that must exist

Most temp files in a shell script exist only to pass data between tools, and go once the
data stays in memory. When a tool really needs a directory, remove it on both paths:

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

## Running a real tool, and pipes between tools

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

## Asking magus

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
