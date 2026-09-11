# Turning an option off has no effect

Callers of `packages/platform/config` report that they cannot override a setting
with a falsy value:

- a default of `{ retry: true }` overridden with `{ retry: false }` still reads back
  as `true`
- a default of `{ retries: 3 }` overridden with `{ retries: 0 }` still reads back as
  `3`
- a default of `{ prefix: 'v1' }` overridden with `{ prefix: '' }` still reads back
  as `v1`

An override of `undefined` is meant to be ignored, and that part is correct.

Find and fix the bug. The package's own tests reproduce it. Leave the rest of the
package's behavior alone: nested objects still merge recursively, and the base
object is still never mutated.
