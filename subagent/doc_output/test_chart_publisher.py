"""Unit tests for chart_publisher.py.

Covers three scenarios (per spec): push / roundtrip / diff.

No real lark-cli required — uses FakeLarkCliClient as a scriptable double.
Run with: python3 -m unittest subagent/doc_output/test_chart_publisher.py
"""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from chart_publisher import (
    ChartPublishError,
    ChartPublisher,
    PublishResult,
)


SAMPLE_MERMAID = (
    "flowchart TD\n"
    "  A([开始]) --> B[校验]\n"
    "  B --> C([结束])\n"
)


class FakeLarkCliClient:
    """Scriptable test double.

    Set `docs_update_response` to dictate the docs +update return shape (typically
    {"data": {"board_tokens": ["..."]}}).
    Set `fetch_responses` to a list of markdowns returned in order from successive docs_fetch calls.
    Set `update_response` for whiteboard +update return.
    Set `query_response` for whiteboard +query return.
    All calls are recorded on `self.calls`.
    """

    def __init__(self):
        self.docs_update_response: dict = {"data": {"board_tokens": ["newtok123"]}}
        self.fetch_responses: list[str] = []
        self.update_response: dict = {"data": {"created_node_id": "t99:1"}}
        self.query_response: str = ""
        self.calls: list[tuple] = []

    def docs_update(self, doc_token, markdown, mode):
        self.calls.append(("docs_update", doc_token, mode, markdown))
        return self.docs_update_response

    def docs_fetch(self, doc_token):
        self.calls.append(("docs_fetch", doc_token))
        if not self.fetch_responses:
            raise RuntimeError("FakeLarkCliClient.fetch_responses exhausted")
        return self.fetch_responses.pop(0)

    def whiteboard_update(self, whiteboard_token, source_relpath, cwd):
        # Record the actual file contents written (proves caller wrote the right .mmd)
        path = Path(cwd) / source_relpath.lstrip("./")
        contents = path.read_text(encoding="utf-8") if path.exists() else None
        self.calls.append(("whiteboard_update", whiteboard_token, contents))
        return self.update_response

    def whiteboard_query_code(self, whiteboard_token):
        self.calls.append(("whiteboard_query_code", whiteboard_token))
        return self.query_response


