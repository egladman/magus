---
title: math module
generated_from: reference/buzz/
aliases: [modules/math]
description: "Buzz's math plus rounding to a decimal place, clamping, and aggregation over a list of numbers."
tags: [math, module, stdlib, magusfile]
---

# math

Buzz's math plus rounding to a decimal place, clamping, and aggregation over a list of numbers.

> **Naming convention:** import the module under its bare name (`import "math"`), reach members with a backslash, and call methods in `camelCase`: `math\someMethod`.

## Methods

### round

Round x to places decimal places, half away from zero. places defaults to 0 (the nearest whole number) and may be negative to round to tens, hundreds and so on. Rendering a coverage percentage or a duration is the usual reason; Buzz's floor and ceil cannot express it.

**Signature:** `math\round(x, [places]) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |
| `places`  | `int`     | yes      |             |

**Returns:** float64

### trunc

Discard x's fractional part, rounding TOWARD ZERO - so -1.7 is -1, where floor gives -2. The difference matters whenever a value can be negative and you meant "drop the decimals".

**Signature:** `math\trunc(x) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### clamp

Constrain x to the range lo..hi, returning lo when x is below it and hi when above. Sizing parallelism is the usual reason - clamp(cpus / 2, 1, 8) never yields zero workers. Raises when lo is greater than hi, which is a caller bug rather than an empty range.

**Signature:** `math\clamp(x, lo, hi) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |
| `lo`      | `float64` |          |             |
| `hi`      | `float64` |          |             |

**Returns:** float64

### sum

Add every number in the list; an empty list sums to 0. Non-numeric items are skipped rather than counted as zero.

**Signature:** `math\sum(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `nums`    | `[]float64` |          |             |

**Returns:** float64

### mean

The arithmetic mean. Raises on an empty list rather than returning 0, because 0 is a real average and "there was nothing to average" is not - returning it would let an empty set silently pass a floor check.

**Signature:** `math\mean(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `nums`    | `[]float64` |          |             |

**Returns:** float64

### median

The middle value, averaging the two middle values for an even count. Prefer it to mean when reporting what a TYPICAL run costs: one pathological outlier moves a mean and barely moves a median. Raises on an empty list, like mean.

**Signature:** `math\median(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `nums`    | `[]float64` |          |             |

**Returns:** float64

### smallest

The smallest number in the list. Buzz's own minInt/minDouble keep the two-value form; this one takes a list. Raises on an empty list.

**Signature:** `math\smallest(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `nums`    | `[]float64` |          |             |

**Returns:** float64

### largest

The largest number in the list. Buzz's own maxInt/maxDouble keep the two-value form; this one takes a list. Raises on an empty list.

**Signature:** `math\largest(nums) -> float64` - [source](https://github.com/egladman/magus/blob/main/std/math.go)

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `nums`    | `[]float64` |          |             |

**Returns:** float64

### abs

Absolute value of x. Buzz's own.

**Signature:** `math\abs(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### acos

Arc cosine of x, in radians. Buzz's own.

**Signature:** `math\acos(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### asin

Arc sine of x, in radians. Buzz's own.

**Signature:** `math\asin(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### atan

Arc tangent of x, in radians. Buzz's own.

**Signature:** `math\atan(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### ceil

Smallest whole number not below x. Buzz's own.

**Signature:** `math\ceil(x) -> int`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** int

### cos

Cosine of x, in radians. Buzz's own.

**Signature:** `math\cos(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### deg

Convert x from radians to degrees. Buzz's own.

**Signature:** `math\deg(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### exp

e raised to x. Buzz's own.

**Signature:** `math\exp(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### floor

Largest whole number not above x. Buzz's own.

**Signature:** `math\floor(x) -> int`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** int

### log

Logarithm of n in the given base. Buzz's own.

**Signature:** `math\log(base, n) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `base`    | `float64` |          |             |
| `n`       | `float64` |          |             |

**Returns:** float64

### maxDouble

The larger of two doubles. Buzz's own.

**Signature:** `math\maxDouble(a, b) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `a`       | `float64` |          |             |
| `b`       | `float64` |          |             |

**Returns:** float64

### maxInt

The larger of two ints. Buzz's own.

**Signature:** `math\maxInt(a, b) -> int`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `a`       | `int` |          |             |
| `b`       | `int` |          |             |

**Returns:** int

### minDouble

The smaller of two doubles. Buzz's own.

**Signature:** `math\minDouble(a, b) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `a`       | `float64` |          |             |
| `b`       | `float64` |          |             |

**Returns:** float64

### minInt

The smaller of two ints. Buzz's own.

**Signature:** `math\minInt(a, b) -> int`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `a`       | `int` |          |             |
| `b`       | `int` |          |             |

**Returns:** int

### pow

x raised to the power y; raises on overflow and underflow. Buzz's own.

**Signature:** `math\pow(x, y) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |
| `y`       | `float64` |          |             |

**Returns:** float64

### rad

Convert x from degrees to radians. Buzz's own.

**Signature:** `math\rad(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### sin

Sine of x, in radians. Buzz's own.

**Signature:** `math\sin(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### sqrt

Square root of x. Buzz's own.

**Signature:** `math\sqrt(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

### tan

Tangent of x, in radians. Buzz's own.

**Signature:** `math\tan(x) -> float64`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `x`       | `float64` |          |             |

**Returns:** float64

