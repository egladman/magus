---
title: math module
generated_from: reference/buzz/
aliases: [modules/math]
description: Rounding to a decimal place, clamping, and aggregation over a list of numbers.
tags: [math, module, stdlib, magusfile]
---

# math

Rounding to a decimal place, clamping, and aggregation over a list of numbers.

> **Naming convention:** import the module under its bare name (`import "math"`), reach members with a backslash, and call methods in `camelCase`: `math\someMethod`.

## Methods

### round

Round x to places decimal places, half away from zero. places defaults to 0 (the nearest whole number) and may be negative to round to tens, hundreds and so on. Rendering a coverage percentage or a duration is the usual reason; Buzz's floor and ceil cannot express it.

**Signature:** `math\round(x, [places]) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L108)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `x` | `float64` |  | |
| `places` | `int` | yes | |

**Returns:** float64

### trunc

Discard x's fractional part, rounding TOWARD ZERO - so -1.7 is -1, where floor gives -2. The difference matters whenever a value can be negative and you meant "drop the decimals".

**Signature:** `math\trunc(x) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L124)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `x` | `float64` |  | |

**Returns:** float64

### clamp

Constrain x to the range lo..hi, returning lo when x is below it and hi when above. Sizing parallelism is the usual reason - clamp(cpus / 2, 1, 8) never yields zero workers. Raises when lo is greater than hi, which is a caller bug rather than an empty range.

**Signature:** `math\clamp(x, lo, hi) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L127)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `x` | `float64` |  | |
| `lo` | `float64` |  | |
| `hi` | `float64` |  | |

**Returns:** float64

### sum

Add every number in the list; an empty list sums to 0. Non-numeric items are skipped rather than counted as zero.

**Signature:** `math\sum(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L135)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `nums` | `[]float64` |  | |

**Returns:** float64

### mean

The arithmetic mean. Raises on an empty list rather than returning 0, because 0 is a real average and "there was nothing to average" is not - returning it would let an empty set silently pass a floor check.

**Signature:** `math\mean(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L153)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `nums` | `[]float64` |  | |

**Returns:** float64

### median

The middle value, averaging the two middle values for an even count. Prefer it to mean when reporting what a TYPICAL run costs: one pathological outlier moves a mean and barely moves a median. Raises on an empty list, like mean.

**Signature:** `math\median(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L163)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `nums` | `[]float64` |  | |

**Returns:** float64

### min

The smallest number in the list. Distinct from Buzz's own minInt/minDouble, which compare exactly two values. Raises on an empty list.

**Signature:** `math\min(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L179)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `nums` | `[]float64` |  | |

**Returns:** float64

### max

The largest number in the list. Distinct from Buzz's own maxInt/maxDouble, which compare exactly two values. Raises on an empty list.

**Signature:** `math\max(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go#L191)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `nums` | `[]float64` |  | |

**Returns:** float64

