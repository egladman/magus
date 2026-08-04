// Analyze paired full-versus-simple skill-eval results.
//
// Usage: node tools/analyze.mjs <results.json>

import { readFileSync } from "node:fs";
import process from "node:process";

const BOOTSTRAPS = 10_000;
const SEED = 20260731;

// Deterministic PRNG (mulberry32) so the bootstrap is reproducible run to run.
// The contract is determinism for a given input, not any particular stream.
function rng(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function rowsFrom(document) {
  if (Array.isArray(document)) {
    return document;
  }
  return document.results ?? document.data ?? [];
}

function value(row, name) {
  return row[name] ?? row.metadata?.[name] ?? row.vars?.[name] ?? row.config?.[name];
}

function assertionPassed(assertion) {
  return Boolean(assertion.passed ?? assertion.success ?? assertion.pass ?? false);
}

function rowScore(row) {
  const assertions = row.assertions ?? row.constraint_results ?? [];
  if (assertions.length === 0) {
    throw new Error(`result for task ${JSON.stringify(value(row, "task_id") ?? null)} has no assertions`);
  }
  const passed = assertions.filter(assertionPassed).length;
  return [passed / assertions.length, passed === assertions.length ? 1 : 0, assertions];
}

function mean(values) {
  return values.reduce((sum, v) => sum + v, 0) / values.length;
}

function percentile(values, fraction) {
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.round((sorted.length - 1) * fraction)];
}

function bootstrapInterval(deltas) {
  const random = rng(SEED);
  const draws = [];
  for (let i = 0; i < BOOTSTRAPS; i++) {
    let sum = 0;
    for (let j = 0; j < deltas.length; j++) {
      sum += deltas[Math.floor(random() * deltas.length)];
    }
    draws.push(sum / deltas.length);
  }
  const lower = percentile(draws, 0.025);
  const upper = percentile(draws, 0.975);
  const nonpositive = draws.filter((draw) => draw <= 0).length / draws.length;
  const nonnegative = draws.filter((draw) => draw >= 0).length / draws.length;
  const pvalue = Math.min(1.0, 2 * Math.min(nonpositive, nonnegative));
  return [lower, upper, pvalue];
}

function verdict(delta, lower, upper) {
  if ((lower > 0 || upper < 0) && Math.abs(delta) >= 0.1) {
    return "REAL";
  }
  return "NOT DISTINGUISHABLE";
}

function holm(comparisons) {
  const ranked = comparisons.map((comparison, index) => ({ index, comparison }));
  ranked.sort((a, b) => a.comparison.pvalue - b.comparison.pvalue);
  const rejected = new Array(comparisons.length).fill(false);
  let stillRejecting = true;
  ranked.forEach(({ index, comparison }, rank) => {
    const threshold = 0.05 / (comparisons.length - rank);
    if (stillRejecting && comparison.pvalue <= threshold) {
      rejected[index] = true;
    } else {
      stillRejecting = false;
    }
  });
  comparisons.forEach((comparison, index) => {
    comparison.holm_reject = rejected[index];
    comparison.adjusted_verdict =
      comparison.raw_verdict === "REAL" && rejected[index] ? "REAL" : "NOT DISTINGUISHABLE";
  });
}

