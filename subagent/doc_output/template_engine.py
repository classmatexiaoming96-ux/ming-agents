#!/usr/bin/env python3
"""
模板引擎模块

负责根据文档类型选择模板，并将融合后的内容填入模板。

v2.4 起在 render() 末尾增加 mermaid 块自动标注 (annotate_mermaid 选项控制, 默认 ON):
为每个未标注的 ```mermaid``` 代码块按语法启发式 prepend `<!-- chart: D-XXX -->` 注释,
使 FeishuPublisher.publish_with_charts 能识别哪些块走画板升级 (与 TEMPLATES.md §0 对齐)。
"""

from typing import Dict, Any, Optional
from datetime import datetime
import os
import re


# --- mermaid auto-annotation (v2.4) ----------------------------------------

# 匹配单个 ```mermaid ... ``` 围栏: 第 1 组是 fence 前的"可能已有的注释行"+ 紧邻的换行;
# 第 2 组是 mermaid body. 用 DOTALL + 非贪婪以正确处理多块。
_MERMAID_FENCE_RE = re.compile(
    r"(^|\n)([ \t]*)```mermaid\n(.*?)\n[ \t]*```",
    re.DOTALL,
)

# 已存在 `<!-- chart: D-XXX -->` 注释 (允许该注释与 fence 之间有空白/换行) — 用于幂等检测。
_EXISTING_ANNOTATION_RE = re.compile(
    r"<!--\s*chart:\s*D-[A-Z]+\s*-->\s*\Z",
    re.DOTALL,
)


def _classify_mermaid_body(body: str) -> Optional[str]:
    """Pick the chart code (D-XXX) that best matches a mermaid block body.

    Heuristic precedence (first match wins). Returns None when nothing matches —
    caller should leave the block un-annotated rather than guess.
    """
    head = body.lstrip()
    first_line = head.split("\n", 1)[0].strip()
    lower_head = head.lower()

    if first_line.startswith("sequenceDiagram"):
        return "D-SEQ"
    if first_line.startswith("classDiagram"):
        return "D-CLASS"
    if first_line.startswith("erDiagram"):
        return "D-CLASS"  # ER 图与类图同档, 用 D-CLASS 别名
    if first_line.startswith("stateDiagram"):
        return "D-STATE"
    if first_line.startswith("mindmap"):
        return "D-MIND"
    if first_line.startswith("gantt"):
        return "D-GANTT"
    if first_line.startswith(("flowchart", "graph")):
        # flowchart 子分类: Phase 子图 → D-DAG; 其他 subgraph → D-ARCH; 无 subgraph → D-DECISION
        if re.search(r"\bsubgraph\s+Phase", body):
            return "D-DAG"
        if re.search(r"\bsubgraph\b", body):
            return "D-ARCH"
        return "D-DECISION"
    return None


def annotate_mermaid_blocks(content: str) -> str:
    """Insert `<!-- chart: D-XXX -->` before each unannotated ```mermaid``` fence.

    Idempotent: a fence whose immediately-preceding non-blank line is already
    `<!-- chart: D-XXX -->` is left untouched. Mermaid blocks whose syntax
    doesn't match any known classifier are left as-is (no guess annotation).

    Returns the modified markdown.
    """
    def _replace(match: re.Match) -> str:
        leading_break = match.group(1)
        indent = match.group(2)
        body = match.group(3)
        chart_code = _classify_mermaid_body(body)
        if chart_code is None:
            return match.group(0)
        # Idempotency: examine up to 3 lines before this fence within `content` global.
        # Easiest robust check: look at the substring just before this fence's start in `content`.
        prefix_start = max(0, match.start() - 200)
        preceding = content[prefix_start:match.start()]
        if _EXISTING_ANNOTATION_RE.search(preceding):
            return match.group(0)
        annotation = f"<!-- chart: {chart_code} -->\n"
        return f"{leading_break}{indent}{annotation}{indent}```mermaid\n{body}\n{indent}```"

    return _MERMAID_FENCE_RE.sub(_replace, content)


