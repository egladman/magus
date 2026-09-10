#!/usr/bin/env python3
"""Tests for the benchmark analysis pipeline, run against the synthetic fixture."""

import json
import os
import shutil
import tempfile
import unittest

import analyze
import extract
import makefixture
import report

HERE = os.path.dirname(os.path.abspath(__file__))
PRICING = extract.load_pricing(os.path.join(HERE, "pricing.json"))


def run_dir(results, arm, task, rep):
    return os.path.join(results, makefixture.run_id(arm, task, rep))


class FixtureCase(unittest.TestCase):
    """Extraction against the synthetic tree, asserted on hand-computed totals."""

    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.mkdtemp(prefix="magus-bench-")
        cls.results = makefixture.build(cls.tmp)
        cls.records = {
            r["run_id"]: r
            for r in (
                extract.extract_run(os.path.join(cls.results, name), PRICING)
                for name in sorted(os.listdir(cls.results))
            )
        }

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp, ignore_errors=True)

    def record(self, arm, task, rep):
        return self.records[makefixture.run_id(arm, task, rep)]

    def test_run_count(self):
        self.assertEqual(len(self.records), 12)

    def test_token_counters(self):
        tokens = self.record("full", "task-a", 1)["tokens"]
        self.assertEqual(tokens["input"], 4000)
        self.assertEqual(tokens["output"], 800)
        self.assertEqual(tokens["cache_read"], 32000)
        self.assertEqual(tokens["cache_write"], 4800)
        self.assertEqual(tokens["total_billed"], 41600)

    def test_token_counters_rampant(self):
        tokens = self.record("rampant", "task-a", 1)["tokens"]
        self.assertEqual(tokens["input"], 10500)
        self.assertEqual(tokens["output"], 2100)
        self.assertEqual(tokens["cache_read"], 84000)
        self.assertEqual(tokens["cache_write"], 10500)
        self.assertEqual(tokens["total_billed"], 107100)

    def test_flat_cache_writes_price_at_the_5m_rate(self):
        record = self.record("full", "task-a", 1)
        self.assertTrue(record["cache_write_ttl_assumed"])
        self.assertAlmostEqual(record["dollars"], 0.086, places=9)

    def test_split_cache_writes_price_each_ttl(self):
        record = self.record("full", "task-b", 1)
        self.assertFalse(record["cache_write_ttl_assumed"])
        self.assertEqual(record["tokens"]["cache_write"], 4800)
        self.assertAlmostEqual(record["dollars"], 0.092, places=9)

    def test_tool_and_read_accounting(self):
        record = self.record("full", "task-a", 1)
        self.assertEqual(record["turns"], 4)
        self.assertEqual(record["tool_calls"], 8)
        self.assertEqual(record["tool_calls_by_name"], {"Bash": 4, "Read": 4})
        self.assertEqual(record["file_reads"], 4)
        self.assertEqual(record["distinct_files_read"], 4)
        self.assertEqual(record["re_read_rate"], 0.0)
        self.assertEqual(record["tool_result_bytes"], 4000)

    def test_re_read_rate_counts_repeat_reads(self):
        record = self.record("rampant", "task-a", 1)
        self.assertEqual(record["file_reads"], 7)
        self.assertEqual(record["distinct_files_read"], 3)
        self.assertAlmostEqual(record["re_read_rate"], 4.0 / 7.0)

    def test_guard_events_present_and_zero_are_different_from_absent(self):
        self.assertEqual(
            self.record("full", "task-a", 1)["guard_events"],
            {"denials": 2, "advisories": 1, "skill_loads": 2},
        )
        self.assertEqual(
            self.record("rampant", "task-a", 1)["guard_events"],
            {"denials": 0, "advisories": 0, "skill_loads": 0},
        )
        self.assertIsNone(self.record("full", "task-a", 3)["guard_events"])

    def test_success_comes_from_check_exit(self):
        self.assertIs(self.record("rampant", "task-b", 2)["success"], False)
        self.assertEqual(self.record("rampant", "task-b", 2)["check_exit"], 1)
        self.assertIs(self.record("full", "task-b", 2)["success"], True)

    def test_missing_check_is_unknown_not_failure(self):
        target = tempfile.mkdtemp(prefix="magus-bench-nocheck-")
        try:
            src = run_dir(self.results, "full", "task-a", 2)
            dst = os.path.join(target, "run")
            shutil.copytree(src, dst)
            os.remove(os.path.join(dst, "check.exit"))
            record = extract.extract_run(dst, PRICING)
            self.assertIsNone(record["success"])
            self.assertIsNone(record["check_exit"])
        finally:
            shutil.rmtree(target, ignore_errors=True)

    def test_deleted_test_file_is_an_invariant_violation(self):
        self.assertEqual(
            self.record("rampant", "task-a", 1)["invariant_violations"],
            {"tests_deleted": ["src/app.test.ts"]},
        )
        self.assertEqual(
            self.record("full", "task-a", 1)["invariant_violations"], {"tests_deleted": []}
        )

    def test_timing_is_carried_through(self):
        record = self.record("rampant", "task-a", 1)
        self.assertEqual(record["wall_ms"], 241000)
        self.assertEqual(record["time_to_first_edit_ms"], 60500)
        self.assertEqual(record["time_to_done_ms"], 239000)


