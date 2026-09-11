# Which feature libraries end up using packages/platform/http?

Four small packages live under `packages/platform/`. Some of the feature libraries
under `packages/<app>/important-feature-*` reach into them, and the platform
packages also import each other.

Work out which feature libraries end up depending on `packages/platform/http` -
directly, or by way of another platform package. Change no code.

Write the answer to `ANSWER.md` at the root of this checkout, in exactly this shape:

## Answer

- packages/some-app/important-feature-0
- packages/other-app/important-feature-1

One directory path per bullet and no other bullets under that heading. The list is
graded as a set, so a path that does not belong costs the same as one that is
missing.
