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


def _extract_doc_token(url: str) -> Optional[str]:
    match = _DOC_TOKEN_RE.search(url)
    return match.group(1) if match else None


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
                            folder_token: Optional[str] = None) -> dict:
        """发布到飞书 + 自动把 D-ARCH/D-SEQ/D-DAG mermaid 块升级为可交互画板。

        步骤:
          1. 走原 publish() 拿到 URL
          2. 从 URL 提取 doc_token (默认飞书 docx URL 格式)
          3. 调 extract_upgradable_charts() 扫出需升级的 (chart_code, mermaid) 列表
          4. 对每个 chart 调 chart_publisher.publish_chart()
          5. 单个 chart 失败不中断其它 chart 的发布 (best-effort)

        Args:
            title: 文档标题
            content: Markdown 内容(应包含 ``<!-- chart: D-XXX -->`` 标注的 mermaid 块)
            chart_publisher: ChartPublisher 实例
            folder_token: 飞书文件夹 token (可选)

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

        for code, mermaid_body in extract_upgradable_charts(content):
            try:
                publish_result = chart_publisher.publish_chart(
                    doc_token=doc_token,
                    chart_code=code,
                    mermaid_source=mermaid_body,
                    chart_type=_CHART_CODE_TYPE[code],
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
