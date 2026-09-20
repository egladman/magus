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
Date: 2026-09-20T15:34:14Z
Go: go1.26.6-X:jsonv2
Kernel: Darwin Elis-MacBook-Air.local 25.6.0 Darwin Kernel Version 25.6.0: Fri Jul 31 19:16:20 PDT 2026; root:xnu-12377.161.14~5/RELEASE_ARM64_T8142 arm64
CPU: Apple M5
CPU cores: 10
RAM: 25165824 kB
magus commit: 1928b209f02f4522e324f166094c9fe5d4ec3c5f
```

### Tool versions (observed)

```text
hyperfine: hyperfine 1.19.0
magus: magus v0.4.3-42-g1928b209f (1928b209f) built 2026-09-20T09:46:13-05:00
make (gmake): GNU Make 4.4.1
```

---

## Fixture: go (N=50)

### S1: Startup overhead (`--version`)

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make  | off    |        1 |         2 |           1 |   0.21 |        2 |   50 |
| magus | on     |        9 |        11 |          11 |      2 |       15 |   50 |
| magus | off    |       18 |        39 |          36 |     16 |      101 |   50 |

### S2: Project discovery

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus | off    |      174 |       213 |         217 |     27 |      245 |   10 |

### S3: Affected dry-run (1 file changed)

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus | on     |       33 |        37 |          36 |      3 |       43 |   10 |
| magus | off    |      110 |       151 |         154 |     25 |      189 |   10 |

### S4: Cold build, parallel

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make  | off    |     1254 |      1298 |        1291 |     34 |     1358 |   10 |
| magus | off    |    15582 |     19387 |       19529 |   2236 |    23278 |   10 |
| magus | on     |    18893 |     20864 |       20010 |   2101 |    24337 |   10 |

### S5: Warm cache replay

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make  | off    |        4 |         4 |           4 |   0.23 |        5 |   10 |
| magus | off    |    15295 |     16766 |       15910 |   1574 |    19716 |   10 |
| magus | on     |    17796 |     19785 |       18929 |   2374 |    25638 |   10 |

### S6: One leaf file changed

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make  | off    |      110 |       120 |         114 |     15 |      152 |   10 |
| magus | off    |    15656 |     16634 |       16235 |   1045 |    18849 |   10 |
| magus | on     |    17951 |     18458 |       18171 |    731 |    20277 |   10 |
