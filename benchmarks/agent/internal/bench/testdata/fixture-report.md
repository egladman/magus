# Harness-effectiveness benchmark

12 runs, 2 task(s), arms full and rampant, model(s) claude-opus-5, randomization seed 20260902.

Computed on fixture.

## Controls

Whether each task's check can tell a solution from its absence: the golden control applies the known solution and must pass, the null control touches nothing and must fail. A pass rate below is only worth reading where both hold.

| task   | golden (pass/n) | null (pass/n) | checks discriminate          |
| ------ | --------------- | ------------- | ---------------------------- |
| task-a | 0/0             | 0/0           | UNVERIFIED (control missing) |
| task-b | 0/0             | 0/0           | UNVERIFIED (control missing) |

## Headline: pass@1 and cost-of-pass

pass@1 is the mean of per-task pass rates, so each task weighs the same; its interval is a t interval over those rates (n/a under 2 tasks). Cost-of-pass is the dollars of every graded run over the passes among them.

| arm     | tasks | passes | pass@1 | 95% CI over tasks | mean $  | median $ | cost-of-pass |
| ------- | ----- | ------ | ------ | ----------------- | ------- | -------- | ------------ |
| full    | 2     | 6 of 6 | 100%   | [1.000, 1.000]    | $0.0890 | $0.0890  | $0.0890      |
| rampant | 2     | 5 of 6 | 83%    | [0.000, 1.000]    | $0.2161 | $0.2161  | $0.2594      |

## Pass rate beside spend

Each cell's pass@1, with its passes of its graded runs, beside the median tokens and dollars it spent.

| arm     | task   | n | pass@1        | median tokens | median tokens (passes only) | median $ |
| ------- | ------ | - | ------------- | ------------- | --------------------------- | -------- |
| full    | task-a | 3 | 100% (3 of 3) | 42000         | 42000                       | $0.0880  |
| full    | task-b | 3 | 100% (3 of 3) | 42000         | 42000                       | $0.0900  |
| rampant | task-a | 3 | 100% (3 of 3) | 107800        | 107800                      | $0.2161  |
| rampant | task-b | 3 | 67% (2 of 3)  | 107800        | 107800                      | $0.2161  |

## Full minus rampant, paired by task

The task is the unit: each task's mean under full is paired with its mean under rampant. p is a two-sided sign-flip test over the tasks, exact up to 16 tasks, and Holm-adjusted across pass@1, total billed tokens, dollars and wall clock. CI is the paired t interval. A verdict needs p (Holm) at or under 0.05 AND a difference of 10% of the rampant mean; every other metric is descriptive.

- pass@1: inconclusive over 2 task(s), mean difference 0.167, p (Holm) 1; 2 task(s): the smallest p an exact test can reach is 0.5, above 0.05; this many tasks detect a difference of 2.848 at 80% power.
- total billed tokens: inconclusive over 2 task(s), mean difference -65800.0, p (Holm) 1; 2 task(s): the smallest p an exact test can reach is 0.5, above 0.05.
- dollars: inconclusive over 2 task(s), mean difference -0.1271, p (Holm) 1; 2 task(s): the smallest p an exact test can reach is 0.5, above 0.05; this many tasks detect a difference of 0.0171 at 80% power.
- wall clock (ms): inconclusive over 2 task(s), mean difference -120000.0, p (Holm) 1; 2 task(s): the smallest p an exact test can reach is 0.5, above 0.05.

| metric              | tasks | mean delta | 95% CI                 | relative | p   | p (Holm) | smallest attainable p | detectable at 80% power | d_z    | verdict      |
| ------------------- | ----- | ---------- | ---------------------- | -------- | --- | -------- | --------------------- | ----------------------- | ------ | ------------ |
| pass@1              | 2     | 0.167      | [-1.951, 2.284]        | 20%      | 1   | 1        | 0.5                   | 2.848                   | 0.71   | inconclusive |
| total billed tokens | 2     | -65800.0   | [-65800.0, -65800.0]   | -61%     | 0.5 | 1        | 0.5                   | n/a                     | n/a    | inconclusive |
| dollars             | 2     | -0.1271    | [-0.1398, -0.1144]     | -59%     | 0.5 | 1        | 0.5                   | 0.0171                  | -89.89 | inconclusive |
| wall clock (ms)     | 2     | -120000.0  | [-120000.0, -120000.0] | -50%     | 0.5 | 1        | 0.5                   | n/a                     | n/a    | inconclusive |
| turns               | 2     | -3.0       | [-3.0, -3.0]           | -43%     | 0.5 | n/a      | 0.5                   | n/a                     | n/a    | descriptive  |
| tool calls          | 2     | -6.0       | [-6.0, -6.0]           | -43%     | 0.5 | n/a      | 0.5                   | n/a                     | n/a    | descriptive  |
| file reads          | 2     | -3.0       | [-3.0, -3.0]           | -43%     | 0.5 | n/a      | 0.5                   | n/a                     | n/a    | descriptive  |
| re-read rate        | 2     | -0.5714    | [-0.5714, -0.5714]     | -100%    | 0.5 | n/a      | 0.5                   | n/a                     | n/a    | descriptive  |
| tool result bytes   | 2     | -8600.0    | [-8600.0, -8600.0]     | -68%     | 0.5 | n/a      | 0.5                   | n/a                     | n/a    | descriptive  |

