#!/usr/bin/env python3
"""
飞书发布模块

负责将生成的文档发布到飞书。v2.4 起新增 `publish_with_charts()`：发布完 docx 后
自动遍历 D-ARCH/D-SEQ/D-DAG 标注的 mermaid 块，通过 chart_publisher 升级成飞书画板。
"""

import os
import re
import sys
from typing import Optional

# 尝试导入飞书SDK，如果不可用则使用subprocess调用
try:
    from feishu_create_doc import create_feishu_doc
    HAS_LARK_SDK = True
except ImportError:
    HAS_LARK_SDK = False


# 匹配 `<!-- chart: D-XXX -->` 紧跟 ` ```mermaid ... ``` ` 的代码块。
# DOTALL 允许 mermaid body 跨多行；非贪婪匹配以 ``` 结束。
_CHART_BLOCK_RE = re.compile(
    r"<!--\s*chart:\s*(D-[A-Z]+)\s*-->\s*\n```mermaid\n(.*?)\n```",
    re.DOTALL,
)

# Auto-upgrade scope — keep in sync with TEMPLATES.md §0 v2.4.0
_UPGRADE_CHART_CODES = frozenset({"D-ARCH", "D-SEQ", "D-DAG"})

# chart_code → chart_type passed to ChartPublisher.publish_chart()
_CHART_CODE_TYPE = {
    "D-ARCH": "flowchart",
    "D-SEQ": "sequence",
    "D-DAG": "flowchart",
}

# Feishu docx URL pattern: https://<host>.larkoffice.com/docx/<token>
_DOC_TOKEN_RE = re.compile(r"/docx/([A-Za-z0-9]+)")


def extract_upgradable_charts(markdown: str) -> list:
    """Find `<!-- chart: D-XXX -->`-annotated mermaid blocks eligible for画板升级.

    Only blocks whose D-code is in :data:`_UPGRADE_CHART_CODES` are returned.
    Un-annotated mermaid blocks are deliberately skipped — auto-classifying by
    content (sequenceDiagram vs flowchart vs Phase-N subgraph) is fragile, so
    we make the template author opt in explicitly.

    Returns: list of ``(chart_code, mermaid_source)`` tuples in document order.
    """
    out = []
    for match in _CHART_BLOCK_RE.finditer(markdown):
        code = match.group(1)
        body = match.group(2)
        if code in _UPGRADE_CHART_CODES:
            out.append((code, body + "\n"))  # restore trailing newline for whiteboard-cli
    return out


def extract_all_annotated_charts(markdown: str) -> list:
    """Like extract_upgradable_charts but returns ALL annotated mermaid blocks
    regardless of chart code. Used by position="auto" path because Feishu's
    docs +create auto-converts every mermaid fence into an inline whiteboard
    blank — we have to pair ALL blanks to ALL annotated sources by document
    order, otherwise the count-mismatch makes pairing skew the mapping.
    """
    return [
        (m.group(1), m.group(2) + "\n")
        for m in _CHART_BLOCK_RE.finditer(markdown)
    ]


def _extract_doc_token(url: str) -> Optional[str]:
    match = _DOC_TOKEN_RE.search(url)
    return match.group(1) if match else None


# Match the full annotated mermaid block:
#   group(1): the chart code (D-ARCH / D-SEQ / D-DAG / ...)
#   group(2): mermaid body (without fences)
# Plus we capture the line index by walking the markdown.
_ANNOTATED_BLOCK_RE = re.compile(
    r"<!--\s*chart:\s*(D-[A-Z]+)\s*-->\s*\n```mermaid\n(.*?)\n```",
    re.DOTALL,
)

# A caption line right after the closing ``` fence:
#   > 图说明：xxx
_CAPTION_RE = re.compile(r"^\s*>\s*图说明[:：]")


