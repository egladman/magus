# Harness-effectiveness benchmark

Published 2026-09-10T15:28:56Z from `results`: 6 scored run(s), model claude-sonnet-5 (anthropic), magus v0.4.3-76-g63871b145 (63871b145) built 2026-09-09T22:55:47-04:00, host darwin/arm64/v8.

6 runs, 1 task(s), arms full and rampant, model(s) claude-sonnet-5, bootstrap seed 20260902.

## Controls

Whether each task's check can tell a solution from its absence: the golden control applies the known solution and must pass, the null control touches nothing and must fail. A pass rate below is only worth reading where both hold.

| task               | golden (pass/n) | null (pass/n) | checks discriminate |
| ------------------ | --------------- | ------------- | ------------------- |
| merge-config-falsy | 1/1             | 0/1           | yes                 |

## Headline: cost-of-pass

Expected dollars per correct solution (mean dollars / pass rate).

| arm     | n | passes | pass rate | Wilson 95%     | mean $  | median $ | cost-of-pass |
| ------- | - | ------ | --------- | -------------- | ------- | -------- | ------------ |
| full    | 3 | 3      | 100%      | [0.439, 1.000] | $0.4441 | $0.3996  | $0.4441      |
| rampant | 3 | 3      | 100%      | [0.439, 1.000] | $0.2196 | $0.2120  | $0.2196      |

## Correctness against median tokens

The cost-accuracy frontier in text: pass rate beside the token spend it cost.

| arm     | task               | n | pass@1 | median tokens | median tokens (passes only) | median $ |
| ------- | ------------------ | - | ------ | ------------- | --------------------------- | -------- |
| full    | merge-config-falsy | 3 | 100%   | 701714        | 701714                      | $0.3996  |
| rampant | merge-config-falsy | 3 | 100%   | 386919        | 386919                      | $0.2120  |

## Paired deltas, full minus rampant

Rep i of one arm is paired with rep i of the other. CI is a seeded 10000-sample bootstrap of the mean paired delta; p is Holm-adjusted across tasks. A verdict needs the CI to exclude zero AND the delta to reach 10% of the rampant median.

### total billed tokens

| task               | pairs | median delta | mean delta | 95% CI               | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | -------------------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 236268       | 355402.3   | [200339.0, 629600.0] | 92%      | 0.000    | higher under full |

### dollars

| task               | pairs | median delta | mean delta | 95% CI           | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ---------------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 0.1385       | 0.2245     | [0.1382, 0.3968] | 106%     | 0.000    | higher under full |

### wall clock (ms)

| task               | pairs | median delta | mean delta | 95% CI             | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ------------------ | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 35916        | 39241.7    | [21857.0, 59952.0] | 147%     | 0.000    | higher under full |

### turns

| task               | pairs | median delta | mean delta | 95% CI     | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ---------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 3            | 4.3        | [1.0, 9.0] | 39%      | 0.000    | higher under full |

### tool calls

| task               | pairs | median delta | mean delta | 95% CI     | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | ---------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 3            | 4.3        | [1.0, 9.0] | 43%      | 0.000    | higher under full |

### file reads

| task               | pairs | median delta | mean delta | 95% CI      | relative | p (Holm) | verdict      |
| ------------------ | ----- | ------------ | ---------- | ----------- | -------- | -------- | ------------ |
| merge-config-falsy | 3     | 0            | -0.3       | [-1.0, 0.0] | -17%     | 0.585    | inconclusive |

### re-read rate

| task               | pairs | median delta | mean delta | 95% CI           | relative | p (Holm) | verdict      |
| ------------------ | ----- | ------------ | ---------- | ---------------- | -------- | -------- | ------------ |
| merge-config-falsy | 3     | 0.0000       | 0.0000     | [0.0000, 0.0000] | n/a      | 1.000    | inconclusive |

### tool result bytes

| task               | pairs | median delta | mean delta | 95% CI          | relative | p (Holm) | verdict           |
| ------------------ | ----- | ------------ | ---------- | --------------- | -------- | -------- | ----------------- |
| merge-config-falsy | 3     | 905          | 1364.3     | [756.0, 2432.0] | 35%      | 0.000    | higher under full |

## pass@1 and pass^k

pass@1 is capability; pass^k (all k reps succeed) is reliability.

| arm     | task               | k | pass@1 | Wilson 95%     | pass^k |
| ------- | ------------------ | - | ------ | -------------- | ------ |
| full    | merge-config-falsy | 3 | 100%   | [0.439, 1.000] | 1      |
| rampant | merge-config-falsy | 3 | 100%   | [0.439, 1.000] | 1      |

## Caveats

- Reps per cell: full/merge-config-falsy n=3, rampant/merge-config-falsy n=3.
- Under the 5-rep protocol: full/merge-config-falsy, rampant/merge-config-falsy. Treat those deltas as directional.
- No activity trail for 3 run(s) (rampant-merge-config-falsy-r1-20260910T121032Z, rampant-merge-config-falsy-r2-20260910T121116Z, rampant-merge-config-falsy-r3-20260910T121152Z); their guard_events are null, not zero.
- Dollars are the host's billed cost; the pricing table would have said 0.67x that, so the table is wrong for this model and only backs runs with no result record.
