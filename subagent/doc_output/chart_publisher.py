"""chart_publisher.py — upgrade inline Mermaid blocks to interactive Feishu whiteboards.

Pipeline per chart:
  1. Append `<whiteboard type="blank"></whiteboard>` to the docx (lark-cli docs +update --mode append).
  2. Re-fetch the docx markdown, locate the newly minted `<whiteboard token="..."/>` placeholder.
  3. Push the Mermaid source into that whiteboard (lark-cli whiteboard +update --input_format mermaid).
  4. Round-trip the whiteboard (lark-cli whiteboard +query --output_as code) and compare to source.

Design notes:
  - All subprocess/lark-cli I/O is funneled through `LarkCliClient`. Tests substitute a fake.
  - lark-cli's `--source @path` rejects absolute paths and paths outside cwd; we honor that by
    writing temp files into a caller-supplied workspace_dir and invoking subprocess with cwd set.
"""

from __future__ import annotations

import json
import re
import subprocess
import tempfile
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Optional, Protocol


WHITEBOARD_TAG_RE = re.compile(r'<whiteboard\b[^>]*\btoken="([^"]+)"[^>]*/>')


class ChartPublishError(RuntimeError):
    """Raised when any step in the publish pipeline fails."""


@dataclass
class PublishResult:
    chart_code: str
    chart_type: str
    whiteboard_token: str
    node_id: Optional[str]
    roundtrip_ok: bool
    diff: Optional[str] = None


class LarkCliClient(Protocol):
    def docs_update(self, doc_token: str, markdown: str, mode: str) -> dict: ...
    def docs_fetch(self, doc_token: str) -> str: ...
    def whiteboard_update(self, whiteboard_token: str, source_relpath: str, cwd: Path) -> dict: ...
    def whiteboard_query_code(self, whiteboard_token: str) -> str: ...


class RealLarkCliClient:
    """Subprocess-backed implementation. Used in production."""

    def __init__(self, identity: str = "bot"):
        self.identity = identity

    def _run(self, args: list[str], cwd: Optional[Path] = None) -> dict:
        proc = subprocess.run(
            args,
            cwd=str(cwd) if cwd else None,
            capture_output=True,
            text=True,
            check=False,
        )
        if proc.returncode != 0:
            raise ChartPublishError(
                f"lark-cli failed (exit {proc.returncode}): {' '.join(args)}\n"
                f"stderr: {proc.stderr.strip()}\nstdout: {proc.stdout.strip()}"
            )
        return _parse_lark_json(proc.stdout)

    def docs_update(self, doc_token: str, markdown: str, mode: str) -> dict:
        return self._run([
            "lark-cli", "docs", "+update",
            "--doc", doc_token,
            "--mode", mode,
            "--markdown", markdown,
            "--as", self.identity,
        ])

    def docs_fetch(self, doc_token: str) -> str:
        result = self._run([
            "lark-cli", "docs", "+fetch",
            "--doc", doc_token,
            "--as", self.identity,
        ])
        # docs +fetch returns either {ok, data: {markdown}} (new shape) or {data: {markdown}} (raw passthrough).
        data = result.get("data") if "data" in result else result
        markdown = data.get("markdown")
        if markdown is None:
            raise ChartPublishError(f"docs +fetch returned no markdown field; response: {result}")
        return markdown

    def whiteboard_update(self, whiteboard_token: str, source_relpath: str, cwd: Path) -> dict:
        return self._run([
            "lark-cli", "whiteboard", "+update",
            "--whiteboard-token", whiteboard_token,
            "--input_format", "mermaid",
            "--source", f"@{source_relpath}",
            "--overwrite",
            "--as", self.identity,
        ], cwd=cwd)

    def whiteboard_query_code(self, whiteboard_token: str) -> str:
        result = self._run([
            "lark-cli", "whiteboard", "+query",
            "--whiteboard-token", whiteboard_token,
            "--output_as", "code",
            "--as", self.identity,
        ])
        data = result.get("data") if "data" in result else result
        code = data.get("code")
        if code is None:
            raise ChartPublishError(f"whiteboard +query returned no code field; response: {result}")
        return code


def _parse_lark_json(stdout: str) -> dict:
    """lark-cli prefixes some calls with [WARN] / [deprecated] notice lines.

    Strip leading non-JSON lines, then parse.
    """
    for i, line in enumerate(stdout.splitlines()):
        if line.lstrip().startswith("{"):
            return json.loads("\n".join(stdout.splitlines()[i:]))
    raise ChartPublishError(f"could not find JSON object in lark-cli output:\n{stdout}")