def validate_chart_prose_interleaving(markdown: str) -> list:
    """Check each annotated mermaid block has prose-before + caption-after.

    Enforces the "图文交织" convention (pm_agent G1.5.10):
    - The line immediately before `<!-- chart: D-XXX -->` should NOT be a
      section heading (#/##/###) or blank — i.e., at least one prose/table
      line should sit just above the annotation to give context.
    - The line immediately after the closing ``` fence should start with
      `> 图说明:` (full-width or half-width colon), per TEMPLATES.md §0.6.

    Returns a list of warning records:
      [{"chart_code": "D-ARCH", "line": <1-based>, "reason": "<short msg>"}, ...]
    Empty list ⇒ clean.
    """
    warnings = []
    lines = markdown.splitlines()
    for m in _ANNOTATED_BLOCK_RE.finditer(markdown):
        chart_code = m.group(1)
        annotation_pos = m.start()
        # Find which line the annotation sits on (1-based)
        annotation_line_idx = markdown.count("\n", 0, annotation_pos)
        # Prose-before check: scan upward past blank lines for the first non-blank
        prev = annotation_line_idx - 1
        while prev >= 0 and not lines[prev].strip():
            prev -= 1
        if prev < 0:
            warnings.append({
                "chart_code": chart_code,
                "line": annotation_line_idx + 1,
                "reason": "no prose line before annotation (block at top of doc)",
            })
        elif lines[prev].lstrip().startswith("#"):
            warnings.append({
                "chart_code": chart_code,
                "line": annotation_line_idx + 1,
                "reason": f"annotation directly follows a heading "
                          f"({lines[prev].strip()[:30]!r}); add ≥1 prose line in between",
            })
        # Caption-after check
        # The closing ``` fence is at m.end()-3 (...```). Find what's on the next line.
        end_pos = m.end()
        # Skip the newline after ```, then take the next non-blank line
        if end_pos >= len(markdown):
            warnings.append({
                "chart_code": chart_code,
                "line": annotation_line_idx + 1,
                "reason": "no `> 图说明:` caption after block (block at end of doc)",
            })
            continue
        caption_line_idx = markdown.count("\n", 0, end_pos) + 1  # 0-based
        # Walk forward skipping blank lines
        while caption_line_idx < len(lines) and not lines[caption_line_idx].strip():
            caption_line_idx += 1
        if caption_line_idx >= len(lines) or not _CAPTION_RE.match(lines[caption_line_idx]):
            actual = lines[caption_line_idx][:30] if caption_line_idx < len(lines) else "<EOF>"
            warnings.append({
                "chart_code": chart_code,
                "line": annotation_line_idx + 1,
                "reason": f"missing `> 图说明:` caption right after block "
                          f"(got {actual!r})",
            })
    return warnings


