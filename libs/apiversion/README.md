# apiversion

A zero-dependency envelope for versioned records shaped as Kubernetes objects:
`apiVersion`, `kind`, `metadata`, `spec`. A reader refuses every header but its own.

- `Parse` requires a lowercase DNS-1123 group with a dot, then `/v<major>[alpha<n>|beta<n>]`.
- `New`, `Read` and `ReadHeader` stamp and check the header from the spec type.
- `Metadata` holds only ObjectMeta members; required features live in the
  annotation `<group>/requires`.
- `Condition` mirrors `metav1.Condition`.
