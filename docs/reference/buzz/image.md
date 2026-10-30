---
title: image module
generated_from: reference/buzz/
aliases: [modules/image]
description: Assemble OCI images from files, and pull and push them.
tags: [image, module, stdlib, magusfile]
---

# image

Assemble OCI images from files, and pull and push them. Each layout is an OCI Image Layout holding one Image Manifest or Image Index; a stage, layer, platform, index and push is each its own target.

> **Naming convention:** import the module under its bare name (`import "image"`), reach members with a backslash, and call methods in `camelCase`: `image\someMethod`.

## Methods

### assemble

Write the Image Manifest spec describes as a layout at layout: the base's layers, at most one new layer, the config and the manifest. Never touches the network.

**Signature:** `image\assemble(spec, layout) -> Layout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `spec`    | `map[string]any` |          |             |
| `layout`  | `string`         |          |             |

**Returns:** map[string]any

### layout

List every entry the new layer of spec holds, in added.entries: path, kind, mode, owner, size, mtime and the Spec item that placed it. It is the list assemble writes, read from the spec's host files without writing; there are no digests yet, and the base is never opened. A dry run of assemble returns the same.

**Signature:** `image\layout(spec) -> Layout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `spec`    | `map[string]any` |          |             |

**Returns:** map[string]any

### assembleIndex

Write one Image Index over single-platform layouts as a layout at layout. Never touches the network.

**Signature:** `image\assembleIndex(layouts, layout) -> Layout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `layouts` | `[]string` |          |             |
| `layout`  | `string`   |          |             |

**Returns:** map[string]any

### pull

Fetch reference for platform into a layout at layout. A name@sha256:<hex> reference is content-addressed and caches like any input; a tag-only reference is refused outside the update charm.

**Signature:** `image\pull(reference, platform, layout, [auth]) -> Layout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter   | Type             | Optional | Description |
| ----------- | ---------------- | -------- | ----------- |
| `reference` | `string`         |          |             |
| `platform`  | `map[string]any` |          |             |
| `layout`    | `string`         |          |             |
| `auth`      | `map[string]any` | yes      |             |

**Returns:** map[string]any

### push

Push the layout's image, its blobs and its referrers to repository, then tag it with each of tags; empty tags pushes by digest only. On a registry without the Referrers API each referrer is also listed in the subject's sha256-<hex> tag index.

**Signature:** `image\push(layout, repository, tags, [auth]) -> Pushed` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter    | Type             | Optional | Description |
| ------------ | ---------------- | -------- | ----------- |
| `layout`     | `string`         |          |             |
| `repository` | `string`         |          |             |
| `tags`       | `[]string`       |          |             |
| `auth`       | `map[string]any` | yes      |             |

**Returns:** map[string]any

### writeSBOM

Write an SPDX 2.3 document naming the layout's image by digest to path.

**Signature:** `image\writeSBOM(layout, path) -> Document` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `layout`  | `string` |          |             |
| `path`    | `string` |          |             |

**Returns:** map[string]any

### writeProvenance

Write an in-toto Statement v1 with SLSA v1 provenance for the layout's image to path. The digest of each base it was assembled over joins the dependencies.

**Signature:** `image\writeProvenance(layout, provenance, path) -> Document` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter    | Type             | Optional | Description |
| ------------ | ---------------- | -------- | ----------- |
| `layout`     | `string`         |          |             |
| `provenance` | `map[string]any` |          |             |
| `path`       | `string`         |          |             |

**Returns:** map[string]any

### attachReferrers

Write a new layout at layout holding subject's image and one OCI 1.1 referrer per document. subject is never edited.

**Signature:** `image\attachReferrers(subject, documents, layout) -> Layout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter   | Type     | Optional | Description |
| ----------- | -------- | -------- | ----------- |
| `subject`   | `string` |          |             |
| `documents` | `any`    |          |             |
| `layout`    | `string` |          |             |

**Returns:** map[string]any

### open

Read the layout at layout back: the image whose org.opencontainers.image.ref.name is name (the only image when name is empty), the referrers whose subject it is, and for a single-platform image whose index.json entry names its platform, that platform, config and layers.

**Signature:** `image\open(layout, [name]) -> Layout` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `layout`  | `string` |          |             |
| `name`    | `string` | yes      |             |

**Returns:** map[string]any

### referrers

Read the documents attached beside the image at layout (the SBOM and provenance referrers) without a registry: one Document per referrer layer, its Path the blob in the layout so fs\read reads it, its Descriptor the layer's with the referrer's artifactType. name picks the image as open does.

**Signature:** `image\referrers(layout, [name]) -> [Document]` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `layout`  | `string` |          |             |
| `name`    | `string` | yes      |             |

**Returns:** any

### parsePlatform

Parse "linux/amd64", "linux/amd64/v3" or "linux/arm64/v8" into a Platform.

**Signature:** `image\parsePlatform(spec) -> Platform` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `spec`    | `string` |          |             |

**Returns:** map[string]any

### parsePlatforms

Parse each of specs into a Platform, refusing two that name the same platform: the set a multi-arch build fans out over.

**Signature:** `image\parsePlatforms(specs) -> [Platform]` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `specs`   | `[]string` |          |             |

**Returns:** any

### goEnv

The GOOS, GOARCH and, for an amd64 variant, GOAMD64 that make go build target platform, for a build target's ctx.withEnv.

**Signature:** `image\goEnv(platform) -> map[string]string` - [source](https://github.com/egladman/magus/blob/main/std/image.go)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `platform` | `map[string]any` |          |             |

**Returns:** map[string]string

