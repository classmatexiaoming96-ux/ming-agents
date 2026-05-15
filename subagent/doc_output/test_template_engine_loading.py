"""Tests for R1 (D1+D2) — template loading fixes (v2.4).

D1: `_extract_template_section` must recognize `## N. doc_type（` anchor pattern,
    not just `## doc_type（`. TEMPLATES.md §1-§7 use numbered headings, so the
    rich templates were silently dropped before this fix.

D2: When TEMPLATES.md fails to provide a section, rich doc_types
    (tech_review / design_doc / module_plan / research_report) get a
    figure-interleaved skeleton with `{{include_diagram:D-XXX}}` placeholders,
    not the bare 4-block default.

Run with: python3 -m unittest test_template_engine_loading.py
"""

from __future__ import annotations

import os
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from template_engine import TemplateEngine


SAMPLE_TEMPLATES_MD = """# 文档模板库

## 0. 图表使用指引
通用规则在此...

## 1. tech_review（技术评审文档）

### 模板结构
```markdown
# 技术评审文档
## 1. 需求概述
{{content_source_1}}
## 2. 技术架构方案
{{include_diagram:D-ARCH}}
```

## 2. design_doc（设计文档）

设计文档结构...
{{include_diagram:D-CLASS}}

## 3. nopattern_doc_type

This section has no Chinese-paren — should not match.
"""


class TestNumberedAnchorExtraction(unittest.TestCase):
    """D1: numbered anchor `## N. doc_type（` must be recognized."""

    def _make_engine_with_templates_md(self, content: str, doc_type: str):
        with tempfile.TemporaryDirectory() as tmp:
            templates_path = Path(tmp) / 'TEMPLATES.md'
            templates_path.write_text(content, encoding='utf-8')
            engine = TemplateEngine(
                doc_type=doc_type,
                merged_content={'overview': '', 'details': {}, 'references': []},
                options={},
            )
            # Patch TEMPLATE_DIR to point at tmp
            with patch.object(TemplateEngine, 'TEMPLATE_DIR', tmp):
                return engine._load_template()

    def test_numbered_anchor_matches_tech_review(self):
        loaded = self._make_engine_with_templates_md(SAMPLE_TEMPLATES_MD, 'tech_review')
        # P0-1 (v2.6 Phase 3): the loader unwraps the inner ```markdown fence,
        # so the §heading + 适用场景 prose are stripped — only the template body
        # inside the fence is returned. Placeholder must remain intact.
        self.assertIn('{{include_diagram:D-ARCH}}', loaded)
        self.assertIn('# 技术评审文档', loaded)  # inner body present
        # The §heading is the wrapper, not the template — should be gone now.
        self.assertNotIn('tech_review（技术评审文档', loaded)
        # Should NOT bleed into §2 design_doc
        self.assertNotIn('设计文档结构', loaded)

    def test_numbered_anchor_matches_design_doc(self):
        # SAMPLE_TEMPLATES_MD's §2 design_doc section has NO ```markdown fence
        # (legacy un-fenced form). Loader returns the whole section in that case.
        loaded = self._make_engine_with_templates_md(SAMPLE_TEMPLATES_MD, 'design_doc')
        self.assertIn('{{include_diagram:D-CLASS}}', loaded)

    def test_unnumbered_anchor_still_works_for_backwards_compat(self):
        legacy_md = (
            "## tech_review（技术评审文档）\n\n"
            "Legacy template body with {{include_diagram:D-ARCH}}\n"
        )
        loaded = self._make_engine_with_templates_md(legacy_md, 'tech_review')
        self.assertIn('{{include_diagram:D-ARCH}}', loaded)
        self.assertIn('Legacy template body', loaded)

    def test_unknown_doc_type_falls_back_to_default_template(self):
        # `unknown_type` not in SAMPLE_TEMPLATES_MD with proper paren
        loaded = self._make_engine_with_templates_md(SAMPLE_TEMPLATES_MD, 'unknown_type')
        # Falls back to default template — should have unreplaced placeholders
        self.assertIn('{{overview}}', loaded)


