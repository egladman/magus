---
title: Build-tool benchmarks
generated_from: benchmarks/BENCHMARKS.md
description: magus timed against make on one fixture with hyperfine, stamped with the date, hardware and tool versions. Generated from the committed results.
tags: [benchmarks, performance, measurement]
---

# magus benchmarks

Measured in this run: magus, make, on the go fixture(s).
A tool absent from the tables below produced no results here and is
not being compared. A row marked FAILED exited non-zero and is not a
measurement of the work the scenario describes.

## Environment

```text
Date: 2026-09-20T16:47:37Z
Go: go1.26.6-X:jsonv2
Kernel: Darwin Elis-MacBook-Air.local 25.6.0 Darwin Kernel Version 25.6.0: Fri Jul 31 19:16:20 PDT 2026; root:xnu-12377.161.14~5/RELEASE_ARM64_T8142 arm64
CPU: Apple M5
CPU cores: 10
RAM: 25165824 kB
magus commit: 34a4a12b31f1ba77f956c992029e846fe11b5d48
```

### Tool versions (observed)

```text
  hyperfine: hyperfine 1.19.0
  magus: magus v0.4.3-46-g34a4a12b3 (34a4a12b3) built 2026-09-20T11:42:48-05:00
  make (gmake): GNU Make 4.4.1
```

---

## Fixture: go (N=50)

### S1: Startup overhead (`--version`)

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus      | on         |     0.00 |      0.00 |        0.00 |   0.00 |     0.00 |   50 |
| make       | off        |        1 |         1 |           1 |   0.14 |        2 |   50 |
| magus      | off        |        9 |         9 |           9 |   0.63 |       13 |   50 |

### S2: Project discovery

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus      | off        |      103 |       104 |         104 |   0.86 |      106 |   10 |

### S3: Affected dry-run (1 file changed)

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus      | on         |       38 |        39 |          38 |   0.66 |       39 |   10 |
| magus      | off        |       90 |       102 |         101 |      7 |      117 |   10 |

### S4: Cold build, parallel

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make       | off        |     1309 |      1349 |        1355 |     22 |     1373 |   10 |
| magus      | on         |     3291 |      3552 |        3342 |    324 |     4073 |   10 |
| magus      | off        |     3527 |      3748 |        3574 |    363 |     4603 |   10 |

### S5: Warm cache replay

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make       | off        |        5 |         5 |           5 |   0.12 |        5 |   10 |
| magus      | off        |     1884 |      2083 |        1902 |    389 |     2914 |   10 |
| magus      | on         |     2178 |      2414 |        2271 |    272 |     2901 |   10 |

### S6: One leaf file changed

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make       | off        |      107 |       110 |         108 |      2 |      115 |   10 |
| magus      | off        |     1891 |      2126 |        2004 |    317 |     2865 |   10 |
| magus      | on         |     2254 |      2470 |        2368 |    253 |     2940 |   10 |

