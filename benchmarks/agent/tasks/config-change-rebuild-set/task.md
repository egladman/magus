# Which apps have to be rebuilt if packages/platform/config changes?

There are five applications under `apps/`. Each one is built from the feature
libraries under `packages/<its name>/`, and some of those libraries reach into the
packages under `packages/platform/`, which in turn import each other.

Work out which of the five apps would have to be rebuilt after a change to
`packages/platform/config`. Change no code.

Write the answer to `ANSWER.md` at the root of this checkout, in exactly this shape:

## Answer

- apps/some-app
- apps/other-app

One directory path per bullet and no other bullets under that heading. The list is
graded as a set, so naming an app that does not need rebuilding costs the same as
missing one that does.