class ChartPublisher:
    SUPPORTED_TYPES = {"flowchart", "sequence", "state-machine", "er-diagram", "mindmap"}

    def __init__(self, client: LarkCliClient, workspace_dir: Path):
        self.client = client
        self.workspace_dir = Path(workspace_dir)
        if not self.workspace_dir.is_dir():
            raise ChartPublishError(f"workspace_dir does not exist: {self.workspace_dir}")

    def publish_chart(
        self,
        doc_token: str,
        chart_code: str,
        mermaid_source: str,
        chart_type: str,
    ) -> PublishResult:
        """Append a new whiteboard to the docx and fill it with the mermaid source.

        Returns a PublishResult including roundtrip verification status.
        """
        if chart_type not in self.SUPPORTED_TYPES:
            raise ChartPublishError(
                f"unsupported chart_type {chart_type!r}; supported: {sorted(self.SUPPORTED_TYPES)}"
            )

        anchor = f"\n\n### {chart_code}\n\n<whiteboard type=\"blank\"></whiteboard>\n"
        update_response = self.client.docs_update(doc_token, anchor, mode="append")

        whiteboard_token = self._extract_new_token(update_response)
        if whiteboard_token is None:
            # Fallback: re-fetch and diff against the markdown before the append.
            # We didn't snapshot pre-state up front (to save a roundtrip when the response is sufficient),
            # so this fallback re-fetches and trusts that the *last* whiteboard tag is the new one.
            post_markdown = self.client.docs_fetch(doc_token)
            tokens = self._extract_whiteboard_tokens(post_markdown)
            if not tokens:
                raise ChartPublishError(
                    f"docs +update returned no board_tokens and fetched markdown has no "
                    f"<whiteboard token=.../> tag at all. chart_code={chart_code}; response={update_response}"
                )
            whiteboard_token = tokens[-1]

        mmd_relpath = f"_chart_{uuid.uuid4().hex[:8]}.mmd"
        mmd_path = self.workspace_dir / mmd_relpath
        try:
            mmd_path.write_text(mermaid_source, encoding="utf-8")
            push_result = self.client.whiteboard_update(
                whiteboard_token=whiteboard_token,
                source_relpath=f"./{mmd_relpath}",
                cwd=self.workspace_dir,
            )
        finally:
            mmd_path.unlink(missing_ok=True)

        push_data = push_result.get("data") if "data" in push_result else push_result
        node_id = push_data.get("created_node_id")

        roundtrip = self.verify_chart(whiteboard_token, mermaid_source)
        return PublishResult(
            chart_code=chart_code,
            chart_type=chart_type,
            whiteboard_token=whiteboard_token,
            node_id=node_id,
            roundtrip_ok=roundtrip["ok"],
            diff=roundtrip.get("diff"),
        )

    def verify_chart(self, whiteboard_token: str, expected_mermaid: str) -> dict:
        """Round-trip read the whiteboard and compare to expected source.

        Returns {"ok": bool, "diff": Optional[str], "remote": str}.
        """
        remote = self.client.whiteboard_query_code(whiteboard_token)
        if remote == expected_mermaid:
            return {"ok": True, "diff": None, "remote": remote}
        diff = _line_diff(expected_mermaid, remote)
        return {"ok": False, "diff": diff, "remote": remote}

    @staticmethod
    def _extract_whiteboard_tokens(markdown: str) -> list[str]:
        return WHITEBOARD_TAG_RE.findall(markdown)

    @staticmethod
    def _extract_new_token(update_response: dict) -> Optional[str]:
        """Pull the freshly-minted whiteboard token from a docs +update response if present.

        Feishu's docs +update returns `data.board_tokens: [<new_token>]` when the markdown
        contained `<whiteboard type="blank"></whiteboard>`. This is the cheapest and most
        reliable signal — prefer it over fetch-diffing.
        """
        data = update_response.get("data") if "data" in update_response else update_response
        if not isinstance(data, dict):
            return None
        board_tokens = data.get("board_tokens")
        if isinstance(board_tokens, list) and board_tokens:
            return board_tokens[0]
        return None


def _line_diff(expected: str, actual: str) -> str:
    """Compact line diff for verify_chart failure reports."""
    import difflib
    lines = difflib.unified_diff(
        expected.splitlines(keepends=True),
        actual.splitlines(keepends=True),
        fromfile="expected", tofile="remote", n=1,
    )
    return "".join(lines)
