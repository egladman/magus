# magus benchmarks

Head-to-head: magus vs turbo, nx, lage, moon, bazel, make.

## Environment

```
Date: 2026-05-25T13:35:00Z
Go: go1.24.7
Kernel: Linux vm 6.18.5 #2 SMP PREEMPT_DYNAMIC Wed Jan 14 17:56:08 UTC 2026 x86_64 x86_64 x86_64 GNU/Linux
CPU: Intel(R) Xeon(R) Processor @ 2.80GHz
RAM: MemTotal:       16466560 kB
magus commit: b1a0c67f441d945d4a018c8dfb9b8709399473dc
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

### S1 — Startup overhead (`--version`)

| Tool            | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| --------------- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make            | off    |        1 |         1 |           1 |   0.15 |        2 |   50 |
| magus-gopherlua | on     |        7 |         7 |           7 |   0.50 |        9 |   50 |
| magus-gopherlua | off    |        7 |         8 |           8 |   0.85 |       10 |   50 |
| magus-luajit    | on     |        7 |         8 |           8 |   0.49 |        9 |   50 |
| magus-luajit    | off    |        7 |         8 |           8 |   0.77 |       12 |   50 |

### S2 — Project discovery

| Tool            | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| --------------- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus-luajit    | off    |       12 |        13 |          13 |   0.47 |       14 |   20 |
| magus-gopherlua | off    |       22 |        23 |          23 |      1 |       29 |   20 |

### S3 — Affected dry-run (1 file changed)

| Tool            | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| --------------- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus-luajit    | on     |        9 |        10 |          10 |   0.90 |       13 |   20 |
| magus-gopherlua | on     |       10 |        11 |          10 |      1 |       15 |   20 |
| magus-luajit    | off    |       13 |        15 |          15 |      1 |       18 |   20 |
| magus-gopherlua | off    |       24 |        26 |          25 |      2 |       31 |   20 |

### S4 — Cold build, parallel

| Tool            | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| --------------- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus-gopherlua | on     |        8 |         8 |           8 |   0.47 |       10 |   20 |
| magus-luajit    | on     |        8 |         8 |           8 |   0.46 |        9 |   20 |
| make            | off    |     3537 |      3623 |        3625 |     52 |     3715 |   20 |
| magus-gopherlua | off    |     3572 |      3716 |        3654 |    132 |     4027 |   20 |
| magus-luajit    | off    |     3830 |      3900 |        3887 |     48 |     3989 |   20 |

### S5 — Warm cache replay

| Tool            | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| --------------- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make            | off    |        7 |         8 |           8 |   0.34 |        9 |   20 |
| magus-gopherlua | on     |        7 |         8 |           8 |   0.49 |        9 |   20 |
| magus-luajit    | on     |        8 |         9 |           9 |   0.38 |        9 |   20 |
| magus-luajit    | off    |       31 |        34 |          34 |      2 |       39 |   20 |
| magus-gopherlua | off    |       37 |        41 |          41 |      2 |       47 |   20 |

### S6 — One leaf file changed

| Tool            | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| --------------- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus-gopherlua | on     |        8 |         9 |           9 |   0.46 |       10 |   20 |
| magus-luajit    | on     |        8 |         9 |           9 |   0.53 |       10 |   20 |
| make            | off    |      188 |       206 |         204 |      9 |      229 |   20 |
| magus-gopherlua | off    |      273 |       318 |         297 |     54 |      444 |   20 |
| magus-luajit    | off    |      296 |       318 |         318 |     12 |      342 |   20 |

### S7 — One upstream lib changed

| Tool            | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| --------------- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus-gopherlua | on     |        8 |         9 |           9 |   0.71 |       11 |   20 |
| magus-luajit    | on     |        8 |         9 |           9 |   0.40 |        9 |   20 |
| magus-gopherlua | off    |      275 |       299 |         298 |     10 |      323 |   20 |
| magus-luajit    | off    |      282 |       302 |         299 |     15 |      329 |   20 |
