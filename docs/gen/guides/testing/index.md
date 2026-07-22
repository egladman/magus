---
title: Testing
description: How to test Buzz code in magus with the test primitive and the assert and suite libraries, and why not to test magusfiles.
tags: [testing, buzz, assert, suite, spells, conventions]
---

# Testing

Buzz builds testing into the language, as Go does: a `test` block is a first-class
construct. magus adds two small libraries that make those blocks easier to write.
`assert` provides value-aware matchers; `suite` groups stateful tests. magus registers
both as extensions alongside Buzz's standard library.

## What to test, and what not to

Test the code that carries logic: standalone spells, and Buzz modules imported into a
magusfile. The docs project's magusfile imports `docs/render.buzz`, a static-site
generator, and that generator earns its tests.

Do not write tests for the magusfile itself.

A magusfile is declarative build configuration. Keep it thin: wire targets together
and push logic elsewhere. When a magusfile grows complex enough to want tests, move
that logic into a spell or a sibling module and test it there. A magusfile wires your
build together, so testing one means testing your configuration. Keep magusfiles thin
enough that the question never comes up.

## Writing a test

A `test` block runs when you execute the file with `-t`. Inside it, call `assert`
matchers; a failed assertion raises and fails the test.

```buzz
import "assert";

test "collectAlias trims slashes and records every alias" {
    final s = mut Site{};
    s.collectAlias("/old-path/", "new-path/");

    // Buzz's == is reference identity for maps, so compare by value with assert.
    assert.equal(s.aliasTarget, {"old-path": "new-path/"}, "alias recorded, slashes trimmed");
}
```

Run the tests:

```bash
magus buzz -t --embedded render.buzz   # a single file
magus run buzz-test                    # the project's test target
```

## The `assert` module

Every matcher raises on failure, the same contract as the built-in `std\assert`, with
value-aware checks on top. Buzz has no optional or variadic parameters, so each
matcher takes a trailing `message` (pass `""` for none; a short label makes failures
easier to read).

| Matcher                                             | Passes when                                                                               |
| --------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| `equal(got, want, message)`                         | `got` is structurally (deeply) equal to `want`                                            |
| `notEqual(got, want, message)`                      | `got` is not deeply equal to `want`                                                       |
| `isTrue(got, message)` / `isFalse(got, message)`    | `got` is `true` / `false`                                                                 |
| `isNull(got, message)` / `notNull(got, message)`    | `got` is / is not `null`                                                                  |
| `contains(container, item, message)`                | a str holds a substring, a list holds an element (by deep equality), or a map holds a key |
| `len(container, want, message)`                     | a str, list, or map has exactly `want` elements                                           |
| `isEmpty(container, message)`                       | a str, list, or map is empty                                                              |
| `greater(a, b, message)` / `less(a, b, message)`    | two numbers or two strings order that way                                                 |
| `throws(fn, message)` / `doesNotThrow(fn, message)` | calling `fn` does / does not raise                                                        |
| `fail(message)`                                     | Always fails the test                                                                     |

Reach for `equal` most often. `==` compares identity for maps, lists, and objects, so
`{a: 1} == {a: 1}` is `false`; `assert.equal` compares by value, recursing to any
depth and ignoring map key order.

`assert.skip(message)` stops the current test and marks it skipped instead of failed
(Go's `t.Skip`). Use it for a case that cannot run in the current environment. The
runner reports skipped tests apart from failures:

```buzz
test "reads the platform keychain" {
    if (os\env("CI") != null) {
        assert.skip("no keychain in CI");
    }
    assert.notNull(readKey(), "key present");
}
```

## The `suite` module

Reach for a suite when several tests share setup or accumulated state. A suite runs
every case and reports all failures at the end; its matchers are soft, so a failed
check records the failure and the test keeps going. You run a suite as a plain script
rather than with `-t`: build the suite, run cases with `it`, then call `summary`.

```buzz
import "suite";

final s = suite\new(
    setupAll: null,
    setupEach: fun () > void { /* fresh fixture per test */ },
    teardownAll: null,
    teardownEach: null
);

s.it("parses a release line", fun () > void {
    s.equal(parseVersion("## [1.2.0] - 2026-01-01"), "1.2.0", "version extracted");
    s.notNull(s, "suite is live");
});

s.summary();   // prints results; exits non-zero if any test failed
```

The soft matchers mirror `assert`'s core (`equal`, `notEqual`, `isTrue`, `isFalse`,
`isNull`, `notNull`, `fail`) as methods on the suite. The lifecycle hooks
(`setupAll`, `setupEach`, `teardownAll`, `teardownEach`) run around the cases; pass
`null` to skip one.

### Per-test controls

Go's testing package hangs its features off the `*testing.T` passed to each test. A
`test` block has no such handle; a Suite is that handle, so it carries the same
per-test controls, callable from inside an `it` body:

| Method             | Go analog   | Effect                                                                                                   |
| ------------------ | ----------- | -------------------------------------------------------------------------------------------------------- |
| `s.skip(message)`  | `t.Skip`    | Marks the test skipped and stops its body                                                                |
| `s.fatal(message)` | `t.Fatal`   | Records a failure and stops the body, for a broken precondition where the soft matchers would keep going |
| `s.cleanup(fn)`    | `t.Cleanup` | Registers a cleanup run after the test in reverse order, whether it passed or failed                     |
| `s.log(message)`   | `t.Log`     | Buffers a line printed only if the test fails                                                            |
| `s.name()`         | `t.Name`    | The name of the running test                                                                             |

```buzz
s.it("reads the fixture", fun () > void {
    if (os\env("CI") == null) {
        s.skip("needs the CI fixture");
    }

    final handle = openFixture();
    s.cleanup(fun () > void { handle.close(); });   // always runs

    s.log("fixture at {handle.path}");              // shown only on failure
    s.equal(handle.read(), "expected", "contents");
});
```
