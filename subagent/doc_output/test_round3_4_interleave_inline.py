"""Tests for R3 (D4 interleaving validator) + R4 (D5 inline-position insert).

R3 validates that annotated mermaid blocks sit between prose and a caption.
R4 lets chart_publisher insert whiteboards right after the annotation comment
(via `lark-cli docs +update --mode insert_after --selection-with-ellipsis ...`),
addressing the "图集 dumped at doc-end" anti-pattern.
"""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from chart_publisher import ChartPublisher, PublishResult, ChartPublishError
from feishu_publisher import validate_chart_prose_interleaving


# Shared mermaid sample
SAMPLE_MERMAID = (
    "flowchart TD\n"
    "  A([开始]) --> B[校验]\n"
    "  B --> C([结束])\n"
)


class TestInterleavingValidator(unittest.TestCase):
    """R3 — validate_chart_prose_interleaving."""

    def test_well_formed_block_returns_no_warnings(self):
        md = (
            "## 2. 整体架构\n\n"
            "本系统按三层划分：接入层、网关层、核心层。\n\n"
            "<!-- chart: D-ARCH -->\n"
            "```mermaid\n"
            "flowchart TB\n"
            "  A --> B\n"
            "```\n"
            "> 图说明：三层架构，自顶向下。\n\n"
        )
        warnings = validate_chart_prose_interleaving(md)
        self.assertEqual(warnings, [])

    def test_full_width_colon_caption_accepted(self):
        md = (
            "Some prose.\n\n"
            "<!-- chart: D-SEQ -->\n"
            "```mermaid\nsequenceDiagram\n  A->>B: x\n```\n"
            "> 图说明：full-width colon variant.\n"
        )
        self.assertEqual(validate_chart_prose_interleaving(md), [])

    def test_half_width_colon_caption_accepted(self):
        md = (
            "Some prose.\n\n"
            "<!-- chart: D-SEQ -->\n"
            "```mermaid\nsequenceDiagram\n  A->>B: x\n```\n"
            "> 图说明: half-width colon variant.\n"
        )
        self.assertEqual(validate_chart_prose_interleaving(md), [])

    def test_missing_prose_before_annotation_warns(self):
        """Annotation directly after a heading → missing prose context."""
        md = (
            "## 2. 整体架构\n\n"
            "<!-- chart: D-ARCH -->\n"
            "```mermaid\nflowchart TB\n  A --> B\n```\n"
            "> 图说明：xxx\n"
        )
        warnings = validate_chart_prose_interleaving(md)
        self.assertEqual(len(warnings), 1)
        self.assertEqual(warnings[0]["chart_code"], "D-ARCH")
        self.assertIn("heading", warnings[0]["reason"])

    def test_missing_caption_after_block_warns(self):
        md = (
            "Some prose.\n\n"
            "<!-- chart: D-ARCH -->\n"
            "```mermaid\nflowchart TB\n  A --> B\n```\n"
            "(next paragraph, no caption)\n"
        )
        warnings = validate_chart_prose_interleaving(md)
        self.assertEqual(len(warnings), 1)
        self.assertEqual(warnings[0]["chart_code"], "D-ARCH")
        self.assertIn("missing `> 图说明:`", warnings[0]["reason"])

    def test_multiple_blocks_independently_validated(self):
        md = (
            "prose-1\n\n"
            "<!-- chart: D-ARCH -->\n"
            "```mermaid\nflowchart TB\n  A\n```\n"
            "> 图说明：ok 1\n\n"
            "## 节标题\n\n"  # next block has only a heading before it → warn
            "<!-- chart: D-SEQ -->\n"
            "```mermaid\nsequenceDiagram\n  A->>B: x\n```\n"
            "(no caption)\n"
        )
        warnings = validate_chart_prose_interleaving(md)
        codes = [w["chart_code"] for w in warnings]
        self.assertCountEqual(codes, ["D-SEQ", "D-SEQ"])  # both prose + caption fail

    def test_empty_doc_returns_no_warnings(self):
        self.assertEqual(validate_chart_prose_interleaving(""), [])

    def test_doc_with_no_annotated_blocks_returns_no_warnings(self):
        md = "# Plain doc\n\nNo charts here.\n"
        self.assertEqual(validate_chart_prose_interleaving(md), [])