class TranscriptDefectCase(unittest.TestCase):
    """A transcript with no usage accounting must fail loudly, never report zeros."""

    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="magus-bench-defect-")
        self.run = os.path.join(self.tmp, "run")
        os.makedirs(self.run)
        with open(os.path.join(self.run, "meta.json"), "w", encoding="utf-8") as fh:
            json.dump(
                {
                    "run_id": "full-task-a-r1-x",
                    "arm": "full",
                    "task": "task-a",
                    "rep": 1,
                    "model": "claude-opus-5",
                },
                fh,
            )

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def write_transcript(self, records):
        with open(os.path.join(self.run, "transcript.jsonl"), "w", encoding="utf-8") as fh:
            for record in records:
                fh.write(json.dumps(record) + "\n")

    def test_assistant_record_without_usage_raises(self):
        self.write_transcript(
            [
                {"type": "system", "subtype": "init", "model": "claude-opus-5"},
                {"type": "assistant", "message": {"id": "m1", "content": [{"type": "text",
                                                                           "text": "hi"}]}},
                {"type": "result", "subtype": "success"},
            ]
        )
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("usage", str(ctx.exception))

    def test_usage_without_token_counters_raises(self):
        self.write_transcript(
            [
                {"type": "assistant", "message": {"id": "m1", "usage": {"output_tokens": 5},
                                                  "content": []}},
            ]
        )
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("input_tokens", str(ctx.exception))

    def test_result_record_output_tokens_replace_the_chunk_sum(self):
        usage = {"input_tokens": 10, "output_tokens": 2, "cache_read_input_tokens": 100}
        self.write_transcript(
            [
                {"type": "system", "subtype": "init", "model": "claude-opus-5"},
                {"type": "assistant", "message": {"id": "m1", "usage": usage, "content": []}},
                {"type": "assistant", "message": {"id": "m2", "usage": usage, "content": []}},
                {"type": "result", "subtype": "success",
                 "usage": {"input_tokens": 20, "output_tokens": 900,
                           "cache_read_input_tokens": 200}},
            ]
        )
        record = extract.extract_run(self.run, PRICING)
        self.assertEqual(record["tokens"]["output"], 900)
        self.assertEqual(record["tokens"]["input"], 20, "input still comes from the turns")
        self.assertEqual(record["tokens"]["cache_read"], 200)

    def test_transcript_with_no_assistant_records_raises(self):
        self.write_transcript([{"type": "system", "subtype": "init", "model": "claude-opus-5"}])
        with self.assertRaises(extract.ExtractError):
            extract.extract_run(self.run, PRICING)

    def test_unpriced_model_raises(self):
        self.write_transcript(
            [
                {
                    "type": "assistant",
                    "message": {
                        "id": "m1",
                        "model": "some-unlisted-model",
                        "usage": {"input_tokens": 10, "output_tokens": 2},
                        "content": [],
                    },
                }
            ]
        )
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("pricing table", str(ctx.exception))

    def test_dated_model_id_is_priced_as_its_alias(self):
        self.write_transcript(
            [
                {
                    "type": "assistant",
                    "message": {
                        "id": "m1",
                        "model": "claude-opus-5-20260301",
                        "usage": {"input_tokens": 1_000_000, "output_tokens": 0},
                        "content": [],
                    },
                }
            ]
        )
        record = extract.extract_run(self.run, PRICING)
        self.assertAlmostEqual(record["dollars"], PRICING["claude-opus-5"]["input"])

    def test_dated_id_with_no_alias_still_raises(self):
        self.write_transcript(
            [
                {
                    "type": "assistant",
                    "message": {
                        "id": "m1",
                        "model": "some-unlisted-model-20260301",
                        "usage": {"input_tokens": 10, "output_tokens": 2},
                        "content": [],
                    },
                }
            ]
        )
        with self.assertRaises(extract.ExtractError):
            extract.extract_run(self.run, PRICING)

    def test_scored_run_without_a_transcript_raises(self):
        with self.assertRaises(extract.ExtractError) as ctx:
            extract.extract_run(self.run, PRICING)
        self.assertIn("transcript.jsonl is missing", str(ctx.exception))

    def test_control_run_carries_its_verdict_and_no_tokens(self):
        # Neither control writes a transcript, so the run must be recognized
        # from meta.json alone rather than from what is missing beside it, and
        # its check verdict is what the report's Controls section reads.
        with open(os.path.join(self.run, "meta.json"), "r+", encoding="utf-8") as fh:
            meta = json.load(fh)
            meta.update({"control": "golden", "exit_reason": "control_golden_ok"})
            fh.seek(0)
            fh.truncate()
            json.dump(meta, fh)
        with open(os.path.join(self.run, "check.exit"), "w", encoding="utf-8") as fh:
            fh.write("0\n")
        record = extract.extract_run(self.run, PRICING)
        self.assertEqual(record["control"], "golden")
        self.assertTrue(record["success"])
        self.assertNotIn("tokens", record)

    def test_controls_must_discriminate_before_a_pass_rate_means_anything(self):
        def control(kind, success):
            return {
                "run_id": "rampant-task-a-r1-%s" % kind,
                "arm": "rampant",
                "task": "task-a",
                "rep": 1,
                "model": "claude-opus-5",
                "control": kind,
                "success": success,
            }

        scored = [
            synthetic_record("rampant", "task-a", 1, 1.0, 100),
            synthetic_record("full", "task-a", 1, 1.0, 100),
        ]
        good = analyze.analyze(scored + [control("golden", True), control("null", False)], 1)
        self.assertTrue(good["controls"]["task-a"]["discriminates"])
        self.assertEqual(good["runs"], 2, "controls are not scored runs")

        lax = analyze.analyze(scored + [control("golden", True), control("null", True)], 1)
        self.assertFalse(lax["controls"]["task-a"]["discriminates"])

        unverified = analyze.analyze(scored, 1)
        self.assertFalse(unverified["controls"]["task-a"]["discriminates"])
        self.assertEqual(unverified["controls"]["task-a"]["golden"]["n"], 0)