# --- end mermaid auto-annotation ------------------------------------------


class TemplateEngine:
    """模板引擎"""
    
    # 模板目录
    TEMPLATE_DIR = os.path.join(
        os.path.dirname(os.path.abspath(__file__)), 
        'references'
    )
    
    # 文档类型到模板文件的映射
    TEMPLATE_FILES = {
        'tech_review': 'TEMPLATES.md',
        'design_doc': 'TEMPLATES.md',
        'research_report': 'TEMPLATES.md',
        'module_plan': 'TEMPLATES.md',
        'task_plan': 'TEMPLATES.md',
        'test_plan': 'TEMPLATES.md'
    }
    
    def __init__(self, doc_type: str, merged_content: Dict[str, Any], 
                 options: Dict[str, Any]):
        self.doc_type = doc_type
        self.merged_content = merged_content
        self.options = options
        self.template_variant = options.get('template_variant', 'default')
        self.include_diagrams = options.get('include_diagrams', True)
        # v2.4: 默认对 ```mermaid 块做 D-XXX 自动标注; 关掉传 annotate_mermaid=False
        self.annotate_mermaid = options.get('annotate_mermaid', True)

    def render(self) -> str:
        """渲染文档"""
        template = self._load_template()

        # 替换变量
        content = self._replace_variables(template)

        # v2.4: 给 ```mermaid 块加 <!-- chart: D-XXX --> 注释 (供 chart_publisher 识别)
        if self.annotate_mermaid:
            content = annotate_mermaid_blocks(content)

        return content
    
    def _load_template(self) -> str:
        """加载模板"""
        template_file = self.TEMPLATE_FILES.get(self.doc_type, 'TEMPLATES.md')
        template_path = os.path.join(self.TEMPLATE_DIR, template_file)
        
        if not os.path.exists(template_path):
            return self._get_default_template()
        
        with open(template_path, 'r', encoding='utf-8') as f:
            content = f.read()
        
        # 提取对应类型的模板
        return self._extract_template_section(content, self.doc_type)
    
    def _extract_template_section(self, content: str, doc_type: str) -> str:
        """从模板文件中提取指定类型的模板。

        TEMPLATES.md 的节标题既可能是 ``## tech_review（...``(无编号),
        也可能是 ``## 1. tech_review（...``(带数字编号 + 半角点 + 空格 +
        中文 doc_type 全角括号开头, e.g. `## 1. tech_review（技术评审文档）`)。
        旧实现只识别第一种, 导致带数字编号的节(目前 TEMPLATES.md 全部如此)
        永远 fallback 到默认模板, 富模板里的 `{{include_diagram:D-XXX}}`
        占位符从未生效。
        """
        # 先试无编号形式
        start_marker = f"## {doc_type}（"
        start_idx = content.find(start_marker)

        # 再试带编号形式 `## N. doc_type（`
        if start_idx == -1:
            numbered_re = re.compile(
                rf'^##\s+\d+\.\s+{re.escape(doc_type)}（',
                re.MULTILINE,
            )
            match = numbered_re.search(content)
            if match:
                start_idx = match.start()

        if start_idx == -1:
            return self._get_default_template()

        # 找到下一个同级章节的开始 (## 开头, 不含 ###)
        # 注意: TEMPLATES.md 的每节里都嵌着 ```markdown ... ``` 代码块,
        # 块内出现的 `## xxx` 是模板示例不是真章节标题, 必须跳过。
        end_idx = self._find_next_section_outside_fence(content, start_idx + 1)
        if end_idx == -1:
            template = content[start_idx:]
        else:
            template = content[start_idx:end_idx]

        return template

    @staticmethod
    def _find_next_section_outside_fence(content: str, search_start: int) -> int:
        """Return index of the next `\\n## ` that is *outside* any ``` fence.

        Walks lines from the line *after* search_start, tracking fence state.
        Returns -1 if no such boundary exists (caller treats as EOF).
        """
        # Advance to the start of the next line after search_start
        first_newline = content.find('\n', search_start)
        if first_newline == -1:
            return -1
        in_fence = False
        offset = first_newline + 1
        remaining = content[offset:]
        for line in remaining.splitlines(keepends=True):
            line_offset = offset
            offset += len(line)
            stripped = line.lstrip()
            if stripped.startswith('```'):
                in_fence = not in_fence
                continue
            if in_fence:
                continue
            if line.startswith('## '):
                # Return position of \n just before this header
                return line_offset - 1
        return -1
    
    # v2.4 起, 已知 rich doc_type 在 TEMPLATES.md 加载失败时也使用图文交织 skeleton。
    # 参考飞书技术评审标准模板 (wikcnSeuhhO00BBpYwl22cL5zoh) 的 "多画图,少写字"
    # 原则,以及 TEMPLATES.md §0.4 的 D-XXX mermaid 骨架。
    _RICH_FALLBACK_TYPES = {
        'tech_review', 'design_doc', 'module_plan', 'research_report',
    }

    def _get_default_template(self) -> str:
        """获取默认模板。

        Routing:
          - doc_type ∈ _RICH_FALLBACK_TYPES → 返回含 mermaid 占位的图文交织骨架
            (`_get_rich_skeleton_template`), TEMPLATES.md 加载失败时也保证产
            "图文并茂" 而非 4 节空壳。
          - 其它 doc_type → 旧的扁平 4 节模板。

        注意 {{{{name}}}} 在 f-string 里会被处理为 {{name}}, 留给后续
        _replace_variables 匹配 {{name}} 占位符。早期版本写的是 {{name}}
        (在 f-string 里会塌缩成 {name}, _replace_variables 永远匹配不到),
        导致 overview/details/references 从未被替换。
        """
        if self.doc_type in self._RICH_FALLBACK_TYPES:
            return self._get_rich_skeleton_template()
        return self._get_minimal_default_template()

    def _get_minimal_default_template(self) -> str:
        """扁平 4 节最小模板, 给未识别 doc_type 用。"""
        title = self._get_doc_title()
        date_now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        date_today = datetime.now().strftime('%Y-%m-%d')
        return f"""# {title}

> 生成时间：{date_now}
> 数据来源：自动融合多个输入源

---

## 概述

{{{{overview}}}}

---

## 详细信息

{{{{details}}}}

---

## 参考资料

{{{{references}}}}

---

## 更新记录

| 版本 | 日期 | 更新内容 |
|------|------|----------|
| v1.0 | {date_today} | 初始版本 |
"""

    def _get_rich_skeleton_template(self) -> str:
        """图文交织 skeleton, 给已知 rich doc_type 用。

        节内顺序: 来源标注 → prose 段 → mermaid 占位 + 图说明 → 表 → mermaid 占位 + 图说明。
        遵循 wiki 标准模板的 "多画图,少写字" 原则 (wikcnSeuhhO00BBpYwl22cL5zoh)
        与 TEMPLATES.md §0.6 的 "每图配 1 行说明"。

        `{{{{include_diagram:D-XXX}}}}` 占位由后续 mermaid generator (PM-Agent prompt
        或人工填写) 替换为实际 ```mermaid 代码块, 然后 template_engine.annotate_mermaid_blocks
        会按语法挂 `<!-- chart: D-XXX -->`, chart_publisher 升级到飞书画板。
        """
        title = self._get_doc_title()
        date_now = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
        date_today = datetime.now().strftime('%Y-%m-%d')
        return f"""# {title}

> 生成时间：{date_now}
> 数据来源：自动融合多个输入源（图文并茂 skeleton, v2.4+）

---

## 1. 需求概述

{{{{overview}}}}

---

## 2. 整体架构

> 章节原则：先 1-2 段叙述上下文，再上架构图，最后用表对齐到组件名。

{{{{details}}}}

### 2.1 架构图

{{{{include_diagram:D-ARCH}}}}

> 图说明：节点 ID 必须与下方"核心组件"表的组件名一致；禁止 ASCII art，禁止重复"核心组件"表已有的字段。

### 2.2 核心组件

| 组件名称 | 职责 | 技术选型 |
|----------|------|----------|
| | | |

### 2.3 关键接口时序

{{{{include_diagram:D-SEQ}}}}

> 图说明：挑 1-2 个最关键的接口（主链路写入、跨模块查询等）画时序图；participant 必须复用 2.2 表中组件名。

---

## 3. 模块依赖

{{{{include_diagram:D-DAG}}}}

> 图说明：用 `subgraph Phase*` 区分阶段；节点 = 模块；边 = 输入依赖。失败回退见 TEMPLATES.md §0.4。

---

## 4. 数据模型

> 章节原则：先 1-2 段实体关系叙述，再上类图，最后字段表对齐到实体名。

| 实体名称 | 字段 | 说明 |
|----------|------|------|
| | | |

{{{{include_diagram:D-CLASS}}}}

> 图说明：类名与上表实体一致；字段类型用原生类型；关系边标 `1`/`N` 基数。

---

## 5. 参考资料

{{{{references}}}}

---

## 更新记录

| 版本 | 日期 | 更新内容 |
|------|------|----------|
| v1.0 | {date_today} | 初始版本 |
"""

    def _replace_variables(self, template: str) -> str:
        """替换模板变量"""
        # 生成文档标题
        title = self._get_doc_title()
        template = template.replace('{{title}}', title)
        template = template.replace('{{date}}', datetime.now().strftime('%Y-%m-%d'))
        template = template.replace('{{author}}', 'Shrimp Team')
        
        # 替换概览
        overview = self._format_overview()
        template = template.replace('{{overview}}', overview)
        
        # 替换详细信息
        details = self._format_details()
        template = template.replace('{{details}}', details)
        
        # 替换参考资料
        references = self._format_references()
        template = template.replace('{{references}}', references)
        
        # 移除未替换的变量
        import re
        template = re.sub(r'\{\{[^}]+\}\}', '', template)
        
        return template
    
    def _get_doc_title(self) -> str:
        """获取文档标题"""
        titles = {
            'tech_review': '技术评审文档',
            'design_doc': '设计文档',
            'research_report': '调研报告',
            'module_plan': '模块拆分文档',
            'task_plan': 'Task规划文档',
            'test_plan': '测试计划'
        }
        return titles.get(self.doc_type, '文档')
    
    def _format_overview(self) -> str:
        """格式化概览内容"""
        overview = self.merged_content.get('overview', '')
        if not overview:
            return '暂无概览信息'
        
        return overview
    
    def _format_details(self) -> str:
        """格式化详细信息"""
        details = self.merged_content.get('details', {})
        if not details:
            return '暂无详细信息'
        
        parts = []
        for key, value in details.items():
            source_type = value.get('type', 'unknown')
            
            if source_type == 'code_repository':
                parts.append(f"### 📦 {key}\n\n{value.get('content', '')}")
            elif source_type == 'feishu_doc':
                parts.append(f"### 📄 {key}\n\n{value.get('content', '')}")
            elif source_type == 'web':
                parts.append(f"### 🌐 {key}\n\n{value.get('content', '')}")
        
        return '\n\n'.join(parts) if parts else '暂无详细信息'
    
    def _format_references(self) -> str:
        """格式化参考资料"""
        references = self.merged_content.get('references', [])
        if not references:
            return '无参考资料'
        
        lines = []
        for ref in references:
            ref_type = ref.get('type', '')
            ref_title = ref.get('title', '')
            ref_url = ref.get('url', '')
            
            if ref_type == 'code_repository':
                lines.append(f"- 📦 [{ref_title}]({ref_url})")
            elif ref_type == 'feishu_doc':
                lines.append(f"- 📄 [{ref_title}]({ref_url})")
            elif ref_type == 'web':
                lines.append(f"- 🌐 [{ref_title}]({ref_url})")
        
        return '\n'.join(lines)
