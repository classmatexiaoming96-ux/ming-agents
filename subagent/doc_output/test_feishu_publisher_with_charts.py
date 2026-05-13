"""Tests for FeishuPublisher.publish_with_charts() + extract_upgradable_charts().

Focused on the new pm-n2 integration logic; the original publish() / _publish_via_*
paths are not exercised here (they're covered by their own tests/manual flow).

Run with: python3 -m unittest subagent/doc_output/test_feishu_publisher_with_charts.py
"""

from __future__ import annotations

import unittest
from unittest.mock import patch

from chart_publisher import PublishResult
from feishu_publisher import (
    FeishuPublisher,
    extract_upgradable_charts,
    _extract_doc_token,
)


SAMPLE_DOC = """# tech_review

## §2.1 系统架构

<!-- chart: D-ARCH -->
```mermaid
flowchart TB
  subgraph Client["接入层"]
    A1[Web]
  end
```

## §2.3 时序

<!-- chart: D-SEQ -->
```mermaid
sequenceDiagram
  A->>B: req
```

## §5 类图

<!-- chart: D-CLASS -->
```mermaid
classDiagram
  class Foo { +bar() }
```

## 未标注的 mermaid 块（应被忽略）

```mermaid
flowchart LR
  X --> Y
```

## module_plan

<!-- chart: D-DAG -->
```mermaid
flowchart LR
  T1 --> T2
```
"""


class TestExtractUpgradableCharts(unittest.TestCase):
    def test_returns_only_d_arch_d_seq_d_dag_blocks(self):
        charts = extract_upgradable_charts(SAMPLE_DOC)
        codes = [c[0] for c in charts]
        self.assertEqual(codes, ["D-ARCH", "D-SEQ", "D-DAG"])

    def test_ignores_d_class_blocks(self):
        # D-CLASS is in SAMPLE_DOC but should be filtered out
        charts = extract_upgradable_charts(SAMPLE_DOC)
        self.assertNotIn("D-CLASS", [c[0] for c in charts])

    def test_ignores_unannotated_mermaid_blocks(self):
        # The "X --> Y" block has no <!-- chart: ... --> annotation
        charts = extract_upgradable_charts(SAMPLE_DOC)
        bodies = [c[1] for c in charts]
        for body in bodies:
            self.assertNotIn("X --> Y", body)

    def test_preserves_mermaid_body_content(self):
        charts = extract_upgradable_charts(SAMPLE_DOC)
        d_arch_body = next(b for c, b in charts if c == "D-ARCH")
        self.assertIn("flowchart TB", d_arch_body)
        self.assertIn('subgraph Client["接入层"]', d_arch_body)
        # Body should end with a newline (restored for whiteboard-cli)
        self.assertTrue(d_arch_body.endswith("\n"))

    def test_returns_empty_for_doc_with_no_annotated_blocks(self):
        md = "# Just text\n\n```mermaid\nflowchart LR\n  A --> B\n```\n"
        self.assertEqual(extract_upgradable_charts(md), [])

    def test_handles_doc_with_no_mermaid_blocks(self):
        self.assertEqual(extract_upgradable_charts("# Hello\n\nNo charts here.\n"), [])


class TestExtractDocToken(unittest.TestCase):
    def test_standard_feishu_docx_url(self):
        url = "https://bytedance.larkoffice.com/docx/D8JudIgEqoJQwuxbrLSlp9jQgSe"
        self.assertEqual(_extract_doc_token(url), "D8JudIgEqoJQwuxbrLSlp9jQgSe")

    def test_url_with_query_string(self):
        url = "https://bytedance.larkoffice.com/docx/D8JudIgEqoJQwuxbrLSlp9jQgSe?from=foo"
        self.assertEqual(_extract_doc_token(url), "D8JudIgEqoJQwuxbrLSlp9jQgSe")

    def test_non_docx_url_returns_none(self):
        self.assertIsNone(_extract_doc_token("https://example.com/file/xyz"))
        self.assertIsNone(_extract_doc_token("飞书文档创建成功"))


