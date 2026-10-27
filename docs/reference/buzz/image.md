---
title: image module
generated_from: reference/buzz/
aliases: [modules/image]
description: Assemble and publish OCI images.
tags: [image, module, stdlib, magusfile]
---

# image

Assemble and publish OCI images. An Image is the artifact; a Container is a running instance. Layouts hold one Image Manifest or Image Index.

> **Naming convention:** import the module under its bare name (`import "image"`), reach members with a backslash, and call methods in `camelCase`: `image\someMethod`.

## Methods

### pullLayout

Fetch the image at reference (name@sha256:<hex>) for platform into dest as an OCI Image Layout. Optional auth: {username, password:{value}}.

**Signature:** `image\pullLayout(reference, platform, dest, [auth]) -> ImageLayout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter   | Type             | Optional | Description |
| ----------- | ---------------- | -------- | ----------- |
| `reference` | `string`         |          |             |
| `platform`  | `map[string]any` |          |             |
| `dest`      | `string`         |          |             |
| `auth`      | `map[string]any` | yes      |             |

**Returns:** map[string]any

### assembleLayout

Assemble an OCI Image Layout from spec into dest: optional base, at most one new Layer for files/symlinks, then config and Image Manifest.

**Signature:** `image\assembleLayout(spec, dest) -> ImageLayout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `spec`    | `map[string]any` |          |             |
| `dest`    | `string`         |          |             |

**Returns:** map[string]any

### assembleIndex

Combine single-platform layouts into an Image Index at dest.

**Signature:** `image\assembleIndex(layouts, dest) -> ImageLayout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `layouts` | `[]string` |          |             |
| `dest`    | `string`   |          |             |

**Returns:** map[string]any

### publishLayout

Push the layout to repository under tags; empty tags means digest only. Optional auth: {username, password:{value}}.

**Signature:** `image\publishLayout(layout, repository, tags, [auth]) -> ImagePublished` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter    | Type             | Optional | Description |
| ------------ | ---------------- | -------- | ----------- |
| `layout`     | `string`         |          |             |
| `repository` | `string`         |          |             |
| `tags`       | `[]string`       |          |             |
| `auth`       | `map[string]any` | yes      |             |

**Returns:** map[string]any

### writeSbom

Write an SPDX SBOM for layout to dest and return the document descriptor.

**Signature:** `image\writeSbom(layout, dest) -> ImageSBOM` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `layout`  | `string` |          |             |
| `dest`    | `string` |          |             |

**Returns:** map[string]any

### writeProvenance

Write an in-toto provenance statement for layout using spec to dest.

**Signature:** `image\writeProvenance(layout, spec, dest) -> ImageProvenance` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `layout`  | `string`         |          |             |
| `spec`    | `map[string]any` |          |             |
| `dest`    | `string`         |          |             |

**Returns:** map[string]any

### attachArtifacts

Attach optional sbomPath and provenancePath documents onto the Image Index at layout.

**Signature:** `image\attachArtifacts(layout, [sbomPath], [provenancePath]) -> ImageLayout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter        | Type     | Optional | Description |
| ---------------- | -------- | -------- | ----------- |
| `layout`         | `string` |          |             |
| `sbomPath`       | `string` | yes      |             |
| `provenancePath` | `string` | yes      |             |

**Returns:** map[string]any