## pass@k and pass^k

pass@k is the chance at least one of k runs passes, a capability; pass^k the chance all k pass, a reliability (Chen et al. 2021; tau-bench). Both are the unbiased estimates from c passes of n graded runs, and an arm's row averages its tasks at a k every task reached.

| arm     | task                  | pass@1        | Wilson 95%     | pass@k                       | pass^k                       |
| ------- | --------------------- | ------------- | -------------- | ---------------------------- | ---------------------------- |
| full    | all (mean over tasks) | 100%          | n/a            | k=1 1.00, k=2 1.00, k=3 1.00 | k=1 1.00, k=2 1.00, k=3 1.00 |
| rampant | all (mean over tasks) | 83%           | n/a            | k=1 0.83, k=2 1.00, k=3 1.00 | k=1 0.83, k=2 0.67, k=3 0.50 |
| full    | task-a                | 100% (3 of 3) | [0.439, 1.000] | k=1 1.00, k=2 1.00, k=3 1.00 | k=1 1.00, k=2 1.00, k=3 1.00 |
| full    | task-b                | 100% (3 of 3) | [0.439, 1.000] | k=1 1.00, k=2 1.00, k=3 1.00 | k=1 1.00, k=2 1.00, k=3 1.00 |
| rampant | task-a                | 100% (3 of 3) | [0.439, 1.000] | k=1 1.00, k=2 1.00, k=3 1.00 | k=1 1.00, k=2 1.00, k=3 1.00 |
| rampant | task-b                | 67% (2 of 3)  | [0.208, 0.939] | k=1 0.67, k=2 1.00, k=3 1.00 | k=1 0.67, k=2 0.33, k=3 0.00 |

## Answers: precision and recall

For interrogation tasks, micro-averaged from the summed true positives, false positives and false negatives of each graded answer.

| arm     | task   | answers | tp | fp | fn | precision | recall | F1    |
| ------- | ------ | ------- | -- | -- | -- | --------- | ------ | ----- |
| full    | all    | 3       | 12 | 0  | 3  | 1.000     | 0.800  | 0.889 |
| rampant | all    | 3       | 9  | 6  | 6  | 0.600     | 0.600  | 0.600 |
| full    | task-b | 3       | 12 | 0  | 3  | 1.000     | 0.800  | 0.889 |
| rampant | task-b | 3       | 9  | 6  | 6  | 0.600     | 0.600  | 0.600 |

## Caveats

- Reps per cell: full/task-a n=3, full/task-b n=3, rampant/task-a n=3, rampant/task-b n=3.
- Under the 5-rep protocol: full/task-a, full/task-b, rampant/task-a, rampant/task-b. Treat those per-task rates as directional.
- Only 2 task(s) ran under both arms, so no comparison can reach p 0.05: an exact test over them bottoms out at 0.5.
- No activity trail for 1 run(s) (full-task-a-r3-20260909T120000Z); their guard_events are null, not zero.
- Cache-write TTL was not reported for 11 run(s) (full-task-a-r1-20260909T120000Z, full-task-a-r2-20260909T120000Z, full-task-a-r3-20260909T120000Z, full-task-b-r2-20260909T120000Z, full-task-b-r3-20260909T120000Z, rampant-task-a-r1-20260909T120000Z, rampant-task-a-r2-20260909T120000Z, rampant-task-a-r3-20260909T120000Z, rampant-task-b-r1-20260909T120000Z, rampant-task-b-r2-20260909T120000Z, rampant-task-b-r3-20260909T120000Z); those writes are priced at the 5-minute rate, so their dollars are a floor.
- Invariant violation (test file deleted) in rampant-task-a-r1-20260909T120000Z.
- No self-reported cost for 12 run(s); the table prices every run either way.
- Checks not shown to discriminate for task-a, task-b (see Controls); pass rates there are not evidence.
