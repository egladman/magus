#!/usr/bin/env python3
"""Tests for the benchmark analysis pipeline, run against the synthetic fixture."""

from __future__ import annotations

import json
import shutil
import tempfile
import unittest
from pathlib import Path
from typing import Any

import analyze
import extract
import makefixture
import report
from records import (
    Analysis,
    ControlKind,
    ControlRun,
    GuardEvents,
    InvariantViolations,
    RunRecord,
    ScoredRun,
    TokenCounts,
    run_from_json,
)

HERE = Path(__file__).resolve().parent
PRICING = extract.load_pricing(HERE / "pricing.json")


def extract_fixture(results: Path) -> list[RunRecord]:
    return [extract.extract_run(run_dir, PRICING) for run_dir in sorted(results.iterdir())]


def scored(run: RunRecord) -> ScoredRun:
    assert isinstance(run, ScoredRun), run
    return run


def synthetic_record(
    arm: str, task: str, rep: int, dollars: float, tokens: int, success: bool = True
) -> ScoredRun:
    return ScoredRun(
        run_id=f"{arm}-{task}-r{rep}",
        arm=arm,
        task=task,
        rep=rep,
        model="claude-opus-5",
        dollars=dollars,
        table_dollars_usd=dollars,
        reported_cost_usd=None,
        cache_write_ttl_assumed=False,
        tokens=TokenCounts(
            input=tokens, output=0, cache_read=0, cache_write=0, total_billed=tokens
        ),
        turns=1,
        tool_calls=1,
        tool_calls_by_name={"Bash": 1},
        file_reads=1,
        distinct_files_read=1,
        re_read_rate=0.0,
        tool_result_bytes=10,
        guard_events=None,
        check_exit=0 if success else 1,
        success=success,
        invariant_violations=InvariantViolations(),
        wall_ms=1000,
        time_to_first_edit_ms=100,
        time_to_done_ms=900,
    )


def control_record(kind: ControlKind, success: bool) -> ControlRun:
    return ControlRun(
        run_id=f"rampant-task-a-r1-{kind}",
        arm="rampant",
        task="task-a",
        rep=1,
        model="claude-opus-5",
        control=kind,
        exit_reason=None,
        check_exit=0 if success else 1,
        success=success,
    )


