---
title: image module
generated_from: reference/buzz/
aliases: [modules/image]
description: Build, assemble and publish OCI images.
tags: [image, module, stdlib, magusfile]
---

# image

Build, assemble and publish OCI images. Layouts are directories holding exactly one manifest or index; equal inputs give byte-identical layouts.

> **Naming convention:** import the module under its bare name (`import "image"`), reach members with a backslash, and call methods in `camelCase`: `image\someMethod`.

## Methods

### pull

Fetch the image at reference, a name@sha256:<hex> digest reference, for platform into the OCI layout directory dest. Returns the layout.

**Signature:** `image\pull(reference, platform, dest) -> ImageLayout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter   | Type             | Optional | Description |
| ----------- | ---------------- | -------- | ----------- |
| `reference` | `string`         |          |             |
| `platform`  | `map[string]any` |          |             |
| `dest`      | `string`         |          |             |

**Returns:** map[string]any

### write

Write the image described by spec, at most one new layer over spec.base, into the OCI layout directory dest. Returns the layout.

**Signature:** `image\write(spec, dest) -> ImageLayout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `spec`    | `map[string]any` |          |             |
| `dest`    | `string`         |          |             |

**Returns:** map[string]any

### index

Combine layouts, each holding one platform manifest, into an image index written to the OCI layout directory dest. Returns the layout.

**Signature:** `image\index(layouts, dest) -> ImageLayout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `layouts` | `[]string` |          |             |
| `dest`    | `string`   |          |             |

**Returns:** map[string]any

### publish

Push the layout to repository, then tag it with each of tags; an empty tags list pushes by digest only. Returns what the registry confirmed.

**Signature:** `image\publish(layout, repository, tags, [opts]) -> ImagePublished` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter    | Type             | Optional | Description |
| ------------ | ---------------- | -------- | ----------- |
| `layout`     | `string`         |          |             |
| `repository` | `string`         |          |             |
| `tags`       | `[]string`       |          |             |
| `opts`       | `map[string]any` | yes      |             |

**Returns:** map[string]any

