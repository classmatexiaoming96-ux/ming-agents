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


if __name__ == "__main__":
    unittest.main()