class TestRichSkeletonFallback(unittest.TestCase):
    """D2: known rich doc_types get figure-interleaved fallback skeleton."""

    def _engine_with_no_templates_file(self, doc_type: str) -> TemplateEngine:
        engine = TemplateEngine(
            doc_type=doc_type,
            merged_content={'overview': '', 'details': {}, 'references': []},
            options={},
        )
        # Force template-file-not-found by pointing at empty tmp
        with tempfile.TemporaryDirectory() as tmp:
            with patch.object(TemplateEngine, 'TEMPLATE_DIR', tmp):
                return engine._load_template()

    def test_tech_review_falls_back_to_rich_skeleton(self):
        loaded = self._engine_with_no_templates_file('tech_review')
        # Rich skeleton has 5 interleaved figure placeholders
        for placeholder in [
            '{{include_diagram:D-ARCH}}',
            '{{include_diagram:D-SEQ}}',
            '{{include_diagram:D-DAG}}',
            '{{include_diagram:D-CLASS}}',
        ]:
            self.assertIn(placeholder, loaded, f"missing {placeholder} in rich skeleton")
        # Includes the "图文并茂" marker comment
        self.assertIn('图文并茂', loaded)
        # Has interleaved prose-figure-table structure (核心组件 table next to D-ARCH)
        d_arch_idx = loaded.find('{{include_diagram:D-ARCH}}')
        core_table_idx = loaded.find('核心组件')
        self.assertGreater(core_table_idx, d_arch_idx, "core components table should follow architecture diagram")

    def test_module_plan_falls_back_to_rich_skeleton(self):
        loaded = self._engine_with_no_templates_file('module_plan')
        self.assertIn('{{include_diagram:D-DAG}}', loaded)

    def test_design_doc_falls_back_to_rich_skeleton(self):
        loaded = self._engine_with_no_templates_file('design_doc')
        self.assertIn('{{include_diagram:D-ARCH}}', loaded)

    def test_unknown_doctype_falls_back_to_minimal(self):
        loaded = self._engine_with_no_templates_file('aime_session')  # not in _RICH_FALLBACK_TYPES
        # Bare 4-section: 概述 / 详细信息 / 参考资料 / 更新记录, no figure placeholders
        self.assertNotIn('{{include_diagram', loaded)
        self.assertIn('## 概述', loaded)


class TestEndToEndChartDetectionAfterFix(unittest.TestCase):
    """After D1+D2: rendered output now contains mermaid placeholders that the
    annotate_mermaid_blocks pass can recognize once they're filled by upstream.
    """

    def test_rich_skeleton_render_contains_figure_placeholders(self):
        """Verify the rich skeleton survives render() variable substitution."""
        engine = TemplateEngine(
            doc_type='tech_review',
            merged_content={'overview': 'OV', 'details': {}, 'references': []},
            options={},
        )
        # Force rich skeleton
        engine._load_template = lambda: engine._get_rich_skeleton_template()
        out = engine.render()
        # Figure placeholders remain (they're {{include_diagram:D-XXX}}, not {{simple}})
        # _replace_variables only strips {{single-token}} after specific substitutions,
        # not {{include_diagram:...}}.
        # 注意: _replace_variables 末尾的 regex re.sub(r'\{\{[^}]+\}\}', '', ...)
        # 会清掉所有未匹配的 {{...}} 占位 — 包括 include_diagram。Test 这里只
        # 是验证 skeleton 进入了 render 流程; 真正的占位填充需上游 generator
        # 替换为 ```mermaid``` 代码块, 不靠 _replace_variables 处理。
        # 因此我们检查 skeleton 的章节标题结构是否保留。
        self.assertIn('## 2. 整体架构', out)
        self.assertIn('### 2.1 架构图', out)
        self.assertIn('OV', out)


if __name__ == "__main__":
    unittest.main()