class FixtureCase(unittest.TestCase):
    """Extraction against the synthetic tree, asserted on hand-computed totals."""

    @classmethod
    def setUpClass(cls) -> None:
        cls.tmp = Path(tempfile.mkdtemp(prefix="magus-bench-"))
        cls.results = makefixture.build(cls.tmp)
        cls.records = {run.run_id: run for run in extract_fixture(cls.results)}

    @classmethod
    def tearDownClass(cls) -> None:
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def record(self, arm: str, task: str, rep: int) -> ScoredRun:
        return scored(self.records[makefixture.run_id(arm, task, rep)])

    def test_run_count(self) -> None:
        self.assertEqual(len(self.records), 12)

    def test_token_counters(self) -> None:
        self.assertEqual(
            self.record("full", "task-a", 1).tokens,
            TokenCounts(
                input=4000, output=800, cache_read=32000, cache_write=4800, total_billed=41600
            ),
        )

    def test_token_counters_rampant(self) -> None:
        self.assertEqual(
            self.record("rampant", "task-a", 1).tokens,
            TokenCounts(
                input=10500, output=2100, cache_read=84000, cache_write=10500, total_billed=107100
            ),
        )

    def test_flat_cache_writes_price_at_the_5m_rate(self) -> None:
        record = self.record("full", "task-a", 1)
        self.assertTrue(record.cache_write_ttl_assumed)
        self.assertAlmostEqual(record.dollars, 0.086, places=9)

    def test_split_cache_writes_price_each_ttl(self) -> None:
        record = self.record("full", "task-b", 1)
        self.assertFalse(record.cache_write_ttl_assumed)
        self.assertEqual(record.tokens.cache_write, 4800)
        self.assertAlmostEqual(record.dollars, 0.092, places=9)

    def test_tool_and_read_accounting(self) -> None:
        record = self.record("full", "task-a", 1)
        self.assertEqual(record.turns, 4)
        self.assertEqual(record.tool_calls, 8)
        self.assertEqual(record.tool_calls_by_name, {"Bash": 4, "Read": 4})
        self.assertEqual(record.file_reads, 4)
        self.assertEqual(record.distinct_files_read, 4)
        self.assertEqual(record.re_read_rate, 0.0)
        self.assertEqual(record.tool_result_bytes, 4000)

    def test_re_read_rate_counts_repeat_reads(self) -> None:
        record = self.record("rampant", "task-a", 1)
        self.assertEqual(record.file_reads, 7)
        self.assertEqual(record.distinct_files_read, 3)
        self.assertAlmostEqual(record.re_read_rate, 4.0 / 7.0)

    def test_guard_events_present_and_zero_are_different_from_absent(self) -> None:
        self.assertEqual(
            self.record("full", "task-a", 1).guard_events,
            GuardEvents(denials=2, advisories=1, skill_loads=2),
        )
        self.assertEqual(
            self.record("rampant", "task-a", 1).guard_events,
            GuardEvents(denials=0, advisories=0, skill_loads=0),
        )
        self.assertIsNone(self.record("full", "task-a", 3).guard_events)

    def test_success_comes_from_check_exit(self) -> None:
        self.assertIs(self.record("rampant", "task-b", 2).success, False)
        self.assertEqual(self.record("rampant", "task-b", 2).check_exit, 1)
        self.assertIs(self.record("full", "task-b", 2).success, True)

    def test_missing_check_is_unknown_not_failure(self) -> None:
        with tempfile.TemporaryDirectory(prefix="magus-bench-nocheck-") as target:
            src = self.results / makefixture.run_id("full", "task-a", 2)
            dst = Path(target) / "run"
            shutil.copytree(src, dst)
            (dst / "check.exit").unlink()
            record = scored(extract.extract_run(dst, PRICING))
            self.assertIsNone(record.success)
            self.assertIsNone(record.check_exit)

    def test_deleted_test_file_is_an_invariant_violation(self) -> None:
        self.assertEqual(
            self.record("rampant", "task-a", 1).invariant_violations,
            InvariantViolations(tests_deleted=("src/app.test.ts",)),
        )
        self.assertEqual(
            self.record("full", "task-a", 1).invariant_violations, InvariantViolations()
        )

    def test_timing_is_carried_through(self) -> None:
        record = self.record("rampant", "task-a", 1)
        self.assertEqual(record.wall_ms, 241000)
        self.assertEqual(record.time_to_first_edit_ms, 60500)
        self.assertEqual(record.time_to_done_ms, 239000)

    def test_metrics_rows_round_trip_through_json(self) -> None:
        for record in self.records.values():
            line = json.dumps(record.to_json(), sort_keys=True)
            self.assertEqual(run_from_json(json.loads(line)), record)

    def test_scored_row_carries_a_null_control_key(self) -> None:
        # The reader dispatches on the key, so a scored row must carry it too.
        row = self.record("full", "task-a", 1).to_json()
        self.assertIn("control", row)
        self.assertIsNone(row["control"])


