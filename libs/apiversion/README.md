# apiversion

A zero-dependency envelope for versioned records shaped as Kubernetes objects:
`api_version`, `kind`, `metadata`, `spec`, every key in snake_case. A reader refuses
every header but its own.

- `APIVersion.Valid` requires a lowercase DNS-1123 group with a dot, then `/v<major>[alpha<n>|beta<n>]`.
- `TypeMeta` is the `api_version` and `kind` pair; a record type embeds it.
- `New` stamps the `TypeMeta` from the spec type, and a reader refuses another with a
  `RefusedError`.
- `Metadata` holds only ObjectMeta members; required features live in the
  annotation `<group>/requires`.
- `Condition` mirrors `metav1.Condition`.
