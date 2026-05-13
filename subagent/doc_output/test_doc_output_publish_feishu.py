"""Tests for DocOutput._publish_feishu routing logic.

Verifies the three branches:
- upgrade_charts=True + workspace ok → calls publish_with_charts and returns its url
- upgrade_charts=False                → calls plain publish() (backwards-compat)
- workspace_dir missing               → falls back to plain publish()

The DocOutput class has heavy init dependencies (config/sources/templates) so
we build only the slice we need via __new__ + attribute injection.
"""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch, MagicMock

from doc_output import DocOutput


def _make_doc_output_skeleton(workspace: str, options: dict) -> DocOutput:
    """Construct a DocOutput just-enough to exercise _publish_feishu.

    Bypasses __init__ because it does a lot of input-source bookkeeping that's
    irrelevant for this slice; we inject the two attributes _publish_feishu
    actually reads.
    """
    doc = DocOutput.__new__(DocOutput)
    doc.workspace = workspace
    doc.options = options
    return doc


class TestPublishFeishuRouting(unittest.TestCase):
    def test_upgrade_charts_true_routes_to_publish_with_charts(self):
        with tempfile.TemporaryDirectory() as tmp:
            doc = _make_doc_output_skeleton(workspace=tmp, options={})

            mock_publisher = MagicMock()
            mock_publisher.publish_with_charts.return_value = {
                "url": "https://example/docx/TOK1",
                "doc_token": "TOK1",
                "chart_results": [],
                "skipped": [],
            }
            with patch("doc_output.FeishuPublisher", return_value=mock_publisher):
                url = doc._publish_feishu(title="t", content="x")

            self.assertEqual(url, "https://example/docx/TOK1")
            mock_publisher.publish_with_charts.assert_called_once()
            mock_publisher.publish.assert_not_called()

    def test_upgrade_charts_false_routes_to_plain_publish(self):
        with tempfile.TemporaryDirectory() as tmp:
            doc = _make_doc_output_skeleton(
                workspace=tmp, options={"upgrade_charts": False}
            )

            mock_publisher = MagicMock()
            mock_publisher.publish.return_value = "https://example/docx/PLAIN"
            with patch("doc_output.FeishuPublisher", return_value=mock_publisher):
                url = doc._publish_feishu(title="t", content="x")

            self.assertEqual(url, "https://example/docx/PLAIN")
            mock_publisher.publish.assert_called_once()
            mock_publisher.publish_with_charts.assert_not_called()

    def test_missing_workspace_falls_back_to_plain_publish(self):
        # workspace_dir does not exist on disk → fallback expected
        doc = _make_doc_output_skeleton(
            workspace="/tmp/does-not-exist-" + Path(tempfile.gettempdir()).name,
            options={},
        )

        mock_publisher = MagicMock()
        mock_publisher.publish.return_value = "https://example/docx/FALLBACK"
        with patch("doc_output.FeishuPublisher", return_value=mock_publisher):
            url = doc._publish_feishu(title="t", content="x")

        self.assertEqual(url, "https://example/docx/FALLBACK")
        mock_publisher.publish.assert_called_once()
        mock_publisher.publish_with_charts.assert_not_called()

    def test_chart_publisher_construction_failure_falls_back(self):
        """If ChartPublisher() raises, fall back to plain publish without crashing."""
        with tempfile.TemporaryDirectory() as tmp:
            doc = _make_doc_output_skeleton(workspace=tmp, options={})

            mock_publisher = MagicMock()
            mock_publisher.publish.return_value = "https://example/docx/RESCUE"

            # Make ChartPublisher constructor raise (via patching the class import path used in _publish_feishu)
            with patch("doc_output.FeishuPublisher", return_value=mock_publisher), \
                 patch("chart_publisher.ChartPublisher",
                       side_effect=RuntimeError("simulated init failure")):
                url = doc._publish_feishu(title="t", content="x")

            self.assertEqual(url, "https://example/docx/RESCUE")
            mock_publisher.publish.assert_called_once()
            mock_publisher.publish_with_charts.assert_not_called()


if __name__ == "__main__":
    unittest.main()