def synthetic_record(arm, task, rep, dollars, tokens, success=True):
    return {
        "run_id": "%s-%s-r%d" % (arm, task, rep),
        "arm": arm,
        "task": task,
        "rep": rep,
        "model": "claude-opus-5",
        "dollars": dollars,
        "tokens": {
            "input": tokens,
            "output": 0,
            "cache_read": 0,
            "cache_write": 0,
            "total_billed": tokens,
        },
        "turns": 1,
        "tool_calls": 1,
        "tool_calls_by_name": {"Bash": 1},
        "file_reads": 1,
        "distinct_files_read": 1,
        "re_read_rate": 0.0,
        "tool_result_bytes": 10,
        "guard_events": None,
        "check_exit": 0 if success else 1,
        "success": success,
        "invariant_violations": {"tests_deleted": []},
        "wall_ms": 1000,
        "time_to_first_edit_ms": 100,
        "time_to_done_ms": 900,
    }


class StatisticsCase(unittest.TestCase):
    def test_wilson_bounds_against_known_values(self):
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

    def test_cost_of_pass_is_infinite_at_a_zero_pass_rate(self):
        records = [
            synthetic_record("rampant", "task-a", rep, 0.5, 100, success=False)
            for rep in (1, 2, 3)
        ]
        summary = analyze.arm_summary(records)
        self.assertEqual(summary["pass_rate"], 0.0)
        self.assertTrue(summary["cost_of_pass_usd"]["infinite"])
        self.assertIsNone(summary["cost_of_pass_usd"]["value"])

    def test_cost_of_pass_divides_mean_dollars_by_pass_rate(self):
        records = [
            synthetic_record("full", "task-a", 1, 1.0, 100),
            synthetic_record("full", "task-a", 2, 2.0, 100),
            synthetic_record("full", "task-a", 3, 3.0, 100, success=False),
        ]
        summary = analyze.arm_summary(records)
        self.assertAlmostEqual(summary["dollars"]["mean"], 2.0)
        self.assertAlmostEqual(summary["pass_rate"], 2.0 / 3.0)
        self.assertAlmostEqual(summary["cost_of_pass_usd"]["value"], 3.0)

    def test_deltas_pair_rep_i_with_rep_i(self):
        # Per-rep values differ widely but each pair differs by exactly 10, so a
        # pairing that ignored the rep field would not produce a zero-width CI.
        records = []
        for rep, base in ((1, 100), (2, 200), (3, 300)):
            records.append(synthetic_record("rampant", "task-a", rep, base / 100.0, base))
        for rep, base in ((3, 310), (1, 110), (2, 210)):
            records.append(synthetic_record("full", "task-a", rep, base / 100.0, base))
        result = analyze.analyze(records, seed=7)
        row = result["paired"]["total_billed_tokens"]["task-a"]
        self.assertEqual(row["n_pairs"], 3)
        self.assertEqual(row["delta_mean"], 10.0)
        self.assertEqual(row["delta_median"], 10.0)
        self.assertEqual(row["ci_low"], 10.0)
        self.assertEqual(row["ci_high"], 10.0)

    def test_verdict_needs_both_a_clean_ci_and_a_ten_percent_delta(self):
        records = []
        for rep, base in ((1, 1000), (2, 1010), (3, 1020)):
            records.append(synthetic_record("rampant", "task-a", rep, 1.0, base))
            records.append(synthetic_record("full", "task-a", rep, 1.0, base - 20))
        result = analyze.analyze(records, seed=7)
        row = result["paired"]["total_billed_tokens"]["task-a"]
        self.assertTrue(row["ci_excludes_zero"])
        self.assertLess(abs(row["relative"]), analyze.MIN_RELATIVE_DELTA)
        self.assertEqual(row["verdict"], "inconclusive")

    def test_verdict_fires_when_the_delta_is_large_and_clean(self):
        records = []
        for rep, base in ((1, 1000), (2, 1100), (3, 1200)):
            records.append(synthetic_record("rampant", "task-a", rep, 1.0, base))
            records.append(synthetic_record("full", "task-a", rep, 0.5, base // 2))
        result = analyze.analyze(records, seed=7)
        row = result["paired"]["total_billed_tokens"]["task-a"]
        self.assertEqual(row["verdict"], "lower under full")

    def test_holm_adjustment_scales_with_the_number_of_tasks(self):
        adjusted = analyze.holm({"task-a": 0.01, "task-b": 0.04, "task-c": None})
        self.assertAlmostEqual(adjusted["task-a"], 0.02)
        self.assertAlmostEqual(adjusted["task-b"], 0.04)
        self.assertIsNone(adjusted["task-c"])

    def test_holm_stays_monotone(self):
        adjusted = analyze.holm({"a": 0.03, "b": 0.031, "c": 0.032})
        self.assertLessEqual(adjusted["a"], adjusted["b"])
        self.assertLessEqual(adjusted["b"], adjusted["c"])


class DeterminismCase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.mkdtemp(prefix="magus-bench-det-")
        self.results = makefixture.build(self.tmp)
        self.records = [
            extract.extract_run(os.path.join(self.results, name), PRICING)
            for name in sorted(os.listdir(self.results))
        ]

    def tearDown(self):
        shutil.rmtree(self.tmp, ignore_errors=True)

    def test_same_seed_gives_identical_output(self):
        first = json.dumps(analyze.analyze(self.records, seed=42), sort_keys=True)
        second = json.dumps(analyze.analyze(self.records, seed=42), sort_keys=True)
        self.assertEqual(first, second)

    def test_record_order_does_not_change_the_analysis(self):
        forward = json.dumps(analyze.analyze(self.records, seed=42), sort_keys=True)
        backward = json.dumps(analyze.analyze(list(reversed(self.records)), seed=42),
                              sort_keys=True)
        self.assertEqual(forward, backward)

    def test_the_point_estimate_does_not_depend_on_the_seed(self):
        one = analyze.analyze(self.records, seed=1)["paired"]["dollars"]["task-a"]
        two = analyze.analyze(self.records, seed=2)["paired"]["dollars"]["task-a"]
        self.assertEqual(one["delta_mean"], two["delta_mean"])
        for row in (one, two):
            self.assertLessEqual(row["ci_low"], row["delta_mean"])
            self.assertLessEqual(row["delta_mean"], row["ci_high"])

    def test_report_caveats_are_generated_from_the_data(self):
        analysis = analyze.analyze(self.records, seed=42)
        text = report.render(analysis)
        self.assertIn("cost-of-pass", text)
        self.assertIn("pass^k", text)
        self.assertIn("null, not zero", text)
        self.assertIn(makefixture.run_id("full", "task-a", 3), text)
        self.assertIn("test file deleted", text)
        self.assertIn("n=3", text)


if __name__ == "__main__":
    unittest.main()
