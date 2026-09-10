# Harness-effectiveness benchmark

Published 2026-09-10T15:28:58Z from `results-opus`: 6 scored run(s), model claude-opus-5 (anthropic), magus v0.4.3-76-g63871b145 (63871b145) built 2026-09-09T22:55:47-04:00, host darwin/arm64/v8.

6 runs, 1 task(s), arms full and rampant, model(s) claude-opus-5, bootstrap seed 20260902.

## Controls

Whether each task's check can tell a solution from its absence: the golden control applies the known solution and must pass, the null control touches nothing and must fail. A pass rate below is only worth reading where both hold.

| task               | golden (pass/n) | null (pass/n) | checks discriminate |
| ------------------ | --------------- | ------------- | ------------------- |
| merge-config-falsy | 1/1             | 0/1           | yes                 |

## Headline: cost-of-pass

Expected dollars per correct solution (mean dollars / pass rate).

| arm     | n | passes | pass rate | Wilson 95%     | mean $  | median $ | cost-of-pass |
| ------- | - | ------ | --------- | -------------- | ------- | -------- | ------------ |
| full    | 3 | 3      | 100%      | [0.439, 1.000] | $0.3285 | $0.3618  | $0.3285      |
| rampant | 3 | 3      | 100%      | [0.439, 1.000] | $0.1692 | $0.1696  | $0.1692      |

## Correctness against median tokens

The cost-accuracy frontier in text: pass rate beside the token spend it cost.

| arm     | task               | n | pass@1 | median tokens | median tokens (passes only) | median $ |
| ------- | ------------------ | - | ------ | ------------- | --------------------------- | -------- |
| full    | merge-config-falsy | 3 | 100%   | 457080        | 457080                      | $0.3618  |
| rampant | merge-config-falsy | 3 | 100%   | 243363        | 243363                      | $0.1696  |

## Paired deltas, full minus rampant

Rep i of one arm is paired with rep i of the other. CI is a seeded 10000-sample bootstrap of the mean paired delta; p is Holm-adjusted across tasks. A verdict needs the CI to exclude zero AND the delta to reach 10% of the rampant median.

### total billed tokens

| task               | pairs | median delta | mean delta | 95% CI              | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ------------------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 191649       | 199084.3   | [85095.0, 320509.0] | 82%      | 0.000    | higher under full |

### dollars

| task               | pairs | median delta | mean delta | 95% CI           | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ---------------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 0.1689       | 0.1592     | [0.0810, 0.2277] | 94%      | 0.000    | higher under full |

### wall clock (ms)

| task               | pairs | median delta | mean delta | 95% CI             | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ------------------ | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 37897        | 35769.7    | [21736.0, 47676.0] | 84%      | 0.000    | higher under full |

### turns

| task               | pairs | median delta | mean delta | 95% CI     | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ---------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 4            | 4.0        | [1.0, 7.0] | 40%      | 0.000    | higher under full |

### tool calls

| task               | pairs | median delta | mean delta | 95% CI     | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ---------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 4            | 4.0        | [1.0, 7.0] | 40%      | 0.000    | higher under full |

### file reads

| task               | pairs | median delta | mean delta | 95% CI     | relative | p (Holm) | verdict      |
| ------------------ | ----- | ------------ | ---------- | ---------- | -------- | -------- | ------------ |
| merge-config-falsy | 3     | 2            | 1.7        | [0.0, 3.0] | 83%      | 0.068    | inconclusive |

### re-read rate

| task               | pairs | median delta | mean delta | 95% CI           | relative | p (Holm) | verdict      |
| ------------------ | ----- | ------------ | ---------- | ---------------- | -------- | -------- | ------------ |
| merge-config-falsy | 3     | 0.0000       | 0.0000     | [0.0000, 0.0000] | n/a      | 1.000    | inconclusive |

### tool result bytes

| task               | pairs | median delta | mean delta | 95% CI             | relative | p (Holm) | verdict      |
| ------------------ | ----- | ------------ | ---------- | ------------------ | -------- | -------- | ------------ |
| merge-config-falsy | 3     | -1451        | 3725.3     | [-2423.0, 15050.0] | 64%      | 0.592    | inconclusive |

## pass@1 and pass^k

pass@1 is capability; pass^k (all k reps succeed) is reliability.

| arm     | task               | k | pass@1 | Wilson 95%     | pass^k |
| ------- | ------------------ | - | ------ | -------------- | ------ |
| full    | merge-config-falsy | 3 | 100%   | [0.439, 1.000] | 1      |
| rampant | merge-config-falsy | 3 | 100%   | [0.439, 1.000] | 1      |

## Caveats

- Reps per cell: full/merge-config-falsy n=3, rampant/merge-config-falsy n=3.
- Under the 5-rep protocol: full/merge-config-falsy, rampant/merge-config-falsy. Treat those deltas as directional.
- No activity trail for 3 run(s) (rampant-merge-config-falsy-r1-20260910T122636Z, rampant-merge-config-falsy-r2-20260910T122732Z, rampant-merge-config-falsy-r3-20260910T122814Z); their guard_events are null, not zero.
- Dollars are the host's billed cost; the pricing table would have said 1.66x that, so the table is wrong for this model and only backs runs with no result record.