class FakeChartPublisher:
    """Minimal stand-in for the real ChartPublisher used in publish_with_charts tests."""

    def __init__(self):
        self.calls = []
        self.next_result = None  # Either a PublishResult, or an Exception to raise
        self.results_for_code = {}  # code -> PublishResult or Exception

    def publish_chart(self, doc_token, chart_code, mermaid_source, chart_type,
                      position="append"):
        self.calls.append({
            "doc_token": doc_token,
            "chart_code": chart_code,
            "chart_type": chart_type,
            "mermaid_source": mermaid_source,
            "position": position,
        })
        if chart_code in self.results_for_code:
            r = self.results_for_code[chart_code]
            if isinstance(r, Exception):
                raise r
            return r
        if isinstance(self.next_result, Exception):
            raise self.next_result
        return self.next_result or PublishResult(
            chart_code=chart_code,
            chart_type=chart_type,
            whiteboard_token=f"tok-{chart_code}",
            node_id="t1:1",
            roundtrip_ok=True,
        )


class TestPublishWithCharts(unittest.TestCase):
    def test_publishes_doc_then_iterates_eligible_charts(self):
        publisher = FeishuPublisher()
        fake_charter = FakeChartPublisher()

        with patch.object(publisher, "publish", return_value=(
            "https://bytedance.larkoffice.com/docx/DOCTOKEN42"
        )) as mock_publish:
            result = publisher.publish_with_charts(
                title="t", content=SAMPLE_DOC, chart_publisher=fake_charter,
            )

        mock_publish.assert_called_once()
        self.assertEqual(result["doc_token"], "DOCTOKEN42")
        # D-ARCH / D-SEQ / D-DAG all succeed; D-CLASS + un-annotated skipped
        self.assertEqual(len(result["chart_results"]), 3)
        self.assertEqual(result["skipped"], [])
        # Chart publisher saw exactly those 3 codes in document order
        codes = [c["chart_code"] for c in fake_charter.calls]
        self.assertEqual(codes, ["D-ARCH", "D-SEQ", "D-DAG"])
        # Type mapping is correct
        types = {c["chart_code"]: c["chart_type"] for c in fake_charter.calls}
        self.assertEqual(types, {
            "D-ARCH": "flowchart",
            "D-SEQ":  "sequence",
            "D-DAG":  "flowchart",
        })

    def test_per_chart_failure_does_not_abort_remaining_charts(self):
        publisher = FeishuPublisher()
        fake_charter = FakeChartPublisher()
        fake_charter.results_for_code = {
            "D-ARCH": PublishResult("D-ARCH", "flowchart", "t1", "n1", True),
            "D-SEQ":  RuntimeError("simulated whiteboard +update 5xx"),
            "D-DAG":  PublishResult("D-DAG", "flowchart", "t3", "n3", True),
        }

        with patch.object(publisher, "publish", return_value=(
            "https://bytedance.larkoffice.com/docx/DOCTOKEN42"
        )):
            result = publisher.publish_with_charts(
                title="t", content=SAMPLE_DOC, chart_publisher=fake_charter,
            )

        # 2 successes
        self.assertEqual([r.chart_code for r in result["chart_results"]], ["D-ARCH", "D-DAG"])
        # 1 skipped with the right reason
        self.assertEqual(len(result["skipped"]), 1)
        self.assertEqual(result["skipped"][0][0], "D-SEQ")
        self.assertIn("simulated whiteboard +update 5xx", result["skipped"][0][1])

    def test_missing_doc_token_reports_skip_and_no_chart_calls(self):
        publisher = FeishuPublisher()
        fake_charter = FakeChartPublisher()

        with patch.object(publisher, "publish", return_value="飞书文档创建成功"):
            result = publisher.publish_with_charts(
                title="t", content=SAMPLE_DOC, chart_publisher=fake_charter,
            )

        self.assertIsNone(result["doc_token"])
        self.assertEqual(result["chart_results"], [])
        # No chart calls happened — orchestration aborted at token extraction
        self.assertEqual(fake_charter.calls, [])
        self.assertEqual(len(result["skipped"]), 1)
        self.assertEqual(result["skipped"][0][0], "*")

    def test_doc_with_no_upgradable_charts_returns_empty_results(self):
        publisher = FeishuPublisher()
        fake_charter = FakeChartPublisher()
        plain_md = "# t\n\n<!-- chart: D-CLASS -->\n```mermaid\nclassDiagram\n  class A\n```\n"

        with patch.object(publisher, "publish", return_value=(
            "https://bytedance.larkoffice.com/docx/X1"
        )):
            result = publisher.publish_with_charts(
                title="t", content=plain_md, chart_publisher=fake_charter,
            )

        self.assertEqual(result["doc_token"], "X1")
        self.assertEqual(result["chart_results"], [])
        self.assertEqual(result["skipped"], [])
        self.assertEqual(fake_charter.calls, [])


if __name__ == "__main__":
    unittest.main()