class TranscriptDefectCase(unittest.TestCase):
    """A transcript with no usage accounting must fail loudly, never report zeros."""

    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="magus-bench-defect-"))
        self.run = self.tmp / "run"
        self.run.mkdir()
        self.write_meta(
            {
                "run_id": "full-task-a-r1-x",
                "arm": "full",
                "task": "task-a",
                "rep": 1,
                "model": "claude-opus-5",
            }
        )

    def tearDown(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def write_meta(self, meta: dict[str, Any]) -> None:
        (self.run / "meta.json").write_text(json.dumps(meta), encoding="utf-8")

    def write_transcript(self, records: list[dict[str, Any]]) -> None:
        lines = "".join(json.dumps(record) + "\n" for record in records)
        (self.run / "transcript.jsonl").write_text(lines, encoding="utf-8")

    def extract_scored(self) -> ScoredRun:
        return scored(extract.extract_run(self.run, PRICING))

    def test_assistant_record_without_usage_raises(self) -> None:
        self.write_transcript(
            [
                {"type": "system", "subtype": "init", "model": "claude-opus-5"},
                {
                    "type": "assistant",
                    "message": {"id": "m1", "content": [{"type": "text", "text": "hi"}]},
                },
                {"type": "result", "subtype": "success"},
            ]
        )
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("usage", str(ctx.exception))

    def test_usage_without_token_counters_raises(self) -> None:
        self.write_transcript(
            [
                {
                    "type": "assistant",
                    "message": {"id": "m1", "usage": {"output_tokens": 5}, "content": []},
                }
            ]
        )
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("input_tokens", str(ctx.exception))

    def test_result_record_output_tokens_replace_the_chunk_sum(self) -> None:
        usage = {"input_tokens": 10, "output_tokens": 2, "cache_read_input_tokens": 100}
        self.write_transcript(
            [
                {"type": "system", "subtype": "init", "model": "claude-opus-5"},
                {"type": "assistant", "message": {"id": "m1", "usage": usage, "content": []}},
                {"type": "assistant", "message": {"id": "m2", "usage": usage, "content": []}},
                {
                    "type": "result",
                    "subtype": "success",
                    "usage": {
                        "input_tokens": 20,
                        "output_tokens": 900,
                        "cache_read_input_tokens": 200,
                    },
                },
            ]
        )
        record = self.extract_scored()
        self.assertEqual(record.tokens.output, 900)
        self.assertEqual(record.tokens.input, 20, "input still comes from the turns")
        self.assertEqual(record.tokens.cache_read, 200)

    def test_billed_cost_beats_the_table_when_the_host_recorded_one(self) -> None:
        usage = {"input_tokens": 1000, "output_tokens": 100}
        self.write_transcript(
            [
                {"type": "system", "subtype": "init", "model": "claude-opus-5"},
                {"type": "assistant", "message": {"id": "m1", "usage": usage, "content": []}},
                {
                    "type": "result",
                    "subtype": "success",
                    "total_cost_usd": 0.05,
                    "usage": dict(usage),
                },
            ]
        )
        record = self.extract_scored()
        self.assertEqual(record.dollars, 0.05)
        self.assertGreater(record.table_dollars_usd, 0)
        self.assertNotEqual(record.table_dollars_usd, 0.05)

    def test_transcript_with_no_assistant_records_raises(self) -> None:
        self.write_transcript([{"type": "system", "subtype": "init", "model": "claude-opus-5"}])
        with self.assertRaises(extract.ExtractError):
            extract.extract_run(self.run, PRICING)

    def write_single_turn(self, model: str, usage: dict[str, int]) -> None:
        self.write_transcript(
            [
                {
                    "type": "assistant",
                    "message": {"id": "m1", "model": model, "usage": usage, "content": []},
                }
            ]
        )

    def test_unpriced_model_raises(self) -> None:
        self.write_single_turn("some-unlisted-model", {"input_tokens": 10, "output_tokens": 2})
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("pricing table", str(ctx.exception))

    def test_dated_model_id_is_priced_as_its_alias(self) -> None:
        self.write_single_turn(
            "claude-opus-5-20260301", {"input_tokens": 1_000_000, "output_tokens": 0}
        )
        record = self.extract_scored()
        self.assertAlmostEqual(record.dollars, PRICING.models["claude-opus-5"].input)

    def test_dated_id_with_no_alias_still_raises(self) -> None:
        self.write_single_turn(
            "some-unlisted-model-20260301", {"input_tokens": 10, "output_tokens": 2}
        )
        with self.assertRaises(extract.ExtractError):
            extract.extract_run(self.run, PRICING)

    def test_scored_run_without_a_transcript_raises(self) -> None:
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("transcript.jsonl is missing", str(ctx.exception))

    def test_control_run_carries_its_verdict_and_no_tokens(self) -> None:
        # Neither control writes a transcript, so the run must be recognized
        # from meta.json alone rather than from what is missing beside it, and
        # its check verdict is what the report's Controls section reads.
        meta = json.loads((self.run / "meta.json").read_text(encoding="utf-8"))
        self.write_meta({**meta, "control": "golden", "exit_reason": "control_golden_ok"})
        (self.run / "check.exit").write_text("0\n", encoding="utf-8")
        record = extract.extract_run(self.run, PRICING)
        self.assertIsInstance(record, ControlRun)
        self.assertEqual(record.control, ControlKind.GOLDEN)
        self.assertTrue(record.success)
        self.assertNotIn("tokens", record.to_json())

    def test_unknown_control_kind_raises(self) -> None:
        meta = json.loads((self.run / "meta.json").read_text(encoding="utf-8"))
        self.write_meta({**meta, "control": "placebo"})
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("placebo", str(ctx.exception))

    def test_controls_must_discriminate_before_a_pass_rate_means_anything(self) -> None:
        scored_runs: list[RunRecord] = [
            synthetic_record("rampant", "task-a", 1, 1.0, 100),
            synthetic_record("full", "task-a", 1, 1.0, 100),
        ]
        golden_ok = control_record(ControlKind.GOLDEN, True)
        null_fails = control_record(ControlKind.NULL, False)
        null_passes = control_record(ControlKind.NULL, True)
        good = analyze.analyze(scored_runs + [golden_ok, null_fails], 1)
        self.assertTrue(good.controls["task-a"].discriminates)
        self.assertEqual(good.runs, 2, "controls are not scored runs")

        lax = analyze.analyze(scored_runs + [golden_ok, null_passes], 1)
        self.assertFalse(lax.controls["task-a"].discriminates)

        unverified = analyze.analyze(scored_runs, 1)
        self.assertFalse(unverified.controls["task-a"].discriminates)
        self.assertEqual(unverified.controls["task-a"].golden.n, 0)


class StatisticsCase(unittest.TestCase):
    def test_wilson_bounds_against_known_values(self) -> None:
        # Reference values are tabulated to five decimals (z = 1.959964); the
        # sixth decimal differs by rounding of z alone, so five places is the
        # precision the table supports.
        low, high = analyze.wilson_interval(0, 10)
        self.assertAlmostEqual(low, 0.0, places=5)
        self.assertAlmostEqual(high, 0.27753, places=5)
        low, high = analyze.wilson_interval(5, 10)
        self.assertAlmostEqual(low, 0.23659, places=5)
        self.assertAlmostEqual(high, 0.76341, places=5)
        low, high = analyze.wilson_interval(10, 10)
        self.assertAlmostEqual(low, 0.72247, places=5)
        self.assertAlmostEqual(high, 1.0, places=5)
        self.assertEqual(analyze.wilson_interval(0, 0), (None, None))

    def test_cost_of_pass_is_infinite_at_a_zero_pass_rate(self) -> None:
        records = [
            synthetic_record("rampant", "task-a", rep, 0.5, 100, success=False) for rep in (1, 2, 3)
        ]
        summary = analyze.arm_summary(records)
        self.assertEqual(summary.pass_rate, 0.0)
        self.assertTrue(summary.cost_of_pass_usd.infinite)
        self.assertIsNone(summary.cost_of_pass_usd.value)

    def test_cost_of_pass_divides_mean_dollars_by_pass_rate(self) -> None:
        records = [
            synthetic_record("full", "task-a", 1, 1.0, 100),
            synthetic_record("full", "task-a", 2, 2.0, 100),
            synthetic_record("full", "task-a", 3, 3.0, 100, success=False),
        ]
        summary = analyze.arm_summary(records)
        self.assertAlmostEqual(summary.dollars.mean, 2.0)
        self.assertAlmostEqual(summary.pass_rate, 2.0 / 3.0)
        self.assertAlmostEqual(summary.cost_of_pass_usd.value, 3.0)

    def test_deltas_pair_rep_i_with_rep_i(self) -> None:
        # Per-rep values differ widely but each pair differs by exactly 10, so a
        # pairing that ignored the rep field would not produce a zero-width CI.
        records: list[RunRecord] = []
        for rep, base in ((1, 100), (2, 200), (3, 300)):
            records.append(synthetic_record("rampant", "task-a", rep, base / 100.0, base))
        for rep, base in ((3, 310), (1, 110), (2, 210)):
            records.append(synthetic_record("full", "task-a", rep, base / 100.0, base))
        row = analyze.analyze(records, seed=7).paired["total_billed_tokens"]["task-a"]
        self.assertEqual(row.n_pairs, 3)
        self.assertEqual(row.delta_mean, 10.0)
        self.assertEqual(row.delta_median, 10.0)
        self.assertEqual(row.ci_low, 10.0)
        self.assertEqual(row.ci_high, 10.0)

    def test_verdict_needs_both_a_clean_ci_and_a_ten_percent_delta(self) -> None:
        records: list[RunRecord] = []
        for rep, base in ((1, 1000), (2, 1010), (3, 1020)):
            records.append(synthetic_record("rampant", "task-a", rep, 1.0, base))
            records.append(synthetic_record("full", "task-a", rep, 1.0, base - 20))
        row = analyze.analyze(records, seed=7).paired["total_billed_tokens"]["task-a"]
        self.assertTrue(row.ci_excludes_zero)
        self.assertLess(abs(row.relative), analyze.MIN_RELATIVE_DELTA)
        self.assertEqual(row.verdict, "inconclusive")

    def test_verdict_fires_when_the_delta_is_large_and_clean(self) -> None:
        records: list[RunRecord] = []
        for rep, base in ((1, 1000), (2, 1100), (3, 1200)):
            records.append(synthetic_record("rampant", "task-a", rep, 1.0, base))
            records.append(synthetic_record("full", "task-a", rep, 0.5, base // 2))
        row = analyze.analyze(records, seed=7).paired["total_billed_tokens"]["task-a"]
        self.assertEqual(row.verdict, "lower under full")

    def test_holm_adjustment_scales_with_the_number_of_tasks(self) -> None:
        adjusted = analyze.holm({"task-a": 0.01, "task-b": 0.04, "task-c": None})
        self.assertAlmostEqual(adjusted["task-a"], 0.02)
        self.assertAlmostEqual(adjusted["task-b"], 0.04)
        self.assertIsNone(adjusted["task-c"])

    def test_holm_stays_monotone(self) -> None:
        adjusted = analyze.holm({"a": 0.03, "b": 0.031, "c": 0.032})
        self.assertLessEqual(adjusted["a"], adjusted["b"])
        self.assertLessEqual(adjusted["b"], adjusted["c"])

    def test_describe_reports_spread_beside_the_mean(self) -> None:
        spread = analyze.describe([4.0, None, 1.0, 3.0, 2.0])
        self.assertEqual(spread.n, 4)
        self.assertEqual(spread.median, 2.5)
        self.assertEqual(spread.iqr, (1.75, 3.25))
        self.assertEqual(spread.mean, 2.5)
        self.assertEqual(analyze.describe([None]).n, 0)


class DeterminismCase(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="magus-bench-det-"))
        self.records = extract_fixture(makefixture.build(self.tmp))

    def tearDown(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def analysis_json(self, records: list[RunRecord], seed: int) -> str:
        return json.dumps(analyze.analyze(records, seed).to_json(), sort_keys=True)

    def test_same_seed_gives_identical_output(self) -> None:
        self.assertEqual(self.analysis_json(self.records, 42), self.analysis_json(self.records, 42))

    def test_record_order_does_not_change_the_analysis(self) -> None:
        forward = self.analysis_json(self.records, 42)
        backward = self.analysis_json(list(reversed(self.records)), 42)
        self.assertEqual(forward, backward)

    def test_the_point_estimate_does_not_depend_on_the_seed(self) -> None:
        one = analyze.analyze(self.records, seed=1).paired["dollars"]["task-a"]
        two = analyze.analyze(self.records, seed=2).paired["dollars"]["task-a"]
        self.assertEqual(one.delta_mean, two.delta_mean)
        for row in (one, two):
            self.assertLessEqual(row.ci_low, row.delta_mean)
            self.assertLessEqual(row.delta_mean, row.ci_high)

    def test_analysis_round_trips_through_json(self) -> None:
        analysis = analyze.analyze(self.records, seed=42)
        self.assertEqual(Analysis.from_json(json.loads(json.dumps(analysis.to_json()))), analysis)

    def test_report_caveats_are_generated_from_the_data(self) -> None:
        analysis = analyze.analyze(self.records, seed=42)
        text = report.render(analysis)
        self.assertIn("cost-of-pass", text)
        self.assertIn("pass^k", text)
        self.assertIn("null, not zero", text)
        self.assertIn(makefixture.run_id("full", "task-a", 3), text)
        self.assertIn("test file deleted", text)
        self.assertIn("n=3", text)

    def test_report_is_the_same_from_memory_and_from_disk(self) -> None:
        analysis = analyze.analyze(self.records, seed=42)
        path = self.tmp / "analysis.json"
        path.write_text(json.dumps(analysis.to_json(), sort_keys=True), encoding="utf-8")
        self.assertEqual(report.render(report.load_analysis(path)), report.render(analysis))


if __name__ == "__main__":
    unittest.main()
