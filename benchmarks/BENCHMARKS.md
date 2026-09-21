# magus benchmarks

Measured in this run: magus, make, on the go fixture(s).
A tool absent from the tables below produced no results here and is
not being compared. A row marked FAILED exited non-zero and is not a
measurement of the work the scenario describes.

## Environment

```text
Date: 2026-09-20T17:21:27Z
Go: go1.26.6-X:jsonv2
Kernel: Darwin Elis-MacBook-Air.local 25.6.0 Darwin Kernel Version 25.6.0: Fri Jul 31 19:16:20 PDT 2026; root:xnu-12377.161.14~5/RELEASE_ARM64_T8142 arm64
CPU: Apple M5
CPU cores: 10
RAM: 25165824 kB
magus commit: 9d6003dc787d93dfe2427472d8df62e3cc53a40e
```

### Tool versions (observed)

```text
hyperfine: hyperfine 1.19.0
magus: magus v0.4.3-48-g9d6003dc7 (9d6003dc7) built 2026-09-20T12:17:26-05:00
make (gmake): GNU Make 4.4.1
```

---

## Fixture: go (N=50)

### S1: Startup overhead (`--version`)

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus | on     |     0.00 |      0.40 |        0.18 |   0.57 |        2 |   50 |
| make  | off    |        1 |         2 |           2 |   0.19 |        2 |   50 |
| magus | off    |        8 |         8 |           8 |   0.29 |        9 |   50 |

### S2: Project discovery

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus | off    |      103 |       104 |         104 |   0.64 |      105 |   10 |

### S3: Affected dry-run (1 file changed)

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| magus | on     |       37 |        39 |          39 |      1 |       41 |   10 |
| magus | off    |       88 |       108 |          96 |     43 |      229 |   10 |

### S4: Cold build, parallel

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make  | off    |     1319 |      1366 |        1366 |     27 |     1398 |   10 |
| magus | on     |     2807 |      2852 |        2841 |     36 |     2928 |   10 |
| magus | off    |     3033 |      3066 |        3059 |     34 |     3155 |   10 |

### S5: Warm cache replay

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make  | off    |        5 |         5 |           5 |   0.24 |        6 |   10 |
| magus | off    |     1381 |      1392 |        1392 |     10 |     1413 |   10 |
| magus | on     |     1705 |      1758 |        1728 |     69 |     1914 |   10 |

### S6: One leaf file changed

| Tool  | Daemon | min (ms) | mean (ms) | median (ms) | stddev | p99 (ms) | runs |
| ----- | ------ | -------: | --------: | ----------: | -----: | -------: | ---: |
| make  | off    |      106 |       109 |         109 |      2 |      113 |   10 |
| magus | off    |     1401 |      1440 |        1423 |     67 |     1627 |   10 |
| magus | on     |     1780 |      1865 |        1813 |    104 |     2082 |   10 |