export function analyze(rows) {
  const grouped = new Map();
  for (const row of rows) {
    const skill = value(row, "skill");
    let permutation = value(row, "permutation");
    let model = value(row, "model") ?? row.provider;
    if (!permutation && typeof model === "string" && model.includes("/")) {
      [permutation, model] = [model.slice(0, model.indexOf("/")), model.slice(model.indexOf("/") + 1)];
    }
    const taskId = value(row, "task_id") ?? value(row, "id");
    const trial = value(row, "trial") ?? 0;
    if (!skill || !permutation || !model || !taskId) {
      throw new Error("each result needs skill, permutation, model, and task_id metadata");
    }
    const key = JSON.stringify([skill, permutation, model, taskId, String(trial)]);
    if (!grouped.has(key)) {
      grouped.set(key, []);
    }
    grouped.get(key).push(rowScore(row));
  }

  const trialScores = new Map();
  const guardrails = new Map();
  for (const [key, values] of grouped) {
    const [skill, , model] = JSON.parse(key);
    trialScores.set(key, [mean(values.map((v) => v[0])), mean(values.map((v) => v[1]))]);
    const guardKey = JSON.stringify([skill, model]);
    if (!guardrails.has(guardKey)) {
      guardrails.set(guardKey, { runs: 0, skill_triggered: 0, never_ran_violations: 0 });
    }
    const counter = guardrails.get(guardKey);
    counter.runs += 1;
    const assertions = values.flatMap((v) => v[2]);
    if (assertions.some((a) => (a.id ?? "").startsWith("used-skill:") && assertionPassed(a))) {
      counter.skill_triggered += 1;
    }
    counter.never_ran_violations += assertions.filter(
      (a) => (a.id ?? "").startsWith("never-ran:") && !assertionPassed(a),
    ).length;
  }

  const taskScores = new Map();
  for (const [key, score] of trialScores) {
    const [skill, permutation, model, taskId] = JSON.parse(key);
    const taskKey = JSON.stringify([skill, model, taskId, permutation]);
    if (!taskScores.has(taskKey)) {
      taskScores.set(taskKey, []);
    }
    taskScores.get(taskKey).push(score);
  }
  const means = new Map();
  for (const [taskKey, values] of taskScores) {
    means.set(taskKey, [mean(values.map((v) => v[0])), mean(values.map((v) => v[1]))]);
  }

  const byComparison = new Map();
  for (const [taskKey, score] of means) {
    const [skill, model, taskId, permutation] = JSON.parse(taskKey);
    const comparisonKey = JSON.stringify([skill, model]);
    if (!byComparison.has(comparisonKey)) {
      byComparison.set(comparisonKey, new Map());
    }
    byComparison.get(comparisonKey).set(JSON.stringify([taskId, permutation]), score);
  }

  const comparisons = [];
  for (const comparisonKey of [...byComparison.keys()].sort()) {
    const [skill, model] = JSON.parse(comparisonKey);
    const taskMap = byComparison.get(comparisonKey);
    const taskIds = [...new Set([...taskMap.keys()].map((k) => JSON.parse(k)[0]))].sort();
    const paired = [];
    const fullInstruction = [];
    const simpleInstruction = [];
    const fullPrompt = [];
    const simplePrompt = [];
    for (const taskId of taskIds) {
      const full = taskMap.get(JSON.stringify([taskId, "full"]));
      const simple = taskMap.get(JSON.stringify([taskId, "simple"]));
      if (full === undefined || simple === undefined) {
        continue;
      }
      paired.push(simple[0] - full[0]);
      fullInstruction.push(full[0]);
      simpleInstruction.push(simple[0]);
      fullPrompt.push(full[1]);
      simplePrompt.push(simple[1]);
    }
    if (paired.length === 0) {
      continue;
    }
    const delta = mean(paired);
    const [lower, upper, pvalue] = bootstrapInterval(paired);
    const counter = guardrails.get(JSON.stringify([skill, model]));
    comparisons.push({
      skill,
      model,
      n_tasks: paired.length,
      full_instruction: mean(fullInstruction),
      simple_instruction: mean(simpleInstruction),
      full_prompt: mean(fullPrompt),
      simple_prompt: mean(simplePrompt),
      delta,
      lower,
      upper,
      pvalue,
      raw_verdict: verdict(delta, lower, upper),
      skill_trigger_rate: counter.skill_triggered / counter.runs,
      never_ran_violations: counter.never_ran_violations,
    });
  }
  holm(comparisons);
  return comparisons;
}

function signed(number) {
  return `${number < 0 ? "" : "+"}${number.toFixed(3)}`;
}

function main() {
  const path = process.argv[2];
  if (!path) {
    throw new Error("usage: node tools/analyze.mjs <results.json>");
  }
  const comparisons = analyze(rowsFrom(JSON.parse(readFileSync(path, "utf8"))));
  console.log("skill model full simple prompt-full prompt-simple delta 95% CI n raw holm trigger never-ran");
  for (const result of comparisons) {
    console.log(
      `${result.skill} ${result.model} ${result.full_instruction.toFixed(3)} ` +
        `${result.simple_instruction.toFixed(3)} ${result.full_prompt.toFixed(3)} ` +
        `${result.simple_prompt.toFixed(3)} ${signed(result.delta)} ` +
        `[${signed(result.lower)}, ${signed(result.upper)}] ${result.n_tasks} ` +
        `${result.raw_verdict} ${result.adjusted_verdict} ` +
        `${result.skill_trigger_rate.toFixed(3)} ${result.never_ran_violations}`,
    );
  }
}

if (process.argv[1] === new URL(import.meta.url).pathname) {
  try {
    main();
  } catch (error) {
    process.stderr.write(`analyze: ${error.message}\n`);
    process.exit(1);
  }
}
