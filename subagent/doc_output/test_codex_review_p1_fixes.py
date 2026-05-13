"""Regression tests for the two P1 findings from codex review of the chart_publisher series.

P1-1: IdeaRefiner.is_idea_refine_needed() was force-triggering idea-refine when
      raw_content is empty, even if input_sources was non-empty — breaking
      `--input-sources X --repos Y` CLI runs.

P1-2: ParallelFetcher.fetch_all() was dropping the `type` field from each
      fetcher's return value, causing ContentMerger._group_by_type() to bucket
      everything under 'unknown' and render empty docs.

Run with: python3 -m unittest test_codex_review_p1_fixes.py
"""

from __future__ import annotations

import unittest

from doc_output import IdeaRefiner
from parallel_fetch import ParallelFetcher


class TestIdeaRefineGating(unittest.TestCase):
    """P1-1 regression."""

    def test_empty_raw_content_with_input_sources_skips_idea_refine(self):
        """The repro from codex review: no raw_content but input_sources non-empty.

        Before fix: returned True → CLI run stops at idea-refine and never fetches.
        After fix: returns False → falls through to fetch/render/publish.
        """
        refiner = IdeaRefiner(
            raw_content="",
            doc_type="tech_review",
            input_sources=[{"type": "code_repository", "repos": [{"path": "."}]}],
        )
        self.assertFalse(refiner.is_idea_refine_needed())

    def test_empty_raw_content_and_no_input_sources_still_triggers_idea_refine(self):
        """Genuine empty input → still routes through idea-refine (pre-fix behavior preserved)."""
        refiner = IdeaRefiner(
            raw_content="",
            doc_type="tech_review",
            input_sources=[],
        )
        self.assertTrue(refiner.is_idea_refine_needed())

    def test_explicit_idea_refine_doctype_always_triggers(self):
        """doc_type='idea_refine' takes precedence over input_sources gate."""
        refiner = IdeaRefiner(
            raw_content="",
            doc_type="idea_refine",
            input_sources=[{"type": "direct", "content": "x"}],
        )
        self.assertTrue(refiner.is_idea_refine_needed())

    def test_rough_idea_mark_always_triggers(self):
        """orchestrator_mark.requirement_type=rough_idea takes precedence too."""
        refiner = IdeaRefiner(
            raw_content="",
            doc_type="tech_review",
            input_sources=[{"type": "direct", "content": "x"}],
            orchestrator_mark={"requirement_type": "rough_idea"},
        )
        self.assertTrue(refiner.is_idea_refine_needed())

    def test_default_input_sources_param_is_empty_list_not_none(self):
        """Constructor without input_sources kw should not crash on truthy/iteration checks."""
        refiner = IdeaRefiner(raw_content="some content here that is long enough to skip refine " * 5)
        # input_sources should be a list (empty), not None
        self.assertEqual(refiner.input_sources, [])


class TestParallelFetcherTypePreservation(unittest.TestCase):
    """P1-2 regression."""

    def test_direct_input_result_carries_type(self):
        fetcher = ParallelFetcher(
            input_sources=[{"type": "direct", "content": "HELLO"}],
            workspace="/tmp",
        )
        results = fetcher.fetch_all()
        # Key shape: 'direct_0'
        self.assertEqual(len(results), 1)
        entry = next(iter(results.values()))
        self.assertEqual(entry["status"], "success")
        self.assertEqual(entry["type"], "direct")
        self.assertIn("HELLO", entry["content"])

    def test_multiple_source_types_each_preserve_their_type(self):
        fetcher = ParallelFetcher(
            input_sources=[
                {"type": "direct", "content": "A"},
                {"type": "direct", "content": "B"},
            ],
            workspace="/tmp",
        )
        results = fetcher.fetch_all()
        self.assertEqual(len(results), 2)
        for entry in results.values():
            self.assertEqual(entry["status"], "success")
            self.assertEqual(entry["type"], "direct")

    def test_content_merger_can_group_by_type_after_fix(self):
        """End-to-end: with type preserved, ContentMerger groups correctly."""
        from content_merger import ContentMerger

        fetcher = ParallelFetcher(
            input_sources=[{"type": "direct", "content": "MERGER_PROOF"}],
            workspace="/tmp",
        )
        results = fetcher.fetch_all()
        merger = ContentMerger(results)
        merged = merger.merge()

        # Pre-fix: overview would have been empty because type='unknown' filtered out.
        # Post-fix: 'direct' content is grouped and included.
        self.assertIn("MERGER_PROOF", merged["overview"])


