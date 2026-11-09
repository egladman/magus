# text

Wording packages shared by magus's Go code and, through the `strings` host
module, its Buzz scripts. Like the standard library's `text/` tree, the module
root holds no package; each package under it is named for its job.

## plural

| Function       | Answers                                                       |
| -------------- | ------------------------------------------------------------- |
| `plural.Word`  | `one` when the count is 1, else `many`; 0 is plural           |
| `plural.Count` | the count, a space, then `plural.Word`: `1 target`, `3 targets` |

The caller spells both forms. No rule derives `es` or `ies` from a noun, so
`branch` and `branches` are written out at each call site. Buzz reaches them as
`strings\plural` and `strings\pluralCount`.
