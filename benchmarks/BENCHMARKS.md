# magus benchmarks

Head-to-head: magus vs turbo, nx, lage, moon, bazel, make.

## Environment

```
Date: 2026-06-18T02:51:33Z
Go: go1.24.7
Kernel: Linux vm 6.18.5 #1 SMP PREEMPT_DYNAMIC @0 x86_64 x86_64 x86_64 GNU/Linux
CPU: Intel(R) Xeon(R) Processor @ 2.10GHz
RAM: MemTotal:       16461176 kB
magus commit: 3659ef2bd3f00ea6f6c9bc26e700cbab444ff4de
```

### Tool versions

```
  hyperfine=1.18.0
  node=22.22.2
  pnpm=10.33.0
  turbo=2.9.14
  nx=22.7.3
  lage=2.15.12
  moon=2.2.5
  bazel=7.6.1
```

---

## Fixture: go (N=50)

### S1: Startup overhead (`--version`)

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make       | off        |     0.99 |         1 |           1 |   0.15 |        2 |   50 |
| magus      | off        |        6 |         7 |           7 |   0.39 |        8 |   50 |
| magus      | on         |        7 |         7 |           7 |   0.46 |        9 |   50 |

### S2: Project discovery

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus      | off        |       61 |        66 |          65 |      5 |       77 |   10 |

### S3: Affected dry-run (1 file changed)

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus      | off        |       39 |        41 |          40 |      2 |       44 |   10 |
| magus      | on         |       40 |        44 |          44 |      2 |       48 |   10 |

### S4: Cold build, parallel

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus      | off        |      534 |       587 |         588 |     34 |      627 |   10 |
| magus      | on         |      537 |       609 |         585 |     56 |      706 |   10 |
| make       | off        |     2968 |      3024 |        3011 |     40 |     3089 |   10 |

### S5: Warm cache replay

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make       | off        |        8 |         8 |           8 |   0.11 |        8 |   10 |
| magus      | off        |      191 |       199 |         196 |      8 |      217 |   10 |
| magus      | on         |      192 |       197 |         196 |      4 |      203 |   10 |

### S6: One leaf file changed

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make       | off        |      161 |       168 |         165 |     10 |      194 |   10 |
| magus      | off        |      221 |       234 |         233 |     10 |      250 |   10 |
| magus      | on         |      221 |       233 |         233 |      7 |      242 |   10 |

### S7: One upstream lib changed

| Tool | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ---- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus      | on         |      218 |       226 |         225 |      6 |      236 |   10 |
| magus      | off        |      220 |       229 |         228 |      7 |      243 |   10 |