class TestM2IdeaRefineUnder200WordsBranch(unittest.TestCase):
    """M2: the <200 word branch must honor structured input_sources, not just URL/path heuristic.

    Spec (references/doc-output-idearefin.md:13): "无明确输入源" includes both
    raw_content URL/path AND structured input_sources. Earlier P1-1 fix only
    covered the word_count==0 branch.
    """

    def test_short_raw_with_input_sources_skips_idea_refine(self):
        """Short non-fuzzy raw (<200 chars, no URL) + structured input_sources → skip refine.

        Phrase deliberately avoids fuzzy-word list (想做 / 大概 / 试试 / etc.) so the only
        idea-refine trigger candidate is condition 3 (无明确输入源 AND <200);
        condition 3 must now honor input_sources after the M2 fix.
        """
        refiner = IdeaRefiner(
            raw_content="审计日志聚合分析",  # <200 chars, no URL/path, no fuzzy words
            doc_type="tech_review",
            input_sources=[{"type": "code_repository", "repos": [{"path": "."}]}],
        )
        self.assertFalse(refiner.is_idea_refine_needed())

    def test_short_raw_no_input_sources_still_triggers_via_condition_3(self):
        """Same short non-fuzzy content + no input_sources → trigger via condition 3."""
        refiner = IdeaRefiner(
            raw_content="审计日志聚合分析",
            doc_type="tech_review",
            input_sources=[],
        )
        self.assertTrue(refiner.is_idea_refine_needed())

    def test_fuzzy_words_still_trigger_even_with_input_sources(self):
        """Condition 4 (fuzzy words + <200) stands alone per spec — does NOT honor input_sources.

        Spirit check: even if user provides input_sources, content like "想做个 XXX 试试"
        signals genuine ambiguity in scope, and idea-refine should still run. This is
        spec compliance (doc-output-idearefin.md:14), not over-fixing.
        """
        refiner = IdeaRefiner(
            raw_content="想做个 XXX 系统试试",
            doc_type="tech_review",
            input_sources=[{"type": "code_repository", "repos": [{"path": "."}]}],
        )
        self.assertTrue(refiner.is_idea_refine_needed())


class TestM3DirectSourceAndZeroTaskGuard(unittest.TestCase):
    """M3: --input-sources direct must wire raw_content into a direct source;
       ParallelFetcher must not crash on 0 tasks.
    """

    def test_build_config_maps_direct_input_source(self):
        from doc_output import build_config_from_args
        import argparse
        args = argparse.Namespace(
            doc_type="research_report",
            raw_content="DIRECT_CLI_CONTENT",
            input_sources=["direct"],
            repos=None, feishu_docs=None,
            output=None, workspace="/tmp",
            no_feishu=True,
        )
        config = build_config_from_args(args)
        self.assertEqual(len(config["input_sources"]), 1)
        self.assertEqual(config["input_sources"][0]["type"], "direct")
        self.assertEqual(config["input_sources"][0]["content"], "DIRECT_CLI_CONTENT")

    def test_build_config_skips_direct_when_raw_content_missing(self):
        """--input-sources direct without --raw-content → don't append empty source."""
        from doc_output import build_config_from_args
        import argparse
        args = argparse.Namespace(
            doc_type="research_report",
            raw_content=None,
            input_sources=["direct"],
            repos=None, feishu_docs=None,
            output=None, workspace="/tmp",
            no_feishu=True,
        )
        config = build_config_from_args(args)
        # No direct source appended because raw_content is None
        self.assertEqual(config["input_sources"], [])

    def test_parallel_fetcher_zero_tasks_returns_empty_dict(self):
        """ParallelFetcher with no input sources should not crash on max_workers=0."""
        fetcher = ParallelFetcher(input_sources=[], workspace="/tmp")
        results = fetcher.fetch_all()  # Pre-fix: ValueError; post-fix: {}
        self.assertEqual(results, {})


if __name__ == "__main__":
    unittest.main()
