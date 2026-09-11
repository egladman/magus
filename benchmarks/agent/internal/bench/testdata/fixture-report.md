# Harness-effectiveness benchmark

12 runs, 2 task(s), arms full and rampant, model(s) claude-opus-5, bootstrap seed 20260902.

## Controls

Whether each task's check can tell a solution from its absence: the golden control applies the known solution and must pass, the null control touches nothing and must fail. A pass rate below is only worth reading where both hold.

| task   | golden (pass/n) | null (pass/n) | checks discriminate          |
| ------ | --------------- | ------------- | ---------------------------- |
| task-a | 0/0             | 0/0           | UNVERIFIED (control missing) |
| task-b | 0/0             | 0/0           | UNVERIFIED (control missing) |

## Headline: cost-of-pass

Expected dollars per correct solution (mean dollars / pass rate).

| arm     | n | passes | pass rate | Wilson 95%     | mean $  | median $ | cost-of-pass |
| ------- | - | ------ | --------- | -------------- | ------- | -------- | ------------ |
| full    | 6 | 6      | 100%      | [0.610, 1.000] | $0.0890 | $0.0890  | $0.0890      |
| rampant | 6 | 5      | 83%       | [0.436, 0.970] | $0.2161 | $0.2161  | $0.2594      |

## Correctness against median tokens

The cost-accuracy frontier in text: pass rate beside the token spend it cost.

| arm     | task   | n | pass@1 | median tokens | median tokens (passes only) | median $ |
| ------- | ------ | - | ------ | ------------- | --------------------------- | -------- |
| full    | task-a | 3 | 100%   | 42000         | 42000                       | $0.0880  |
| full    | task-b | 3 | 100%   | 42000         | 42000                       | $0.0900  |
| rampant | task-a | 3 | 100%   | 107800        | 107800                      | $0.2161  |
| rampant | task-b | 3 | 67%    | 107800        | 107800                      | $0.2161  |

## Paired deltas, full minus rampant

Rep i of one arm is paired with rep i of the other. CI is a seeded 10000-sample bootstrap of the mean paired delta; p is Holm-adjusted across tasks. A verdict needs the CI to exclude zero AND the delta to reach 10% of the rampant median.

### total billed tokens

| task   | pairs | median delta | mean delta | 95% CI               | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | -------------------- | -------- | -------- | ---------------- |
| task-a | 3     | -65800       | -65800.0   | [-66100.0, -65500.0] | -61%     | 0.000    | lower under full |
| task-b | 3     | -65800       | -65800.0   | [-66100.0, -65500.0] | -61%     | 0.000    | lower under full |

### dollars

| task   | pairs | median delta | mean delta | 95% CI             | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | ------------------ | -------- | -------- | ---------------- |
| task-a | 3     | -0.1281      | -0.1281    | [-0.1296, -0.1266] | -59%     | 0.000    | lower under full |
| task-b | 3     | -0.1281      | -0.1261    | [-0.1296, -0.1206] | -58%     | 0.000    | lower under full |

### wall clock (ms)

| task   | pairs | median delta | mean delta | 95% CI                 | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | ---------------------- | -------- | -------- | ---------------- |
| task-a | 3     | -120000      | -120000.0  | [-120000.0, -120000.0] | -50%     | 0.000    | lower under full |
| task-b | 3     | -120000      | -120000.0  | [-120000.0, -120000.0] | -50%     | 0.000    | lower under full |

### turns

| task   | pairs | median delta | mean delta | 95% CI       | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | ------------ | -------- | -------- | ---------------- |
| task-a | 3     | -3           | -3.0       | [-3.0, -3.0] | -43%     | 0.000    | lower under full |
| task-b | 3     | -3           | -3.0       | [-3.0, -3.0] | -43%     | 0.000    | lower under full |

### tool calls

| task   | pairs | median delta | mean delta | 95% CI       | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | ------------ | -------- | -------- | ---------------- |
| task-a | 3     | -6           | -6.0       | [-6.0, -6.0] | -43%     | 0.000    | lower under full |
| task-b | 3     | -6           | -6.0       | [-6.0, -6.0] | -43%     | 0.000    | lower under full |

### file reads

| task   | pairs | median delta | mean delta | 95% CI       | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | ------------ | -------- | -------- | ---------------- |
| task-a | 3     | -3           | -3.0       | [-3.0, -3.0] | -43%     | 0.000    | lower under full |
| task-b | 3     | -3           | -3.0       | [-3.0, -3.0] | -43%     | 0.000    | lower under full |

### re-read rate

| task   | pairs | median delta | mean delta | 95% CI             | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | ------------------ | -------- | -------- | ---------------- |
| task-a | 3     | -0.5714      | -0.5714    | [-0.5714, -0.5714] | -100%    | 0.000    | lower under full |
| task-b | 3     | -0.5714      | -0.5714    | [-0.5714, -0.5714] | -100%    | 0.000    | lower under full |

### tool result bytes

| task   | pairs | median delta | mean delta | 95% CI             | relative | p (Holm) | verdict          |
| ------ | ----- | ------------ | ---------- | ------------------ | -------- | -------- | ---------------- |
| task-a | 3     | -8600        | -8600.0    | [-8600.0, -8600.0] | -68%     | 0.000    | lower under full |
| task-b | 3     | -8600        | -8600.0    | [-8600.0, -8600.0] | -68%     | 0.000    | lower under full |

## pass@1 and pass^k

pass@1 is capability; pass^k (all k reps succeed) is reliability.

| arm     | task   | k | pass@1 | Wilson 95%     | pass^k |
| ------- | ------ | - | ------ | -------------- | ------ |
| full    | task-a | 3 | 100%   | [0.439, 1.000] | 1      |
| full    | task-b | 3 | 100%   | [0.439, 1.000] | 1      |
| rampant | task-a | 3 | 100%   | [0.439, 1.000] | 1      |
| rampant | task-b | 3 | 67%    | [0.208, 0.939] | 0      |

## Caveats

- Reps per cell: full/task-a n=3, full/task-b n=3, rampant/task-a n=3, rampant/task-b n=3.
- Under the 5-rep protocol: full/task-a, full/task-b, rampant/task-a, rampant/task-b. Treat those deltas as directional.
- No activity trail for 1 run(s) (full-task-a-r3-20260909T120000Z); their guard_events are null, not zero.
- Cache-write TTL was not reported for 11 run(s) (full-task-a-r1-20260909T120000Z, full-task-a-r2-20260909T120000Z, full-task-a-r3-20260909T120000Z, full-task-b-r2-20260909T120000Z, full-task-b-r3-20260909T120000Z, rampant-task-a-r1-20260909T120000Z, rampant-task-a-r2-20260909T120000Z, rampant-task-a-r3-20260909T120000Z, rampant-task-b-r1-20260909T120000Z, rampant-task-b-r2-20260909T120000Z, rampant-task-b-r3-20260909T120000Z); those writes are priced at the 5-minute rate, so their dollars are a floor.
- Invariant violation (test file deleted) in rampant-task-a-r1-20260909T120000Z.
- No billed cost for 12 run(s); their dollars come from the pricing table.
- Checks not shown to discriminate for task-a, task-b (see Controls); pass rates there are not evidence.