class TestPushHappyPath(unittest.TestCase):
    def test_publish_chart_calls_pipeline_and_returns_result(self):
        """Happy path: docs +update response contains board_tokens → no fallback fetch needed."""
        with tempfile.TemporaryDirectory() as tmp:
            client = FakeLarkCliClient()
            client.docs_update_response = {"data": {"board_tokens": ["newtok123"]}}
            client.update_response = {"data": {"created_node_id": "t42:7"}}
            client.query_response = SAMPLE_MERMAID  # exact match → roundtrip ok

            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            result = pub.publish_chart(
                doc_token="DOC123",
                chart_code="D-ARCH",
                mermaid_source=SAMPLE_MERMAID,
                chart_type="flowchart",
            )

            self.assertIsInstance(result, PublishResult)
            self.assertEqual(result.whiteboard_token, "newtok123")
            self.assertEqual(result.node_id, "t42:7")
            self.assertTrue(result.roundtrip_ok)
            self.assertIsNone(result.diff)

            # Call sequence: docs_update → whiteboard_update → whiteboard_query_code
            # (no extra docs_fetch — response had board_tokens)
            kinds = [c[0] for c in client.calls]
            self.assertEqual(kinds, [
                "docs_update",
                "whiteboard_update",
                "whiteboard_query_code",
            ])
            wb_update_call = client.calls[1]
            self.assertEqual(wb_update_call[1], "newtok123")
            self.assertEqual(wb_update_call[2], SAMPLE_MERMAID)
            # Temp .mmd cleaned up
            leftover_mmd = list(Path(tmp).glob("_chart_*.mmd"))
            self.assertEqual(leftover_mmd, [])

    def test_publish_chart_falls_back_to_fetch_when_response_lacks_board_tokens(self):
        """If docs +update doesn't include board_tokens, fall back to fetching and grabbing the last tag."""
        with tempfile.TemporaryDirectory() as tmp:
            client = FakeLarkCliClient()
            client.docs_update_response = {"data": {"success": True}}  # no board_tokens
            client.fetch_responses = [
                '# Title\n\n<whiteboard token="OLD_TOKEN" align="left"/>\n\n'
                '### D-ARCH\n\n<whiteboard token="FALLBACK_TOK" align="left"/>\n',
            ]
            client.query_response = SAMPLE_MERMAID
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            result = pub.publish_chart("DOC1", "D-ARCH", SAMPLE_MERMAID, "flowchart")

            self.assertEqual(result.whiteboard_token, "FALLBACK_TOK")
            kinds = [c[0] for c in client.calls]
            self.assertEqual(kinds, [
                "docs_update",
                "docs_fetch",  # only on fallback path
                "whiteboard_update",
                "whiteboard_query_code",
            ])

    def test_regex_tolerates_extra_attributes_on_whiteboard_tag(self):
        """Feishu adds align="left" to the inline whiteboard tag — regex must tolerate that."""
        md = '# X\n<whiteboard token="ABC" align="left"/>\n<whiteboard token="DEF"/>\n'
        tokens = ChartPublisher._extract_whiteboard_tokens(md)
        self.assertEqual(tokens, ["ABC", "DEF"])

    def test_publish_chart_rejects_unsupported_chart_type(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = FakeLarkCliClient()
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            with self.assertRaises(ChartPublishError):
                pub.publish_chart("DOC1", "D-ARCH", SAMPLE_MERMAID, chart_type="gantt-no-go")

    def test_publish_chart_fails_when_no_new_token_appears(self):
        """Response lacks board_tokens AND fallback fetch has zero <whiteboard> tags → fail."""
        with tempfile.TemporaryDirectory() as tmp:
            client = FakeLarkCliClient()
            client.docs_update_response = {"data": {"success": True}}  # no board_tokens
            client.fetch_responses = ["# Title with no whiteboard tags at all\n"]
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            with self.assertRaises(ChartPublishError) as cm:
                pub.publish_chart("DOC1", "D-ARCH", SAMPLE_MERMAID, "flowchart")
            self.assertIn("no <whiteboard token=.../>", str(cm.exception))


class TestRoundtripVerify(unittest.TestCase):
    def test_verify_chart_returns_ok_true_when_remote_matches(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = FakeLarkCliClient()
            client.query_response = SAMPLE_MERMAID
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))

            result = pub.verify_chart("tok-roundtrip", SAMPLE_MERMAID)

            self.assertTrue(result["ok"])
            self.assertIsNone(result["diff"])
            self.assertEqual(result["remote"], SAMPLE_MERMAID)
            self.assertEqual(client.calls, [("whiteboard_query_code", "tok-roundtrip")])


class TestDiffDetection(unittest.TestCase):
    def test_verify_chart_returns_ok_false_with_diff_when_remote_differs(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = FakeLarkCliClient()
            modified = SAMPLE_MERMAID.replace("校验", "校验失败")  # remote drifted
            client.query_response = modified
            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))

            result = pub.verify_chart("tok-drifted", SAMPLE_MERMAID)

            self.assertFalse(result["ok"])
            self.assertIsNotNone(result["diff"])
            self.assertIn("校验", result["diff"])
            self.assertIn("校验失败", result["diff"])
            # Diff includes the standard unified-diff header
            self.assertIn("--- expected", result["diff"])
            self.assertIn("+++ remote", result["diff"])

    def test_publish_chart_propagates_diff_in_result_when_remote_drifts(self):
        with tempfile.TemporaryDirectory() as tmp:
            client = FakeLarkCliClient()
            client.docs_update_response = {"data": {"board_tokens": ["seq-tok"]}}
            client.update_response = {"data": {"created_node_id": "t1:1"}}
            client.query_response = SAMPLE_MERMAID + "  X --> Y\n"  # remote has extra line

            pub = ChartPublisher(client=client, workspace_dir=Path(tmp))
            result = pub.publish_chart("DOC1", "D-SEQ", SAMPLE_MERMAID, "sequence")

            self.assertFalse(result.roundtrip_ok)
            self.assertIsNotNone(result.diff)
            self.assertIn("X --> Y", result.diff)


if __name__ == "__main__":
    unittest.main()