class _FakeClient:
    def __init__(self):
        self.docs_update_response = {"data": {"board_tokens": ["tok-XYZ"]}}
        self.update_response = {"data": {"created_node_id": "t1:5"}}
        self.query_response = SAMPLE_MERMAID
        self.calls = []
        self.fetch_responses = []  # not needed in inline path

    def docs_update(self, doc_token, markdown, mode, selection_with_ellipsis=None):
        self.calls.append({
            "kind": "docs_update",
            "doc_token": doc_token,
            "markdown": markdown,
            "mode": mode,
            "selection_with_ellipsis": selection_with_ellipsis,
        })
        return self.docs_update_response

    def docs_fetch(self, doc_token):
        self.calls.append({"kind": "docs_fetch", "doc_token": doc_token})
        return self.fetch_responses.pop(0) if self.fetch_responses else ""

    def whiteboard_update(self, whiteboard_token, source_relpath, cwd):
        self.calls.append({
            "kind": "whiteboard_update",
            "whiteboard_token": whiteboard_token,
        })
        return self.update_response

    def whiteboard_query_code(self, whiteboard_token):
        self.calls.append({
            "kind": "whiteboard_query_code",
            "whiteboard_token": whiteboard_token,
        })
        return self.query_response


class TestInlinePosition(unittest.TestCase):
    """R4 — chart_publisher publish_chart position='inline'."""

    def test_inline_position_uses_insert_after_with_annotation_selector(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = _FakeClient()
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            result = pub.publish_chart(
                doc_token="DOC1",
                chart_code="D-ARCH",
                mermaid_source=SAMPLE_MERMAID,
                chart_type="flowchart",
                position="inline",
            )
            self.assertEqual(result.whiteboard_token, "tok-XYZ")
            self.assertTrue(result.roundtrip_ok)
            # Verify the docs_update call shape
            update_call = next(c for c in client.calls if c["kind"] == "docs_update")
            self.assertEqual(update_call["mode"], "insert_after")
            self.assertEqual(
                update_call["selection_with_ellipsis"],
                "<!-- chart: D-ARCH -->",
            )
            # The markdown passed to docs_update is the blank-whiteboard tag, not the
            # append-mode "### chart_code\n\n<whiteboard.../>" anchor:
            self.assertEqual(update_call["markdown"], '<whiteboard type="blank"></whiteboard>')

    def test_append_position_preserves_legacy_behavior(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = _FakeClient()
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            pub.publish_chart(
                doc_token="DOC1",
                chart_code="D-SEQ",
                mermaid_source=SAMPLE_MERMAID,
                chart_type="sequence",
                position="append",
            )
            update_call = next(c for c in client.calls if c["kind"] == "docs_update")
            self.assertEqual(update_call["mode"], "append")
            self.assertIsNone(update_call["selection_with_ellipsis"])
            # Legacy anchor: ### D-SEQ heading + blank whiteboard
            self.assertIn("### D-SEQ", update_call["markdown"])
            self.assertIn('<whiteboard type="blank"', update_call["markdown"])

    def test_default_position_is_append_for_backwards_compat(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = _FakeClient()
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            # No position kwarg → default
            pub.publish_chart(
                doc_token="DOC1",
                chart_code="D-DAG",
                mermaid_source=SAMPLE_MERMAID,
                chart_type="flowchart",
            )
            update_call = next(c for c in client.calls if c["kind"] == "docs_update")
            self.assertEqual(update_call["mode"], "append")

    def test_unsupported_position_raises(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = _FakeClient()
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            with self.assertRaises(ChartPublishError) as cm:
                pub.publish_chart(
                    doc_token="DOC1",
                    chart_code="D-ARCH",
                    mermaid_source=SAMPLE_MERMAID,
                    chart_type="flowchart",
                    position="prepend",  # not supported
                )
            self.assertIn("unsupported position", str(cm.exception))


if __name__ == "__main__":
    unittest.main()