class FeishuPublisher:
    """飞书文档发布器"""
    
    def __init__(self):
        self.api_available = HAS_LARK_SDK
    
    def publish(self, title: str, content: str, 
                folder_token: Optional[str] = None) -> str:
        """
        发布文档到飞书
        
        Args:
            title: 文档标题
            content: 文档内容 (Markdown格式)
            folder_token: 飞书文件夹 token (可选)
        
        Returns:
            飞书文档 URL
        """
        if self.api_available:
            return self._publish_via_sdk(title, content, folder_token)
        else:
            return self._publish_via_cli(title, content, folder_token)
    
    def _publish_via_sdk(self, title: str, content: str,
                         folder_token: Optional[str]) -> str:
        """通过 SDK 发布"""
        try:
            result = create_feishu_doc(
                title=title,
                markdown=content,
                folder_token=folder_token
            )
            return result.get('doc_url', '')
        except Exception as e:
            print(f"[FeishuPublisher] SDK 发布失败: {e}")
            # 降级到 CLI
            return self._publish_via_cli(title, content, folder_token)
    
    def _publish_via_cli(self, title: str, content: str,
                         folder_token: Optional[str]) -> str:
        """通过 CLI 工具发布"""
        # 保存临时文件
        import tempfile
        with tempfile.NamedTemporaryFile(mode='w', suffix='.md', 
                                         delete=False, encoding='utf-8') as f:
            f.write(content)
            temp_file = f.name
        
        try:
            # 构建命令
            cmd = ['openclaw', 'feishu', 'create-doc', '--title', title]
            if folder_token:
                cmd.extend(['--folder', folder_token])
            cmd.extend([temp_file])
            
            # 执行命令
            import subprocess
            result = subprocess.run(
                cmd,
                capture_output=True,
                text=True,
                timeout=60
            )
            
            if result.returncode == 0:
                # 解析输出获取 URL
                output = result.stdout
                # 尝试从输出中提取 URL
                import re
                url_match = re.search(r'https?://[^\s]+', output)
                if url_match:
                    return url_match.group()
                
                return "飞书文档创建成功"
            else:
                raise Exception(f"CLI 命令失败: {result.stderr}")
        
        finally:
            # 清理临时文件
            os.unlink(temp_file)
        
        return ""
    
    def publish_with_charts(self, title: str, content: str,
                            chart_publisher,
                            folder_token: Optional[str] = None,
                            position: str = "append") -> dict:
        """发布到飞书 + 自动把 D-ARCH/D-SEQ/D-DAG mermaid 块升级为可交互画板。

        步骤:
          1. 走原 publish() 拿到 URL
          2. 从 URL 提取 doc_token (默认飞书 docx URL 格式)
          3. 调 extract_upgradable_charts() 扫出需升级的 (chart_code, mermaid) 列表
          4. 对每个 chart 调 chart_publisher.publish_chart(position=position)
          5. 单个 chart 失败不中断其它 chart 的发布 (best-effort)

        Args:
            title: 文档标题
            content: Markdown 内容(应包含 ``<!-- chart: D-XXX -->`` 标注的 mermaid 块)
            chart_publisher: ChartPublisher 实例
            folder_token: 飞书文件夹 token (可选)
            position: "append" (v2.3 默认,向后兼容) → 画板追加到文档末尾;
                      "inline" (v2.5+) → 画板插入到对应 ``<!-- chart: D-XXX -->`` 注释
                      之后(紧邻原 mermaid 块),解决"图集 dump 在文末"问题。

        Returns:
            dict with:
              - url (str): 发布后的文档 URL (publish() 的原返回)
              - doc_token (Optional[str]): 从 URL 解析的 token, 解析失败则为 None
              - chart_results (list): 每个成功升级的 chart 对应 PublishResult
              - skipped (list of (chart_code, reason)): 跳过/失败原因
        """
        url = self.publish(title, content, folder_token)
        doc_token = _extract_doc_token(url)

        result = {
            "url": url,
            "doc_token": doc_token,
            "chart_results": [],
            "skipped": [],
        }

        if not doc_token:
            result["skipped"].append(
                ("*", f"could not extract doc_token from URL: {url!r}")
            )
            return result

        # position="auto" pairs ALL annotated blocks to Feishu's auto-created whiteboards
        # (Feishu converts every mermaid block, regardless of D-XXX code, into an
        # inline blank). Filtering to D-ARCH/D-SEQ/D-DAG only causes count mismatch
        # and skewed pairing. Other positions still respect the upgrade filter.
        if position == "auto":
            upgradables = extract_all_annotated_charts(content)
        else:
            upgradables = extract_upgradable_charts(content)

        if position == "auto":
            # v2.5+: Feishu's `docs +create` 自动把每个 ```mermaid``` 块转成
            # 一个内联 `<whiteboard token=.../>` 空白占位 — 已经"图文并茂"地放在
            # 原 mermaid 块的位置上。所以我们不再"另建空白板+ append",
            # 而是 fetch 已发布 docx, 抽 inline tokens (按文档先后顺序), 配对
            # upgradables 列表逐个 fill。
            try:
                published_md = chart_publisher.client.docs_fetch(doc_token)
            except Exception as e:
                # Without fetch we can't enumerate the auto-created blanks; abort upgrade
                result["skipped"].append(("*", f"docs +fetch failed: {e}"))
                return result
            existing_tokens = [
                m.group(1) for m in re.finditer(
                    r'<whiteboard\b[^>]*\btoken="([^"]+)"', published_md
                )
            ]
            if len(existing_tokens) < len(upgradables):
                result["skipped"].append((
                    "*",
                    f"Feishu auto-created only {len(existing_tokens)} whiteboards "
                    f"but {len(upgradables)} upgradable mermaid blocks "
                    f"were found in source — pairing skipped to avoid mismatch",
                ))
                return result
            for (code, mermaid_body), wb_token in zip(upgradables, existing_tokens):
                try:
                    publish_result = chart_publisher.fill_existing_whiteboard(
                        whiteboard_token=wb_token,
                        chart_code=code,
                        chart_type=_CHART_CODE_TYPE[code],
                        mermaid_source=mermaid_body,
                    )
                    result["chart_results"].append(publish_result)
                except Exception as e:
                    print(f"[FeishuPublisher] chart {code} 填充失败: {e}", file=sys.stderr)
                    result["skipped"].append((code, f"fill_existing_whiteboard failed: {e}"))
            return result

        # Legacy position="append" / "inline" path — kept for back-compat
        for code, mermaid_body in upgradables:
            try:
                publish_result = chart_publisher.publish_chart(
                    doc_token=doc_token,
                    chart_code=code,
                    mermaid_source=mermaid_body,
                    chart_type=_CHART_CODE_TYPE[code],
                    position=position,
                )
                result["chart_results"].append(publish_result)
            except Exception as e:
                # Best-effort: log and continue with remaining charts
                print(f"[FeishuPublisher] chart {code} 升级失败: {e}", file=sys.stderr)
                result["skipped"].append((code, f"publish_chart failed: {e}"))

        return result

    def update(self, doc_id: str, content: str) -> bool:
        """
        更新已发布的飞书文档
        
        Args:
            doc_id: 飞书文档 ID
            content: 新的文档内容
        
        Returns:
            是否更新成功
        """
        try:
            if self.api_available:
                # TODO: 实现 SDK 更新逻辑
                pass
            else:
                # TODO: 实现 CLI 更新逻辑
                pass
            return True
        except Exception as e:
            print(f"[FeishuPublisher] 更新失败: {e}")
            return False
